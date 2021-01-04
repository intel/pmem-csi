package pmdmanager

import (
	"fmt"

	api "github.com/intel/pmem-csi/pkg/apis/pmemcsi/v1beta1"
)

const (
	FakeDevicePathPrefix = "/dev/pmem-csi-fake"
)

// PmemDeviceInfo represents a volume created by PMEM-CSI.
type PmemDeviceInfo struct {
	// VolumeId is a unique identifier created by PMEM-CSI for the volume.
	// It is returned by CreateDevice and passed into NodeStageVolume
	// and NodePublishVolume.
	VolumeId string

	// Path is the actual device path (for example, /dev/pmem0.1).
	// As a special case, if the path starts with FakeDevicePathPrefix,
	// then the volume doesn't have a backing store.
	Path string

	// Size allocated for block device in bytes.
	Size uint64
}

// Capacity contains information about PMEM. All sizes count bytes.
type Capacity struct {
	// MaxVolumeSize is the size of the largest volume that
	// currently can be created, considering alignment and
	// fragmentation.
	MaxVolumeSize uint64
	// Available is the sum of all PMEM that could be used for
	// volumes.
	Available uint64
	// Managed is all PMEM that is managed by the driver.
	Managed uint64
	// Total is all PMEM found by the driver.
	Total uint64
}

//PmemDeviceManager interface to manage the PMEM block devices
type PmemDeviceManager interface {
	// GetName returns current device manager's operation mode
	GetMode() api.DeviceMode

	// GetCapacity returns information about local capacity.
	GetCapacity() (Capacity, error)

	// CreateDevice creates a new block device with give name, size and namespace mode
	// Possible errors: ErrNotEnoughSpace, ErrDeviceExists
	CreateDevice(name string, size uint64) error

	// GetDevice returns the block device information for given name
	// Possible errors: ErrDeviceNotFound
	GetDevice(name string) (*PmemDeviceInfo, error)

	// DeleteDevice deletes an existing block device with give name.
	// If 'flush' is 'true', then the device data is zeroed before deleting the device
	// Possible errors: ErrDeviceInUse
	DeleteDevice(name string, flush bool) error

	// ListDevices returns all the block devices information that was created by this device manager
	ListDevices() ([]*PmemDeviceInfo, error)
}

// New creates a new device manager for the given mode and percentage.
func New(mode api.DeviceMode, pmemPercentage uint) (PmemDeviceManager, error) {
	switch mode {
	case api.DeviceModeFake:
		return newFake(pmemPercentage)
	case api.DeviceModeLVM:
		return newPmemDeviceManagerLVM(pmemPercentage)
	case api.DeviceModeDirect:
		return newPmemDeviceManagerNdctl(pmemPercentage)
	default:
		return nil, fmt.Errorf("unsupported device mode %q", mode)
	}
}
