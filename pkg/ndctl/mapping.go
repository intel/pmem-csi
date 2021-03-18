package ndctl

//#cgo pkg-config: libndctl
//#include <ndctl/libndctl.h>
import "C"

// Mapping is a go wrapper for ndctl_mapping.
type Mapping interface {
	// Offset returns the offset within the region.
	Offset() uint64
	// Length returns the mapping's length.
	Length() uint64
	// Position returns the mapping's position.
	Position() int
	// Region gets the associated region.
	Region() Region
	// Dimm gets the associated dimm.
	Dimm() Dimm
}

type mapping = C.struct_ndctl_mapping

var _ Mapping = &mapping{}

func (m *mapping) Offset() uint64 {
	return uint64(C.ndctl_mapping_get_offset(m))
}

func (m *mapping) Length() uint64 {
	return uint64(C.ndctl_mapping_get_length(m))
}

func (m *mapping) Position() int {
	return int(C.ndctl_mapping_get_position(m))
}

func (m *mapping) Region() Region {
	return C.ndctl_mapping_get_region(m)
}

func (m *mapping) Dimm() Dimm {
	return C.ndctl_mapping_get_dimm(m)
}

// Strings formats all relevant attributes as JSON.
func (m *mapping) String() string {
	return marshal(map[string]interface{}{
		"dimm":     m.Dimm().DeviceName(),
		"offset":   m.Offset(),
		"length":   m.Length(),
		"position": m.Position(),
	})
}
