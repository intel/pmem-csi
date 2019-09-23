package pmdmanager

//PmemDeviceInfo represents a block device
type PmemDeviceInfo struct {
	//VolumeId is name of the block device
	VolumeId string
	//Path actual device path
	Path string
	//Size size allocated for block device
	Size uint64
}

//PmemDeviceManager interface to manage the PMEM block devices
type PmemDeviceManager interface {
	//GetCapacity returns the available maximum capacity that can be assigned to a Device/Volume
	GetCapacity() (map[string]uint64, error)

	//CreateDevice creates a new block device with give name, size and namespace mode
	CreateDevice(name string, size uint64, nsmode string) error

	//GetDevice returns the block device information for given name
	GetDevice(name string) (*PmemDeviceInfo, error)

	//DeleteDevice deletes an existing block device with give name.
	// If 'flush' is 'true', then the device data is zerod beofore deleting the device
	DeleteDevice(name string, flush bool) error

	//ListDevices returns all the block devices information that was created by this device manager
	ListDevices() ([]*PmemDeviceInfo, error)
}
