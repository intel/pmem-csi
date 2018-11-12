package ndctl

//#cgo pkg-config: libndctl
//#define ARRAY_SIZE(a) (sizeof(a) / sizeof((a)[0]))
//#include <ndctl/libndctl.h>
//#include <ndctl/ndctl.h>
import "C"
import "encoding/json"

//Bus go wrapper for ndctl_bus
type Bus C.struct_ndctl_bus

//Provider returns bus provider
func (b *Bus) Provider() string {
	ndbus := (*C.struct_ndctl_bus)(b)
	return C.GoString(C.ndctl_bus_get_provider(ndbus))
}

//DeviceName returns bus device name
func (b *Bus) DeviceName() string {
	ndbus := (*C.struct_ndctl_bus)(b)
	return C.GoString(C.ndctl_bus_get_devname(ndbus))
}

//Dimms returns dimms provided by the bus
func (b *Bus) Dimms() []*Dimm {
	var dimms []*Dimm
	ndbus := (*C.struct_ndctl_bus)(b)
	for nddimm := C.ndctl_dimm_get_first(ndbus); nddimm != nil; nddimm = C.ndctl_dimm_get_next(nddimm) {
		dimms = append(dimms, (*Dimm)(nddimm))
	}
	return dimms
}

//ActiveRegions returns all active regions in the bus
func (b *Bus) ActiveRegions() []*Region {
	return b.regions(true)
}

//AllRegions returns all regions in the bus including disabled regions
func (b *Bus) AllRegions() []*Region {
	return b.regions(false)
}

//GetRegionByPhysicalAddress Find region by physical address
func (b *Bus) GetRegionByPhysicalAddress(address uint64) *Region {
	ndbus := (*C.struct_ndctl_bus)(b)
	ndr := C.ndctl_bus_get_region_by_physical_address(ndbus, C.ulonglong(address))
	return (*Region)(ndr)
}

//MarshalJSON returns the encoded value of bus
func (b *Bus) MarshalJSON() ([]byte, error) {
	return json.Marshal(map[string]interface{}{
		"provider": b.Provider(),
		"dev":      b.DeviceName(),
		"regions":  b.ActiveRegions(),
		"dimms":    b.Dimms(),
	})
}

func (b *Bus) regions(onlyActive bool) []*Region {
	var regions []*Region
	ndbus := (*C.struct_ndctl_bus)(b)
	for ndr := C.ndctl_region_get_first(ndbus); ndr != nil; ndr = C.ndctl_region_get_next(ndr) {
		if !onlyActive || int(C.ndctl_region_is_enabled(ndr)) == 1 {
			regions = append(regions, (*Region)(ndr))
		}
	}

	return regions
}
