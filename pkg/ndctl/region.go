package ndctl

//#cgo pkg-config: libndctl
//#include <string.h>
//#include <ndctl/libndctl.h>
//#define ARRAY_SIZE(a) (sizeof(a) / sizeof((a)[0]))
//#include <ndctl/ndctl.h>
import "C"
import (
	"encoding/json"
	"fmt"

	"github.com/google/uuid"
	"k8s.io/klog/glog"
)

type RegionType string

const (
	PmemRegion    RegionType = "pmem" //C.ND_DEVICE_REGION_PMEM
	BlockRegion   RegionType = "blk"  //C.ND_DEVICE_REGION_BLK
	UnknownRegion RegionType = "unknown"
)

// Region go wrapper for ndctl_region
type Region C.struct_ndctl_region

//ID returns region id
func (r *Region) ID() uint {
	ndr := (*C.struct_ndctl_region)(r)
	return uint(C.ndctl_region_get_id(ndr))
}

//DeviceName returns region name
func (r *Region) DeviceName() string {
	ndr := (*C.struct_ndctl_region)(r)
	return C.GoString(C.ndctl_region_get_devname(ndr))
}

//Size returns total size of the region
func (r *Region) Size() uint64 {
	ndr := (*C.struct_ndctl_region)(r)
	return uint64(C.ndctl_region_get_size(ndr))
}

//AvailableSize returns size available in the region
func (r *Region) AvailableSize() uint64 {
	ndr := (*C.struct_ndctl_region)(r)
	return uint64(C.ndctl_region_get_available_size(ndr))
}

//MaxAvailableExtent returns max available extent size in the region
func (r *Region) MaxAvailableExtent() uint64 {
	ndr := (*C.struct_ndctl_region)(r)
	return uint64(C.ndctl_region_get_max_available_extent(ndr))
}

func (r *Region) Type() RegionType {
	ndr := (*C.struct_ndctl_region)(r)
	switch C.ndctl_region_get_type(ndr) {
	case C.ND_DEVICE_REGION_PMEM:
		return PmemRegion
	case C.ND_DEVICE_REGION_BLK:
		return BlockRegion
	}

	return UnknownRegion
}

//TypeName returns region type
func (r *Region) TypeName() string {
	ndr := (*C.struct_ndctl_region)(r)
	return C.GoString(C.ndctl_region_get_type_name(ndr))
}

func (r *Region) Enabled() bool {
	ndr := (*C.struct_ndctl_region)(r)
	return C.ndctl_region_is_enabled(ndr) != 0
}

func (r *Region) Readonly() bool {
	ndr := (*C.struct_ndctl_region)(r)
	return C.ndctl_region_get_ro(ndr) != 0
}

//ActiveNamespaces returns all active namespaces in the region
func (r *Region) ActiveNamespaces() []*Namespace {
	return r.namespaces(true)
}

//AllNamespaces returns all namespaces in the region
func (r *Region) AllNamespaces() []*Namespace {
	return r.namespaces(false)
}

//Bus get associated bus
func (r *Region) Bus() *Bus {
	ndr := (*C.struct_ndctl_region)(r)
	return (*Bus)(C.ndctl_region_get_bus(ndr))
}

//Mappings return available mappings in the region
func (r *Region) Mappings() []*Mapping {
	ndr := (*C.struct_ndctl_region)(r)
	var mappings []*Mapping
	for ndmap := C.ndctl_mapping_get_first(ndr); ndmap != nil; ndmap = C.ndctl_mapping_get_next(ndmap) {
		mappings = append(mappings, (*Mapping)(ndmap))
	}

	return mappings
}

func (r *Region) SeedNamespace() *Namespace {
	ndr := (*C.struct_ndctl_region)(r)
	return (*Namespace)(C.ndctl_region_get_namespace_seed(ndr))
}

func (r *Region) CreateNamespace(opts CreateNamespaceOpts) (*Namespace, error) {
	ndr := (*C.struct_ndctl_region)(r)
	defaultAlign := mib2
	var err error
	/* Set defaults */
	if opts.Type == "" {
		opts.Type = PmemNamespace
	}
	if opts.Mode == "" {
		if opts.Type == PmemNamespace {
			opts.Mode = FsdaxMode // == MemoryMode
		} else {
			opts.Mode = SectorMode
		}
	}
	if opts.Location == "" {
		opts.Location = DeviceMap
	}

	if opts.SectorSize == 0 {
		if opts.Type == BlockNamespace || opts.Mode == SectorMode {
			// default sector size for blk-type or safe-mode
			opts.SectorSize = kib4
		}
	}

	/* Sanity checks */

	regionName := r.DeviceName()
	if !r.Enabled() {
		return nil, fmt.Errorf("Region not enabled")
	}
	if r.Readonly() {
		return nil, fmt.Errorf("Cannot create namspace in readonly region")
	}

	if r.Type() == BlockRegion {
		if opts.Mode == FsdaxMode || opts.Mode == DaxMode {
			return nil, fmt.Errorf("Block regions does not support %s mode namespace", opts.Mode)
		}
	}

	if opts.Size != 0 {
		available := r.MaxAvailableExtent()
		if available == uint64(C.ULLONG_MAX) {
			available = r.AvailableSize()
		}
		if opts.Size > available {
			return nil, fmt.Errorf("Not enough space to create namespace with size %v", opts.Size)
		}
	}

	if opts.Align != 0 {
		if opts.Mode == SectorMode || opts.Mode == RawMode {
			glog.V(4).Infof("%s mode does not support setting an alignment, hence ignoring alignment", opts.Mode)
		} else {
			resource := uint64(C.ndctl_region_get_resource(ndr))
			if resource < uint64(C.ULLONG_MAX) && resource&(mib2-1) != 0 {
				glog.V(4).Infof("%s: falling back to a 4K alignment", regionName)
				opts.Align = kib4
			}
			if opts.Align != kib4 && opts.Align != mib2 && opts.Align != gib {
				return nil, fmt.Errorf("unsupported alignment: %v", opts.Align)
			}
		}
	} else {
		opts.Align = defaultAlign
	}

	if opts.Size != 0 {
		ways := uint64(C.ndctl_region_get_interleave_ways(ndr))
		align := opts.Align * ways
		if opts.Size%align != 0 {
			// force-align down to block boundary. More sensible would be to align up, but then it may fail because we ask more then there is left
			opts.Size /= align
			opts.Size *= align
			glog.Warningf("%s: namespace size must align to interleave-width:%d * alignment:%d, force-align to %d",
				regionName, ways, opts.Align, opts.Size)
		}
	}

	/* setup_namespace */

	ns := r.SeedNamespace()
	if ns == nil {
		return nil, fmt.Errorf("Failed to get seed namespace in region %s", r.DeviceName())
	}
	if ns.Active() {
		return nil, fmt.Errorf("Seed namespace is active in region %s", r.DeviceName())
	}
	ndns := (*C.struct_ndctl_namespace)(ns)

	if ns.Type() != IoNamespace {
		uid, _ := uuid.NewUUID()
		err = ns.SetUUID(uid)
		if err == nil {
			err = ns.SetSize(opts.Size)
		}
		if err == nil && opts.Name != "" {
			err = ns.SetAltName(opts.Name)
		}
	}

	if err == nil {
		glog.V(5).Infof("setting namespace sector size: %v", opts.SectorSize)
		err = ns.SetSectorSize(opts.SectorSize)
	}
	if err == nil {
		err = ns.SetEnforceMode(opts.Mode)
	}

	if err == nil {
		switch opts.Mode {
		case FsdaxMode:
			glog.V(5).Infof("setting pfn")
			err = ns.setPfnSeed(opts.Location, opts.Align)
		case DaxMode:
			glog.V(5).Infof("setting dax")
			err = ns.setDaxSeed(opts.Location, opts.Align)
		case SectorMode:
			glog.V(5).Infof("setting btt")
			err = ns.setBttSeed(opts.SectorSize)
		}
	}
	if err == nil {
		glog.V(5).Infof("enabling namespace")
		err = ns.Enable()
	}

	if err != nil {
		// reset seed on failure
		ns.SetEnforceMode(RawMode)
		C.ndctl_namespace_delete(ndns)
		return nil, err
	}

	return ns, nil
}

//DestroyNamespace destroys the given namespace ns in the region
func (r *Region) DestroyNamespace(ns *Namespace, force bool) error {
	var rc C.int
	devname := ns.DeviceName()
	ndr := (*C.struct_ndctl_region)(r)
	ndns := (*C.struct_ndctl_namespace)(ns)
	if ndns == nil {
		return fmt.Errorf("null namespace")
	}

	if rc = C.ndctl_region_get_ro(ndr); rc < 0 {
		return fmt.Errorf("namespace %s is in readonly region", devname)
	}

	if ns.Active() && !force {
		return fmt.Errorf("namespace is active, use force deletion")
	}

	if rc = C.ndctl_namespace_disable_safe(ndns); rc < 0 {
		return fmt.Errorf("failed to disable namespace: %s", cErrorString(rc))
	}

	if err := ns.SetEnforceMode(RawMode); err != nil {
		return nil
	}

	/* originally here we try to clear 4k at start of block device,
	* but that seems not work reliably so we use different method via flushDevice
	* This here remains commented out
	if err := ns.nullify(); err != nil {
		return fmt.Errorf("failed to nullify namespace: %s", err.Error())
	}*/

	C.ndctl_namespace_disable_invalidate(ndns)

	if rc = C.ndctl_namespace_delete(ndns); rc < 0 {
		return fmt.Errorf("failed to reclaim namespace: %s", cErrorString(rc))
	}

	return nil
}

//MarshalJSON returns json encoding of the region
func (r *Region) MarshalJSON() ([]byte, error) {
	return json.Marshal(map[string]interface{}{
		"type":                 r.Type(),
		"dev":                  r.DeviceName(),
		"size":                 r.Size(),
		"available_size":       r.AvailableSize(),
		"max_available_extent": r.MaxAvailableExtent(),
		"namespaces":           r.ActiveNamespaces(),
		"mappings":             r.Mappings(),
	})
}

func (r *Region) namespaces(onlyActive bool) []*Namespace {
	var namespaces []*Namespace
	ndr := (*C.struct_ndctl_region)(r)

	for ndns := C.ndctl_namespace_get_first(ndr); ndns != nil; ndns = C.ndctl_namespace_get_next(ndns) {
		if !onlyActive || C.ndctl_namespace_is_active(ndns) == true {
			namespaces = append(namespaces, (*Namespace)(ndns))
		}
	}

	return namespaces
}
