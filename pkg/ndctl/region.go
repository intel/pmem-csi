package ndctl

//#cgo pkg-config: libndctl
//#include <string.h>
//#include <ndctl/libndctl.h>
//#define ARRAY_SIZE(a) (sizeof(a) / sizeof((a)[0]))
//#include <ndctl/ndctl.h>
import "C"
import (
	"fmt"

	"github.com/google/uuid"
	"k8s.io/klog/v2"

	pmemerr "github.com/intel/pmem-csi/pkg/errors"
)

type RegionType string

const (
	PmemRegion    RegionType = "pmem" //C.ND_DEVICE_REGION_PMEM
	BlockRegion   RegionType = "blk"  //C.ND_DEVICE_REGION_BLK
	UnknownRegion RegionType = "unknown"
)

// Region go wrapper for ndctl_region
type Region interface {
	// ID returns region id.
	ID() uint
	// DeviceName returns region name.
	DeviceName() string
	// Size returns the total size of the region.
	Size() uint64
	// AvailableSize returns the size of remaining available space in the region.
	AvailableSize() uint64
	// MaxAvailableExtent returns max available extent size in the region.
	MaxAvailableExtent() uint64
	// Type identifies the kind of region.
	Type() RegionType
	// TypeName returns the name for the region type.
	TypeName() string
	// Enabled returns true if the region is enabled.
	Enabled() bool
	// Readonly returns true if the region is read/only.
	Readonly() bool
	// InterleaveWays returns the interleaving of the region.
	InterleaveWays() uint64
	// ActiveNamespaces returns all active namespaces in the region.
	ActiveNamespaces() []Namespace
	// AllNamespaces returns all non-zero sized namespaces in the region
	// as sometime a deleted namespace also lies around with size zero, we can ignore
	// such namespace.
	AllNamespaces() []Namespace
	// Bus returns the bus associated with the region.
	Bus() Bus
	// Mappings returns all available mappings in the region.
	Mappings() []Mapping
	// SeedNamespace returns the initial namespace in the region.
	SeedNamespace() Namespace
	// CreateNamespace creates a new namespace in the region.
	CreateNamespace(opts CreateNamespaceOpts) (Namespace, error)
	// DestroyNamespace destroys the given namespace in the region.
	DestroyNamespace(ns Namespace, force bool) error
	// AdaptAlign modifies the alignment for the region.
	AdaptAlign(align uint64) (uint64, error)
	// FsdaxAlignment returns the default alignment for an fsdax namespace.
	FsdaxAlignment() (uint64, error)
}

type region = C.struct_ndctl_region

var _ Region = &region{}

func (r *region) ID() uint {
	return uint(C.ndctl_region_get_id(r))
}

func (r *region) DeviceName() string {
	return C.GoString(C.ndctl_region_get_devname(r))
}

func (r *region) Size() uint64 {
	return uint64(C.ndctl_region_get_size(r))
}

func (r *region) AvailableSize() uint64 {
	return uint64(C.ndctl_region_get_available_size(r))
}

func (r *region) MaxAvailableExtent() uint64 {
	return uint64(C.ndctl_region_get_max_available_extent(r))
}

func (r *region) Type() RegionType {
	switch C.ndctl_region_get_type(r) {
	case C.ND_DEVICE_REGION_PMEM:
		return PmemRegion
	case C.ND_DEVICE_REGION_BLK:
		return BlockRegion
	}

	return UnknownRegion
}

func (r *region) TypeName() string {
	return C.GoString(C.ndctl_region_get_type_name(r))
}

func (r *region) Enabled() bool {
	return C.ndctl_region_is_enabled(r) != 0
}

func (r *region) Readonly() bool {
	return C.ndctl_region_get_ro(r) != 0
}

func (r *region) InterleaveWays() uint64 {
	return uint64(C.ndctl_region_get_interleave_ways(r))
}

func (r *region) ActiveNamespaces() []Namespace {
	return r.namespaces(true)
}

func (r *region) AllNamespaces() []Namespace {
	return r.namespaces(false)
}

func (r *region) Bus() Bus {
	return C.ndctl_region_get_bus(r)
}

func (r *region) Mappings() []Mapping {
	var mappings []Mapping
	for ndmap := C.ndctl_mapping_get_first(r); ndmap != nil; ndmap = C.ndctl_mapping_get_next(ndmap) {
		mappings = append(mappings, ndmap)
	}

	return mappings
}

func (r *region) SeedNamespace() Namespace {
	return C.ndctl_region_get_namespace_seed(r)
}

func (r *region) CreateNamespace(opts CreateNamespaceOpts) (Namespace, error) {
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
			return nil, fmt.Errorf("create namespace with size %v: %w", opts.Size, pmemerr.NotEnoughSpace)
		}
	}

	align := defaultAlign
	if opts.Align != 0 {
		if opts.Mode == SectorMode || opts.Mode == RawMode {
			klog.V(4).Infof("%s mode does not support setting an alignment, hence ignoring alignment", opts.Mode)
		} else {
			var err error
			align, err = r.AdaptAlign(opts.Align)
			if err != nil {
				return nil, err
			}
		}
	}

	if opts.Size != 0 {
		ways := uint64(C.ndctl_region_get_interleave_ways(r))
		align = align * ways
		if opts.Size%align != 0 {
			// Round up size to align with next block boundary.
			opts.Size = (opts.Size/align + 1) * align
			klog.V(4).Infof("%s: namespace size must align to interleave-width:%d * alignment:%d, force-align to %d",
				regionName, ways, align, opts.Size)
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
	ndns := (ns).(*namespace)

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
		klog.V(5).Infof("setting namespace sector size: %v", opts.SectorSize)
		err = ns.SetSectorSize(opts.SectorSize)
	}
	if err == nil {
		err = ns.SetEnforceMode(opts.Mode)
	}

	if err == nil {
		switch opts.Mode {
		case FsdaxMode:
			klog.V(5).Info("setting pfn")
			err = ndns.SetPfnSeed(opts.Location, align)
		case DaxMode:
			klog.V(5).Info("setting dax")
			err = ndns.setDaxSeed(opts.Location, align)
		case SectorMode:
			klog.V(5).Info("setting btt")
			err = ndns.setBttSeed(opts.SectorSize)
		}
	}
	if err == nil {
		klog.V(5).Info("enabling namespace")
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

func (r *region) AdaptAlign(align uint64) (uint64, error) {
	resource := uint64(C.ndctl_region_get_resource(r))
	if resource < uint64(C.ULLONG_MAX) && resource&(mib2-1) != 0 {
		klog.V(4).Infof("%s: falling back to a 4K alignment", r.DeviceName())
		align = kib4
	}
	if align != kib4 && align != mib2 && align != gib {
		return 0, fmt.Errorf("unsupported alignment: %v", align)
	}
	return align, nil
}

func (r *region) FsdaxAlignment() (uint64, error) {
	var align uint64
	// https://github.com/pmem/ndctl/blob/ea014c0c9ec8d0ef945d072dcc52b306c7a686f9/ndctl/namespace.c#L724-L732
	pfn := C.ndctl_region_get_pfn_seed(r)

	// https://github.com/pmem/ndctl/blob/ea014c0c9ec8d0ef945d072dcc52b306c7a686f9/ndctl/namespace.c#L799-L814
	//
	// The initial pfn device support in the kernel didn't
	// have the 'align' sysfs attribute and assumed a 2MB
	// alignment. Fall back to that if we don't have the
	// attribute.
	//
	if pfn != nil && C.ndctl_pfn_has_align(pfn) != 0 {
		align = (uint64)(C.ndctl_pfn_get_align(pfn))
	} else {
		align = mib2
	}
	return align, nil
}

func (r *region) DestroyNamespace(ns Namespace, force bool) error {
	var rc C.int
	devname := ns.DeviceName()
	if ns == nil {
		return fmt.Errorf("null namespace")
	}
	ndns := (ns).(*namespace)

	if rc = C.ndctl_region_get_ro(r); rc < 0 {
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

// Strings formats all relevant attributes as JSON.
func (r *region) String() string {
	return marshal(map[string]interface{}{
		"type":                 r.Type(),
		"dev":                  r.DeviceName(),
		"size":                 r.Size(),
		"available_size":       r.AvailableSize(),
		"max_available_extent": r.MaxAvailableExtent(),
		"namespaces":           r.ActiveNamespaces(),
		"mappings":             r.Mappings(),
	})
}

func (r *region) namespaces(onlyActive bool) []Namespace {
	var namespaces []Namespace

	for ndns := C.ndctl_namespace_get_first(r); ndns != nil; ndns = C.ndctl_namespace_get_next(ndns) {
		ns := (Namespace)(ndns)
		// If asked for only active namespaces return it regardless of it size
		// if not, return only valid namespaces, i.e, non-zero sized.
		if onlyActive {
			if ns.Active() {
				namespaces = append(namespaces, ns)
			}
		} else if ns.Size() > 0 {
			namespaces = append(namespaces, ns)
		}
	}

	return namespaces
}
