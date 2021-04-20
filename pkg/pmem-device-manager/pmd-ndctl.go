package pmdmanager

import (
	"errors"
	"fmt"
	"os"
	"sync"

	api "github.com/intel/pmem-csi/pkg/apis/pmemcsi/v1beta1"
	pmemerr "github.com/intel/pmem-csi/pkg/errors"
	"github.com/intel/pmem-csi/pkg/ndctl"
	"k8s.io/klog/v2"
	"k8s.io/utils/mount"
)

const (
	// 1 GB align in ndctl creation request has proven to be reliable.
	// Newer kernels may allow smaller alignment but we do not want to introduce kernel dependency.
	ndctlAlign uint64 = 1024 * 1024 * 1024
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
func newPmemDeviceManagerNdctl(pmemPercentage uint) (PmemDeviceManager, error) {
	if pmemPercentage > 100 {
		return nil, fmt.Errorf("invalid pmemPercentage '%d'. Value must be 0..100", pmemPercentage)
	}

	writable, err := sysIsWritable()
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
			writable, err = sysIsWritable()
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
func sysIsWritable() (bool, error) {
	mounts, err := mount.New("").List()
	if err != nil {
		return false, fmt.Errorf("list mounts: %v", err)
	}
	for _, mnt := range mounts {
		if mnt.Device == "sysfs" && mnt.Path == "/sys" {
			klog.V(5).Infof("/sys mount options:%s", mnt.Opts)
			for _, opt := range mnt.Opts {
				if opt == "rw" {
					klog.V(4).Info("/sys mounted read-write, good")
					return true, nil
				} else if opt == "ro" {
					klog.V(4).Info("/sys mounted read-only, bad")
				}
			}
			klog.V(4).Info("/sys mounted with unknown options, bad")
		}
	}
	return false, nil
}

func (pmem *pmemNdctl) GetMode() api.DeviceMode {
	return api.DeviceModeDirect
}

func (pmem *pmemNdctl) GetCapacity() (capacity Capacity, err error) {
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

			realalign := ndctlAlign * r.InterleaveWays()
			available := r.MaxAvailableExtent()
			// align down, avoid claiming more than what we really can serve
			klog.V(4).Infof("GetCapacity: available before realalign: %d", available)
			available /= realalign
			available *= realalign
			klog.V(4).Infof("GetCapacity: available after realalign: %d", available)
			if available > capacity.MaxVolumeSize {
				capacity.MaxVolumeSize = available
			}
			capacity.Available += available
			capacity.Managed += r.Size()
		}
	}
	// TODO: we should maintain capacity when adding or subtracting
	// from upper layer, not done right now!!
	return capacity, nil
}

func (pmem *pmemNdctl) CreateDevice(volumeId string, size uint64) error {
	ndctlMutex.Lock()
	defer ndctlMutex.Unlock()

	ndctx, err := ndctl.NewContext()
	if err != nil {
		return err
	}
	defer ndctx.Free()

	// Check that such volume does not exist. In certain error states, for example when
	// namespace creation works but device zeroing fails (missing /dev/pmemX.Y in container),
	// this function is asked to create new devices repeatedly, forcing running out of space.
	// Avoid device filling with garbage entries by returning error.
	// Overall, no point having more than one namespace with same name.
	if _, err := getDevice(ndctx, volumeId); err == nil {
		return pmemerr.DeviceExists
	}

	// libndctl needs to store meta data and will use some of the allocated
	// space for that (https://github.com/pmem/ndctl/issues/79).
	// We don't know exactly how much space that is, just
	// that it should be a small amount. But because libndctl
	// rounds up to the alignment, in practice that means we need
	// to request `align` additional bytes.
	size += ndctlAlign
	klog.V(4).Infof("Compensate for libndctl creating one alignment step smaller: increase size to %d", size)
	ns, err := ndctl.CreateNamespace(ndctx, ndctl.CreateNamespaceOpts{
		Name:  volumeId,
		Size:  size,
		Align: ndctlAlign,
		Mode:  "fsdax",
	})
	if err != nil {
		return err
	}
	klog.V(3).Infof("Namespace created: %+v", ns)
	// clear start of device to avoid old data being recognized as file system
	device, err := getDevice(ndctx, volumeId)
	if err != nil {
		return err
	}
	if err := clearDevice(device, false); err != nil {
		return fmt.Errorf("clear device %q: %v", volumeId, err)
	}

	return nil
}

func (pmem *pmemNdctl) DeleteDevice(volumeId string, flush bool) error {
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
	if err := clearDevice(device, flush); err != nil {
		if errors.Is(err, pmemerr.DeviceNotFound) {
			return nil
		}
		return err
	}
	return ndctl.DestroyNamespaceByName(ndctx, volumeId)
}

func (pmem *pmemNdctl) GetDevice(volumeId string) (*PmemDeviceInfo, error) {
	ndctlMutex.Lock()
	defer ndctlMutex.Unlock()

	ndctx, err := ndctl.NewContext()
	if err != nil {
		return nil, err
	}
	defer ndctx.Free()

	return getDevice(ndctx, volumeId)
}

func (pmem *pmemNdctl) ListDevices() ([]*PmemDeviceInfo, error) {
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
