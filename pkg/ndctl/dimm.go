package ndctl

//#cgo pkg-config: libndctl
//#define ARRAY_SIZE(a) (sizeof(a) / sizeof((a)[0]))
//#include <ndctl/libndctl.h>
//#include <ndctl/ndctl.h>
import "C"

// Dimm is a go wrapper for ndctl_dimm.
type Dimm interface {
	// Enabled returns if the dimm is enabled.
	Enabled() bool
	// Active returns if the the device is active.
	Active() bool
	// ID returns the dimm's unique identifier string.
	ID() string
	// PhysicalID returns the dimm's physical id number.
	PhysicalID() int
	// DeviceName returns the dimm's device name.
	DeviceName() string
	// Handle returns the dimm's handle.
	Handle() int16
}

type dimm = C.struct_ndctl_dimm

var _ Dimm = &dimm{}

func (d *dimm) Enabled() bool {
	return C.ndctl_dimm_is_enabled(d) == 1
}

func (d *dimm) Active() bool {
	return C.ndctl_dimm_is_active(d) == 1
}

func (d *dimm) ID() string {
	return C.GoString(C.ndctl_dimm_get_unique_id(d))
}

func (d *dimm) PhysicalID() int {
	return int(C.ndctl_dimm_get_phys_id(d))
}

func (d *dimm) DeviceName() string {
	return C.GoString(C.ndctl_dimm_get_devname(d))
}

func (d *dimm) Handle() int16 {
	return int16(C.ndctl_dimm_get_handle(d))
}

// Strings formats all relevant attributes as JSON.
func (d *dimm) String() string {
	return marshal(map[string]interface{}{
		"id":      d.ID(),
		"dev":     d.DeviceName(),
		"handle":  d.Handle(),
		"phys_id": d.PhysicalID(),
		"enabled": d.Enabled(),
	})
}
