package pmdmanager

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
	"sync"

	"github.com/intel/pmem-csi/pkg/ndctl"
	pmemexec "github.com/intel/pmem-csi/pkg/pmem-exec"
	"k8s.io/klog"
)

const (
	// 4 MB alignment is used by LVM
	lvmAlign uint64 = 4 * 1024 * 1024
)

type pmemLvm struct {
	volumeGroups []string
	devices      map[string]*PmemDeviceInfo
}

var _ PmemDeviceManager = &pmemLvm{}
var lvsArgs = []string{"--noheadings", "--nosuffix", "-o", "lv_name,lv_path,lv_size", "--units", "B"}
var vgsArgs = []string{"--noheadings", "--nosuffix", "-o", "vg_name,vg_size,vg_free,vg_tags", "--units", "B"}

// mutex to synchronize all LVM calls
// The reason we chose not to support concurrent LVM calls was
// due to LVM's inconsistent behavior when made concurrent calls
// in our stress tests. One should revisit this and choose better
// suitable synchronization policy.
var lvmMutex = &sync.Mutex{}

// NewPmemDeviceManagerLVM Instantiates a new LVM based pmem device manager
// The pre-requisite for this manager is that all the pmem regions which should be managed by
// this LMV manager are devided into namespaces and grouped as volume groups.
func NewPmemDeviceManagerLVM() (PmemDeviceManager, error) {
	lvmMutex.Lock()
	defer lvmMutex.Unlock()

	ctx, err := ndctl.NewContext()
	if err != nil {
		return nil, err
	}
	volumeGroups := []string{}
	for _, bus := range ctx.GetBuses() {
		for _, r := range bus.ActiveRegions() {
			nsmodes := []ndctl.NamespaceMode{ndctl.FsdaxMode, ndctl.SectorMode}
			for _, nsmod := range nsmodes {
				vgname := vgName(bus, r, nsmod)
				if _, err := pmemexec.RunCommand("vgs", vgname); err != nil {
					klog.V(5).Infof("NewPmemDeviceManagerLVM: VG %v non-existent, skip", vgname)
				} else {
					volumeGroups = append(volumeGroups, vgname)
				}
			}
		}
	}
	ctx.Free()

	return NewPmemDeviceManagerLVMForVGs(volumeGroups)
}

func NewPmemDeviceManagerLVMForVGs(volumeGroups []string) (PmemDeviceManager, error) {
	devices, err := listDevices(volumeGroups...)
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
	tag  string
}

func (lvm *pmemLvm) GetCapacity() (map[string]uint64, error) {
	lvmMutex.Lock()
	defer lvmMutex.Unlock()
	return lvm.getCapacity()
}

// nsmode is expected to be either "fsdax" or "sector"
func (lvm *pmemLvm) CreateDevice(volumeId string, size uint64, nsmode string) error {
	if nsmode != string(ndctl.FsdaxMode) && nsmode != string(ndctl.SectorMode) {
		return fmt.Errorf("unknown namespace mode %q: %w", nsmode, ErrInvalid)
	}
	lvmMutex.Lock()
	defer lvmMutex.Unlock()
	// Check that such volume does not exist. In certain error states, for example when
	// namespace creation works but device zeroing fails (missing /dev/pmemX.Y in container),
	// this function is asked to create new devices repeatedly, forcing running out of space.
	// Avoid device filling with garbage entries by returning error.
	// Overall, no point having more than one namespace with same volumeId.
	if _, err := lvm.getDevice(volumeId); err == nil {
		return ErrDeviceExists
	}
	vgs, err := getVolumeGroups(lvm.volumeGroups, nsmode)
	if err != nil {
		return err
	}
	// Adjust up to next alignment boundary, if not aligned already.
	// This logic relies on size guaranteed to be nonzero,
	// which is now achieved by check in upper layer.
	// If zero size possible then we would need to check and increment by lvmAlign,
	// because LVM does not tolerate creation of zero size.
	if reminder := size % lvmAlign; reminder != 0 {
		klog.V(5).Infof("CreateDevice align size up by %v: from %v", lvmAlign-reminder, size)
		size += lvmAlign - reminder
		klog.V(5).Infof("CreateDevice align size up: to %v", size)
	}
	strSz := strconv.FormatUint(size, 10) + "B"

	for _, vg := range vgs {
		// use first Vgroup with enough available space
		if vg.free >= size {
			// In some container environments clearing device fails with race condition.
			// So, we ask lvm not to clear(-Zn) the newly created device, instead we do ourself in later stage.
			// lvcreate takes size in MBytes if no unit
			if _, err := pmemexec.RunCommand("lvcreate", "-Zn", "-L", strSz, "-n", volumeId, vg.name); err != nil {
				klog.V(3).Infof("lvcreate failed with error: %v, trying for next free region", err)
			} else {
				// clear start of device to avoid old data being recognized as file system
				device, err := getUncachedDevice(volumeId, vg.name)
				if err != nil {
					return err
				}
				if err := waitDeviceAppears(device); err != nil {
					return err
				}
				if err := clearDevice(device, false); err != nil {
					return fmt.Errorf("clear device %q: %v", volumeId, err)
				}

				lvm.devices[device.VolumeId] = device

				return nil
			}
		}
	}
	return ErrNotEnoughSpace
}

func (lvm *pmemLvm) DeleteDevice(volumeId string, flush bool) error {
	lvmMutex.Lock()
	defer lvmMutex.Unlock()

	var err error
	var device *PmemDeviceInfo

	if device, err = lvm.getDevice(volumeId); err != nil {
		if errors.Is(err, ErrDeviceNotFound) {
			return nil
		}
		return err
	}
	if err := clearDevice(device, flush); err != nil {
		if errors.Is(err, ErrDeviceNotFound) {
			// Remove device from cache
			delete(lvm.devices, volumeId)
			return nil
		}
		return err
	}

	if _, err := pmemexec.RunCommand("lvremove", "-fy", device.Path); err != nil {
		return err
	}

	// Remove device from cache
	delete(lvm.devices, volumeId)

	return nil
}

func (lvm *pmemLvm) ListDevices() ([]*PmemDeviceInfo, error) {
	lvmMutex.Lock()
	defer lvmMutex.Unlock()

	devices := []*PmemDeviceInfo{}
	for _, dev := range lvm.devices {
		devices = append(devices, dev)
	}

	return devices, nil
}

func (lvm *pmemLvm) GetDevice(volumeId string) (*PmemDeviceInfo, error) {
	lvmMutex.Lock()
	defer lvmMutex.Unlock()

	return lvm.getDevice(volumeId)
}

func (lvm *pmemLvm) getDevice(volumeId string) (*PmemDeviceInfo, error) {
	if dev, ok := lvm.devices[volumeId]; ok {
		return dev, nil
	}

	return nil, ErrDeviceNotFound
}

func getUncachedDevice(volumeId string, volumeGroup string) (*PmemDeviceInfo, error) {
	devices, err := listDevices(volumeGroup)
	if err != nil {
		return nil, err
	}

	if dev, ok := devices[volumeId]; ok {
		return dev, nil
	}

	return nil, ErrDeviceNotFound
}

// listDevices Lists available logical devices in given volume groups
func listDevices(volumeGroups ...string) (map[string]*PmemDeviceInfo, error) {
	args := append(lvsArgs, volumeGroups...)
	output, err := pmemexec.RunCommand("lvs", args...)
	if err != nil {
		return nil, fmt.Errorf("lvs failure : %v", err)
	}
	return parseLVSOutput(output)
}

func vgName(bus *ndctl.Bus, region *ndctl.Region, nsmode ndctl.NamespaceMode) string {
	return bus.DeviceName() + region.DeviceName() + string(nsmode)
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

func (lvm *pmemLvm) getCapacity() (map[string]uint64, error) {
	capacity := map[string]uint64{}
	nsmodes := []ndctl.NamespaceMode{ndctl.FsdaxMode, ndctl.SectorMode}
	for _, nsmod := range nsmodes {
		vgs, err := getVolumeGroups(lvm.volumeGroups, string(nsmod))
		if err != nil {
			return nil, err
		}

		for _, vg := range vgs {
			if vg.free > capacity[string(nsmod)] {
				capacity[string(nsmod)] = vg.free
			}
		}
	}

	return capacity, nil
}

func getVolumeGroups(groups []string, wantedTag string) ([]vgInfo, error) {
	vgs := []vgInfo{}
	args := append(vgsArgs, groups...)
	output, err := pmemexec.RunCommand("vgs", args...)
	if err != nil {
		return vgs, fmt.Errorf("vgs failure: %v", err)
	}
	for _, line := range strings.SplitN(output, "\n", len(groups)) {
		fields := strings.Fields(strings.TrimSpace(line))
		if len(fields) != 4 {
			return vgs, fmt.Errorf("failed to parse vgs output: %q", line)
		}
		tag := fields[3]
		if tag == wantedTag {
			vg := vgInfo{}
			vg.name = fields[0]
			vg.size, _ = strconv.ParseUint(fields[1], 10, 64)
			vg.free, _ = strconv.ParseUint(fields[2], 10, 64)
			vg.tag = tag
			vgs = append(vgs, vg)
		}
	}

	return vgs, nil
}
