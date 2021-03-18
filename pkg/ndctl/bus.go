package ndctl

//#cgo pkg-config: libndctl
//#define ARRAY_SIZE(a) (sizeof(a) / sizeof((a)[0]))
//#include <ndctl/libndctl.h>
//#include <ndctl/ndctl.h>
import "C"

// Bus is a go wrapper for ndctl_bus.
type Bus interface {
	// Provider returns the bus provider.
	Provider() string
	// DeviceName returns the bus device name.
	DeviceName() string
	// Dimms returns the dimms provided by the bus.
	Dimms() []Dimm
	// ActiveRegions returns all active regions in the bus.
	ActiveRegions() []Region
	// AllRegions returns all regions in the bus including disabled regions.
	AllRegions() []Region
	// GetRegionByPhysicalAddress finds a region by physical address.
	GetRegionByPhysicalAddress(address uint64) Region
}

type bus = C.struct_ndctl_bus

var _ Bus = &bus{}

func (b *bus) Provider() string {
	return C.GoString(C.ndctl_bus_get_provider(b))
}

func (b *bus) DeviceName() string {
	return C.GoString(C.ndctl_bus_get_devname(b))
}

func (b *bus) Dimms() []Dimm {
	var dimms []Dimm
	for nddimm := C.ndctl_dimm_get_first(b); nddimm != nil; nddimm = C.ndctl_dimm_get_next(nddimm) {
		dimms = append(dimms, nddimm)
	}
	return dimms
}

func (b *bus) ActiveRegions() []Region {
	return b.regions(true)
}

func (b *bus) AllRegions() []Region {
	return b.regions(false)
}

func (b *bus) GetRegionByPhysicalAddress(address uint64) Region {
	ndr := C.ndctl_bus_get_region_by_physical_address(b, C.ulonglong(address))
	return ndr
}

// Strings formats all relevant attributes as JSON.
func (b *bus) String() string {
	return marshal(map[string]interface{}{
		"provider": b.Provider(),
		"dev":      b.DeviceName(),
		"regions":  b.ActiveRegions(),
		"dimms":    b.Dimms(),
	})
}

func (b *bus) regions(onlyActive bool) []Region {
	var regions []Region
	for ndr := C.ndctl_region_get_first(b); ndr != nil; ndr = C.ndctl_region_get_next(ndr) {
		if !onlyActive || int(C.ndctl_region_is_enabled(ndr)) == 1 {
			regions = append(regions, ndr)
		}
	}

	return regions
}
