package ndctl

//#cgo pkg-config: libndctl
//#include <ndctl/libndctl.h>
import "C"
import "encoding/json"

// Mapping go wrapper for ndctl_mapping
type Mapping C.struct_ndctl_mapping

//Offset returns offset within the region
func (m *Mapping) Offset() uint64 {
	ndm := (*C.struct_ndctl_mapping)(m)
	return uint64(C.ndctl_mapping_get_offset(ndm))
}

//Length returns mapping length
func (m *Mapping) Length() uint64 {
	ndm := (*C.struct_ndctl_mapping)(m)
	return uint64(C.ndctl_mapping_get_length(ndm))
}

//Position returns mapping position
func (m *Mapping) Position() int {
	ndm := (*C.struct_ndctl_mapping)(m)
	return int(C.ndctl_mapping_get_position(ndm))
}

//Region get associated Region
func (m *Mapping) Region() *Region {
	ndm := (*C.struct_ndctl_mapping)(m)
	return (*Region)(C.ndctl_mapping_get_region(ndm))
}

//Dimm get associated Dimm
func (m *Mapping) Dimm() *Dimm {
	ndm := (*C.struct_ndctl_mapping)(m)
	return (*Dimm)(C.ndctl_mapping_get_dimm(ndm))
}

//MarshalJSON returns json encoding of the mapping
func (m *Mapping) MarshalJSON() ([]byte, error) {
	return json.Marshal(map[string]interface{}{
		"dimm":     m.Dimm().DeviceName(),
		"offset":   m.Offset(),
		"length":   m.Length(),
		"position": m.Position(),
	})
}
