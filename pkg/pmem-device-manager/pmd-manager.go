package pmdmanager

import (
	"errors"
	"os"

	api "github.com/intel/pmem-csi/pkg/apis/pmemcsi/v1alpha1"
)

var (
	// ErrInvalid invalid argument passed
	ErrInvalid = os.ErrInvalid

	// ErrPermission no permission to complete the task
	ErrPermission = os.ErrPermission

	// ErrDeviceExists device with given id already exists
	ErrDeviceExists = errors.New("device exists")

	// ErrDeviceNotFound device does not exists
	ErrDeviceNotFound = errors.New("device not found")

	// ErrDeviceInUse device is in use
	ErrDeviceInUse = errors.New("device in use")

	// ErrDeviceNotReady device not ready yet
	ErrDeviceNotReady = errors.New("device not ready")

	// ErrNotEnoughSpace no space to create the device
	ErrNotEnoughSpace = errors.New("not enough space")
)

//PmemDeviceInfo represents a block device
type PmemDeviceInfo struct {
	//VolumeId is name of the block device
	VolumeId string
	//Path actual device path
	Path string
	//Size size allocated for block device
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
	// Possible errors: ErrNotEnoughSpace, ErrInvalid, ErrDeviceExists
	CreateDevice(name string, size uint64) error

	// GetDevice returns the block device information for given name
	// Possible errors: ErrDeviceNotFound
	GetDevice(name string) (*PmemDeviceInfo, error)

	// DeleteDevice deletes an existing block device with give name.
	// If 'flush' is 'true', then the device data is zeroed before deleting the device
	// Possible errors: ErrDeviceInUse, ErrPermission
	DeleteDevice(name string, flush bool) error

	// ListDevices returns all the block devices information that was created by this device manager
	ListDevices() ([]*PmemDeviceInfo, error)
}
