package ndctl

//#cgo pkg-config: libndctl
//#define ARRAY_SIZE(a) (sizeof(a) / sizeof((a)[0]))
//#include <ndctl/libndctl.h>
//#include <ndctl/ndctl.h>
import "C"
import "encoding/json"

// Dimm go wrapper for ndctl_dimm
type Dimm C.struct_ndctl_dimm

//Enabled returns if the dimm is enabled
func (d *Dimm) Enabled() bool {
	ndd := (*C.struct_ndctl_dimm)(d)
	return C.ndctl_dimm_is_enabled(ndd) == 1
}

//Active returns if the the device is active
func (d *Dimm) Active() bool {
	ndd := (*C.struct_ndctl_dimm)(d)
	return C.ndctl_dimm_is_active(ndd) == 1
}

//ID returns unique dimm id
func (d *Dimm) ID() string {
	ndd := (*C.struct_ndctl_dimm)(d)
	return C.GoString(C.ndctl_dimm_get_unique_id(ndd))
}

//PhysicalID returns dimm physical id
func (d *Dimm) PhysicalID() int {
	ndd := (*C.struct_ndctl_dimm)(d)
	return int(C.ndctl_dimm_get_phys_id(ndd))
}

//DeviceName returns dimm device name
func (d *Dimm) DeviceName() string {
	ndd := (*C.struct_ndctl_dimm)(d)
	return C.GoString(C.ndctl_dimm_get_devname(ndd))
}

//Handle returns dimm handle
func (d *Dimm) Handle() int16 {
	ndd := (*C.struct_ndctl_dimm)(d)
	return int16(C.ndctl_dimm_get_handle(ndd))
}

//MarshalJSON returns the encoding of dimm
func (d *Dimm) MarshalJSON() ([]byte, error) {
	return json.Marshal(map[string]interface{}{
		"id":      d.ID(),
		"dev":     d.DeviceName(),
		"handle":  d.Handle(),
		"phys_id": d.PhysicalID(),
		"enabled": d.Enabled(),
	})
}
