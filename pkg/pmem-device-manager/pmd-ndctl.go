package pmdmanager

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sync"

	"k8s.io/klog/v2"

	api "github.com/intel/pmem-csi/pkg/apis/pmemcsi/v1beta1"
	pmemerr "github.com/intel/pmem-csi/pkg/errors"
	pmemlog "github.com/intel/pmem-csi/pkg/logger"
	"github.com/intel/pmem-csi/pkg/ndctl"
	"github.com/intel/pmem-csi/pkg/pmem-csi-driver/parameters"

	"k8s.io/utils/mount"
)

type pmemNdctl struct {
	pmemPercentage uint
}

var _ PmemDeviceManager = &pmemNdctl{}

// mutex to synchronize all ndctl calls
// https://github.com/pmem/ndctl/issues/96
// Once ndctl supports concurrent calls we need to revisit
// our locking strategy.
var ndctlMutex = &sync.Mutex{}

//NewPmemDeviceManagerNdctl Instantiates a new ndctl based pmem device manager
// FIXME(avalluri): consider pmemPercentage while calculating available space
func newPmemDeviceManagerNdctl(ctx context.Context, pmemPercentage uint) (PmemDeviceManager, error) {
	ctx, _ = pmemlog.WithName(ctx, "ndctl-New")
	if pmemPercentage > 100 {
		return nil, fmt.Errorf("invalid pmemPercentage '%d'. Value must be 0..100", pmemPercentage)
	}

	writable, err := sysIsWritable(ctx)
	if err != nil {
		return nil, fmt.Errorf("check for writable /sys: %v", err)
	}

	if !writable {
		// If /host-sys exists, then bind mount it to /sys.  This is a
		// workaround for /sys being read-only on OpenShift 4.5
		// (https://github.com/intel/pmem-csi/issues/786 =
		// https://github.com/containerd/containerd/issues/3221).
		_, err := os.Stat("/host-sys")
		switch {
		case err == nil:
			if err := mount.New("").Mount("/host-sys", "/sys", "", []string{"rw", "bind", "seclabel", "nosuid", "nodev", "noexec", "relatime"}); err != nil {
				return nil, fmt.Errorf("bind-mount /host-sys onto /sys: %v", err)
			}

			// Check again, just to be sure.
			writable, err = sysIsWritable(ctx)
			if err != nil {
				return nil, fmt.Errorf("check for writable /sys: %v", err)
			}
			if !writable {
				return nil, errors.New("bind-mounting /host-sys to /sys did not result in writable /sys")
			}
		case os.IsNotExist(err):
			// Can't fix the write-only /sys.
			return nil, errors.New("/sys mounted read-only, can not operate")
		default:
			return nil, fmt.Errorf("/sys mounted read-only and access to /host-sys fallback failed: %v", err)
		}
	}

	return &pmemNdctl{pmemPercentage: pmemPercentage}, nil
}

// sysIsWritable returns true if any of the /sys mounts is writable.
func sysIsWritable(ctx context.Context) (bool, error) {
	logger := klog.FromContext(ctx).WithName("sysIsWritable")
	mounts, err := mount.New("").List()
	if err != nil {
		return false, fmt.Errorf("list mounts: %v", err)
	}
	for _, mnt := range mounts {
		if mnt.Device == "sysfs" && mnt.Path == "/sys" {
			logger.V(5).Info("Checking /sys", "mount-options", mnt.Opts)
			for _, opt := range mnt.Opts {
				if opt == "rw" {
					logger.V(4).Info("/sys mounted read-write, good")
					return true, nil
				} else if opt == "ro" {
					logger.V(4).Info("/sys mounted read-only, bad")
				}
			}
			logger.V(4).Info("/sys mounted with unknown options, bad")
		}
	}
	return false, nil
}

func (pmem *pmemNdctl) GetMode() api.DeviceMode {
	return api.DeviceModeDirect
}

func (pmem *pmemNdctl) GetCapacity(ctx context.Context) (capacity Capacity, err error) {
	ctx, logger := pmemlog.WithName(ctx, "ndctl-GetCapacity")
	ndctlMutex.Lock()
	defer ndctlMutex.Unlock()

	var ndctx ndctl.Context
	ndctx, err = ndctl.NewContext()
	if err != nil {
		return
	}
	defer ndctx.Free()

	for _, bus := range ndctx.GetBuses() {
		for _, r := range bus.AllRegions() {
			capacity.Total += r.Size()
			// TODO: check type?!
			if !r.Enabled() {
				continue
			}

			align, alignInfo := ndctl.CalculateAlignment(r)
			maxVolumeSize := r.MaxAvailableExtent()
			available := r.AvailableSize()
			size := r.Size()
			logger.V(4).WithValues("region", r.DeviceName()).WithValues(alignInfo...).Info("Found a region",
				"max-available-extent", pmemlog.CapacityRef(int64(maxVolumeSize)),
				"available", pmemlog.CapacityRef(int64(available)),
				"size", pmemlog.CapacityRef(int64(size)),
			)

			// align down by the region's alignment, avoid claiming having more than what we really can serve
			maxVolumeSize = maxVolumeSize / align * align
			if maxVolumeSize > capacity.MaxVolumeSize {
				capacity.MaxVolumeSize = maxVolumeSize
			}
			capacity.Available += available / align * align
			capacity.Managed += size
		}
	}
	// TODO: we should maintain capacity when adding or subtracting
	// from upper layer, not done right now!!
	return capacity, nil
}

func (pmem *pmemNdctl) CreateDevice(ctx context.Context, volumeId string, size uint64, usage parameters.Usage) (uint64, error) {
	ctx, _ = pmemlog.WithName(ctx, "ndctl-CreateDevice")
	ndctlMutex.Lock()
	defer ndctlMutex.Unlock()

	ndctx, err := ndctl.NewContext()
	if err != nil {
		return 0, err
	}
	defer ndctx.Free()

	// Check that such volume does not exist. In certain error states, for example when
	// namespace creation works but device zeroing fails (missing /dev/pmemX.Y in container),
	// this function is asked to create new devices repeatedly, forcing running out of space.
	// Avoid device filling with garbage entries by returning error.
	// Overall, no point having more than one namespace with same name.
	if _, err := getDevice(ndctx, volumeId); err == nil {
		return 0, pmemerr.DeviceExists
	}

	opts := ndctl.CreateNamespaceOpts{
		Name: volumeId,
		Size: size,
	}
	switch usage {
	case parameters.UsageAppDirect:
		opts.Mode = ndctl.FsdaxMode
	case parameters.UsageFileIO:
		opts.Mode = ndctl.SectorMode
	default:
		return 0, fmt.Errorf("unsupported usage %s for direct mode", usage)
	}

	ns, err := ndctl.CreateNamespace(ctx, ndctx, opts)
	if err != nil {
		return 0, err
	}
	actual := ns.RawSize()

	// clear start of device to avoid old data being recognized as file system
	device, err := getDevice(ndctx, volumeId)
	if err != nil {
		return 0, err
	}
	if err := clearDevice(ctx, device, false); err != nil {
		return 0, fmt.Errorf("clear device %q: %v", volumeId, err)
	}

	return actual, nil
}

func (pmem *pmemNdctl) DeleteDevice(ctx context.Context, volumeId string, flush bool) error {
	ctx, _ = pmemlog.WithName(ctx, "ndctl-DeleteDevice")
	ndctlMutex.Lock()
	defer ndctlMutex.Unlock()

	ndctx, err := ndctl.NewContext()
	if err != nil {
		return err
	}
	defer ndctx.Free()

	device, err := getDevice(ndctx, volumeId)
	if err != nil {
		if errors.Is(err, pmemerr.DeviceNotFound) {
			return nil
		}
		return err
	}
	if err := clearDevice(ctx, device, flush); err != nil {
		if errors.Is(err, pmemerr.DeviceNotFound) {
			return nil
		}
		return err
	}
	return ndctl.DestroyNamespaceByName(ndctx, volumeId)
}

func (pmem *pmemNdctl) GetDevice(ctx context.Context, volumeId string) (*PmemDeviceInfo, error) {
	ndctlMutex.Lock()
	defer ndctlMutex.Unlock()

	ndctx, err := ndctl.NewContext()
	if err != nil {
		return nil, err
	}
	defer ndctx.Free()

	return getDevice(ndctx, volumeId)
}

func (pmem *pmemNdctl) ListDevices(ctx context.Context) ([]*PmemDeviceInfo, error) {
	ndctlMutex.Lock()
	defer ndctlMutex.Unlock()

	ndctx, err := ndctl.NewContext()
	if err != nil {
		return nil, err
	}
	defer ndctx.Free()

	devices := []*PmemDeviceInfo{}
	for _, ns := range ndctl.GetAllNamespaces(ndctx) {
		devices = append(devices, namespaceToPmemInfo(ns))
	}
	return devices, nil
}

func getDevice(ndctx ndctl.Context, volumeId string) (*PmemDeviceInfo, error) {
	ns, err := ndctl.GetNamespaceByName(ndctx, volumeId)
	if err != nil {
		return nil, fmt.Errorf("error getting device %q: %w", volumeId, err)
	}

	return namespaceToPmemInfo(ns), nil
}

func namespaceToPmemInfo(ns ndctl.Namespace) *PmemDeviceInfo {
	return &PmemDeviceInfo{
		VolumeId: ns.Name(),
		Path:     "/dev/" + ns.BlockDeviceName(),
		Size:     ns.Size(),
	}
}

// totalSize sums up all PMEM regions, regardless whether they are
// enabled and regardless of their mode.
func totalSize() (size uint64, err error) {
	var ndctx ndctl.Context
	ndctx, err = ndctl.NewContext()
	if err != nil {
		return
	}
	defer ndctx.Free()

	for _, bus := range ndctx.GetBuses() {
		for _, region := range bus.AllRegions() {
			size += region.Size()
		}
	}
	return
}
