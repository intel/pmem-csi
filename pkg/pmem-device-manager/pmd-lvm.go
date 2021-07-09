package pmdmanager

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"sync"

	api "github.com/intel/pmem-csi/pkg/apis/pmemcsi/v1beta1"
	pmemerr "github.com/intel/pmem-csi/pkg/errors"
	pmemexec "github.com/intel/pmem-csi/pkg/exec"
	pmemlog "github.com/intel/pmem-csi/pkg/logger"
	"github.com/intel/pmem-csi/pkg/ndctl"
	pmemcommon "github.com/intel/pmem-csi/pkg/pmem-common"
)

const (
	// 4 MB alignment is used by LVM
	lvmAlign uint64 = 4 * 1024 * 1024

	// special alt name that a namespace must have to be managed by PMEM-CSI.
	pmemCSINamespaceName = "pmem-csi"
)

type pmemLvm struct {
	volumeGroups []string
	devices      map[string]*PmemDeviceInfo
}

var _ PmemDeviceManager = &pmemLvm{}
var lvsArgs = []string{"--noheadings", "--nosuffix", "-o", "lv_name,lv_path,lv_size", "--units", "B"}
var vgsArgs = []string{"--noheadings", "--nosuffix", "-o", "vg_name,vg_size,vg_free", "--units", "B"}

// mutex to synchronize all LVM calls
// The reason we chose not to support concurrent LVM calls was
// due to LVM's inconsistent behavior when made concurrent calls
// in our stress tests. One should revisit this and choose better
// suitable synchronization policy.
var lvmMutex = &sync.Mutex{}

// NewPmemDeviceManagerLVM Instantiates a new LVM based pmem device manager
func newPmemDeviceManagerLVM(ctx context.Context, pmemPercentage uint) (PmemDeviceManager, error) {
	ctx, logger := pmemlog.WithName(ctx, "LVM-New")

	if pmemPercentage > 100 {
		return nil, fmt.Errorf("invalid pmemPercentage '%d'. Value must be 0..100", pmemPercentage)
	}
	lvmMutex.Lock()
	defer lvmMutex.Unlock()

	ndctx, err := ndctl.NewContext()
	if err != nil {
		return nil, err
	}
	defer ndctx.Free()

	volumeGroups := []string{}
	for _, bus := range ndctx.GetBuses() {
		for _, r := range bus.ActiveRegions() {
			vgName := pmemcommon.VgName(bus, r)
			if r.Type() != ndctl.PmemRegion {
				logger.Info("Region is not suitable for fsdax, skipping it", "id", r.ID(), "device", r.DeviceName())
				continue
			}

			if err := setupNS(ctx, r, pmemPercentage); err != nil {
				return nil, err
			}
			if err := setupVG(ctx, r, vgName); err != nil {
				return nil, err
			}
			if _, err := pmemexec.RunCommand(ctx, "vgs", vgName); err != nil {
				logger.V(5).Info("Volume group non-existent, skipping it", "vg", vgName)
			} else {
				volumeGroups = append(volumeGroups, vgName)
			}
		}
	}

	return newPmemDeviceManagerLVMForVGs(ctx, volumeGroups)
}

func (pmem *pmemLvm) GetMode() api.DeviceMode {
	return api.DeviceModeLVM
}

func newPmemDeviceManagerLVMForVGs(ctx context.Context, volumeGroups []string) (PmemDeviceManager, error) {
	devices, err := listDevices(ctx, volumeGroups...)
	if err != nil {
		return nil, err
	}

	return &pmemLvm{
		volumeGroups: volumeGroups,
		devices:      devices,
	}, nil
}

type vgInfo struct {
	name string
	size uint64
	free uint64
}

func (lvm *pmemLvm) GetCapacity(ctx context.Context) (capacity Capacity, err error) {
	logger := pmemlog.Get(ctx).WithName("LVM-GetCapacity")
	ctx = pmemlog.Set(ctx, logger)

	lvmMutex.Lock()
	defer lvmMutex.Unlock()

	var vgs []vgInfo
	vgs, err = getVolumeGroups(ctx, lvm.volumeGroups)
	if err != nil {
		return
	}

	for _, vg := range vgs {
		if vg.free > capacity.MaxVolumeSize {
			capacity.MaxVolumeSize = vg.free / lvmAlign * lvmAlign
		}
		capacity.Available += vg.free
		capacity.Managed += vg.size
		capacity.Total, err = totalSize()
		if err != nil {
			return
		}
	}

	return capacity, nil
}

func (lvm *pmemLvm) CreateDevice(ctx context.Context, volumeId string, size uint64) (uint64, error) {
	ctx, logger := pmemlog.WithName(ctx, "LVM-CreateDevice")

	lvmMutex.Lock()
	defer lvmMutex.Unlock()
	// Check that such volume does not exist. In certain error states, for example when
	// namespace creation works but device zeroing fails (missing /dev/pmemX.Y in container),
	// this function is asked to create new devices repeatedly, forcing running out of space.
	// Avoid device filling with garbage entries by returning error.
	// Overall, no point having more than one namespace with same volumeId.
	if _, err := lvm.getDevice(volumeId); err == nil {
		return 0, pmemerr.DeviceExists
	}
	vgs, err := getVolumeGroups(ctx, lvm.volumeGroups)
	if err != nil {
		return 0, err
	}
	// Adjust up to next alignment boundary, if not aligned already.
	actual := (size + lvmAlign - 1) / lvmAlign * lvmAlign
	if actual == 0 {
		actual = lvmAlign
	}
	if actual != size {
		logger.V(3).Info("Increased size to satisfy LVM alignment",
			"old-size", pmemlog.CapacityRef(int64(size)),
			"new-size", pmemlog.CapacityRef(int64(actual)),
			"alignment", pmemlog.CapacityRef(int64(lvmAlign)))
	}
	strSz := strconv.FormatUint(actual, 10) + "B"

	for _, vg := range vgs {
		// use first Vgroup with enough available space
		if vg.free >= actual {
			// In some container environments clearing device fails with race condition.
			// So, we ask lvm not to clear(-Zn) the newly created device, instead we do ourself in later stage.
			// lvcreate takes size in MBytes if no unit
			if _, err := pmemexec.RunCommand(ctx, "lvcreate", "-Zn", "-L", strSz, "-n", volumeId, vg.name); err != nil {
				logger.V(3).Info("lvcreate failed with error, trying next free region", "error", err)
			} else {
				// clear start of device to avoid old data being recognized as file system
				device, err := getUncachedDevice(ctx, volumeId, vg.name)
				if err != nil {
					return 0, err
				}
				if err := waitDeviceAppears(ctx, device); err != nil {
					return 0, err
				}
				if err := clearDevice(ctx, device, false); err != nil {
					return 0, fmt.Errorf("clear device %q: %v", volumeId, err)
				}

				lvm.devices[device.VolumeId] = device

				return actual, nil
			}
		}
	}
	return 0, pmemerr.NotEnoughSpace
}

func (lvm *pmemLvm) DeleteDevice(ctx context.Context, volumeId string, flush bool) error {
	ctx, _ = pmemlog.WithName(ctx, "LVM-DeleteDevice")

	lvmMutex.Lock()
	defer lvmMutex.Unlock()

	var err error
	var device *PmemDeviceInfo

	if device, err = lvm.getDevice(volumeId); err != nil {
		if errors.Is(err, pmemerr.DeviceNotFound) {
			return nil
		}
		return err
	}
	if err := clearDevice(ctx, device, flush); err != nil {
		if errors.Is(err, pmemerr.DeviceNotFound) {
			// Remove device from cache
			delete(lvm.devices, volumeId)
			return nil
		}
		return err
	}

	if _, err := pmemexec.RunCommand(ctx, "lvremove", "-fy", device.Path); err != nil {
		return err
	}

	// Remove device from cache
	delete(lvm.devices, volumeId)

	return nil
}

func (lvm *pmemLvm) ListDevices(ctx context.Context) ([]*PmemDeviceInfo, error) {
	lvmMutex.Lock()
	defer lvmMutex.Unlock()

	devices := []*PmemDeviceInfo{}
	for _, dev := range lvm.devices {
		devices = append(devices, dev)
	}

	return devices, nil
}

func (lvm *pmemLvm) GetDevice(ctx context.Context, volumeId string) (*PmemDeviceInfo, error) {
	lvmMutex.Lock()
	defer lvmMutex.Unlock()

	return lvm.getDevice(volumeId)
}

func (lvm *pmemLvm) getDevice(volumeId string) (*PmemDeviceInfo, error) {
	if dev, ok := lvm.devices[volumeId]; ok {
		return dev, nil
	}

	return nil, pmemerr.DeviceNotFound
}

func getUncachedDevice(ctx context.Context, volumeId string, volumeGroup string) (*PmemDeviceInfo, error) {
	devices, err := listDevices(ctx, volumeGroup)
	if err != nil {
		return nil, err
	}

	if dev, ok := devices[volumeId]; ok {
		return dev, nil
	}

	return nil, pmemerr.DeviceNotFound
}

// listDevices Lists available logical devices in given volume groups
func listDevices(ctx context.Context, volumeGroups ...string) (map[string]*PmemDeviceInfo, error) {
	args := append(lvsArgs, volumeGroups...)
	output, err := pmemexec.RunCommand(ctx, "lvs", args...)
	if err != nil {
		return nil, fmt.Errorf("lvs failure : %v", err)
	}
	return parseLVSOutput(output)
}

//lvs options "lv_name,lv_path,lv_size,lv_free"
func parseLVSOutput(output string) (map[string]*PmemDeviceInfo, error) {
	devices := map[string]*PmemDeviceInfo{}
	lines := strings.Split(output, "\n")
	for _, line := range lines {
		fields := strings.Fields(strings.TrimSpace(line))
		if len(fields) != 3 {
			continue
		}

		dev := &PmemDeviceInfo{}
		dev.VolumeId = fields[0]
		dev.Path = fields[1]
		dev.Size, _ = strconv.ParseUint(fields[2], 10, 64)

		devices[dev.VolumeId] = dev
	}

	return devices, nil
}

func getVolumeGroups(ctx context.Context, groups []string) ([]vgInfo, error) {
	ctx, _ = pmemlog.WithName(ctx, "getVolumeGroups")

	vgs := []vgInfo{}
	args := append(vgsArgs, groups...)
	output, err := pmemexec.RunCommand(ctx, "vgs", args...)
	if err != nil {
		return vgs, fmt.Errorf("vgs failure: %v", err)
	}
	for _, line := range strings.SplitN(output, "\n", len(groups)) {
		fields := strings.Fields(strings.TrimSpace(line))
		if len(fields) != 3 {
			return vgs, fmt.Errorf("failed to parse vgs output: %q", line)
		}
		vg := vgInfo{}
		vg.name = fields[0]
		vg.size, _ = strconv.ParseUint(fields[1], 10, 64)
		vg.free, _ = strconv.ParseUint(fields[2], 10, 64)
		vgs = append(vgs, vg)
	}

	return vgs, nil
}

// setupNS checks if a namespace needs to be created in the region and if so, does that.
func setupNS(ctx context.Context, r ndctl.Region, percentage uint) error {
	ctx, logger := pmemlog.WithName(ctx, "setupNS")
	canUse := uint64(percentage) * r.Size() / 100
	logger.V(3).Info("Checking region for fsdax namespaces",
		"region", r.DeviceName(),
		"percentage", percentage,
		"size", pmemlog.CapacityRef(int64(r.Size())),
		"available", pmemlog.CapacityRef(int64(r.AvailableSize())),
		"max-available-extent", pmemlog.CapacityRef(int64(r.MaxAvailableExtent())),
		"may-use", pmemlog.CapacityRef(int64(canUse)))
	// Subtract sizes of existing active namespaces with currently handled mode and owned by pmem-csi
	for _, ns := range r.ActiveNamespaces() {
		logger.V(3).Info("Existing namespace",
			"usable-size", pmemlog.CapacityRef(int64(ns.Size())),
			"raw-size", pmemlog.CapacityRef(int64(ns.RawSize())),
			"mode", ns.Mode(),
			"device", ns.DeviceName(),
			"name", ns.Name())
		if ns.Name() != pmemCSINamespaceName {
			continue
		}
		used := ns.RawSize()
		if used >= canUse {
			logger.V(3).Info("All allowed space already in use by PMEM-CSI.")
			canUse = 0
			break
		}
		canUse -= used
	}
	// Because of overhead by alignment and extra space for page mapping, calculated available may show more than actual
	if r.AvailableSize() < canUse {
		logger.V(3).Info("Available space in region is less than desired size, limit to that",
			"available", pmemlog.CapacityRef(int64(r.AvailableSize())))
		canUse = r.AvailableSize()
	}
	// Should not happen often: fragmented space could lead to r.MaxAvailableExtent() being less than r.AvailableSize()
	if r.MaxAvailableExtent() < canUse {
		logger.V(3).Info("Maximum namespace size in region is less than desired size due to fragmentation, limit to that",
			"max-available-extent", pmemlog.CapacityRef(int64(r.MaxAvailableExtent())))
		canUse = r.MaxAvailableExtent()
	}
	if canUse > 0 {
		logger.V(3).Info("Create fsdax namespace", "size", pmemlog.CapacityRef(int64(canUse)))
		ns, err := r.CreateNamespace(ctx, ndctl.CreateNamespaceOpts{
			Name: "pmem-csi",
			Mode: "fsdax",
			Size: canUse,
		})
		if err != nil {
			return fmt.Errorf("failed to create PMEM namespace with size '%d' in region '%s': %v", canUse, r.DeviceName(), err)
		}
		// Wipe out any old filesystem or LVM signatures. Without this we might get
		// duplicate volume groups when accidentally restoring a namespace that existed
		// before and was used in a volume group. This is not idempotent, but hopefully
		// it'll never fail or if it does, can be skipped when the driver tries again.
		if _, err := pmemexec.RunCommand(ctx, "wipefs", "--all", "--force", "/dev/"+ns.BlockDeviceName()); err != nil {
			return fmt.Errorf("failed to wipe new namespace: %v", err)
		}
	}

	return nil
}

// setupVG ensures that all namespaces with name "pmem-csi" in the region
// are part of the volume group.
func setupVG(ctx context.Context, r ndctl.Region, vgName string) error {
	ctx, logger := pmemlog.WithName(ctx, "setupVG")
	nsArray := r.ActiveNamespaces()
	if len(nsArray) == 0 {
		logger.V(3).Info("No active namespaces, nothing to do", "region", r.DeviceName())
		return nil
	}
	var devNames []string
	for _, ns := range nsArray {
		// consider only namespaces having name given by this driver, to exclude foreign ones
		if ns.Name() == pmemCSINamespaceName {
			devName := "/dev/" + ns.BlockDeviceName()
			devNames = append(devNames, devName)
		}
	}
	if len(devNames) == 0 {
		logger.V(3).Info("No namespace found to add to the volume group", "vg", vgName)
		return nil
	}
	return setupVGForNamespaces(ctx, vgName, devNames...)
}

// setupVGForNamespaces ensures that the given namespace are in the volume group,
// creating it if necessary. Namespaces that are already in a group are ignored.
func setupVGForNamespaces(ctx context.Context, vgName string, devNames ...string) error {
	ctx, logger := pmemlog.WithName(ctx, "setupVGForNamespace")
	var unusedDevNames []string
	for _, devName := range devNames {
		// check if this pv is already part of a group, if yes ignore
		// this pv if not add to arg list
		output, err := pmemexec.RunCommand(ctx, "pvs", "--noheadings", "-o", "vg_name", devName)
		output = strings.TrimSpace(output)
		if err != nil || len(output) == 0 {
			unusedDevNames = append(unusedDevNames, devName)
		} else {
			logger.V(3).Info("Namespace already part of a volume group", "namespace", devName, "vg", output)
		}
	}
	if len(unusedDevNames) == 0 {
		logger.V(3).Info("No unused namespace found to add to the volume group", "vg", vgName)
		return nil
	}

	cmd := ""
	if _, err := pmemexec.RunCommand(ctx, "vgdisplay", vgName); err != nil {
		logger.V(3).Info("Creating new volume group", "vg", vgName)
		cmd = "vgcreate"
	} else {
		logger.V(3).Info("Volume group exists, extending it", "vg", vgName)
		cmd = "vgextend"
	}

	cmdArgs := []string{"--force", vgName}
	cmdArgs = append(cmdArgs, unusedDevNames...)
	_, err := pmemexec.RunCommand(ctx, cmd, cmdArgs...) //nolint gosec
	if err != nil {
		return fmt.Errorf("failed to create/extend volume group '%s': %v", vgName, err)
	}
	return nil
}
