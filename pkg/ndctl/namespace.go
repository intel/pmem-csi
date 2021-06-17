package ndctl

//#cgo pkg-config: libndctl
//#include <string.h>
//#include <stdlib.h>
//#include <ndctl/libndctl.h>
//#define ARRAY_SIZE(a) (sizeof(a) / sizeof((a)[0]))
//#include <ndctl/ndctl.h>
import "C"
import (
	"fmt"

	/* needed for nullify
	"os"
	"syscall"*/
	"unsafe"

	"github.com/google/uuid"
)

const (
	//PmemNamespace pmem type namespace
	PmemNamespace NamespaceType = "pmem"
	//BlockNamespace block type namespace
	BlockNamespace NamespaceType = "blk"
	//IoNamespace io type namespace
	IoNamespace NamespaceType = "io"
	//UnknownType unknown namespace
	UnknownType NamespaceType = "unknown"
)

const (
	DaxMode     NamespaceMode = "dax"   //DevDax
	FsdaxMode   NamespaceMode = "fsdax" //Memory
	RawMode     NamespaceMode = "raw"
	SectorMode  NamespaceMode = "sector"
	UnknownMode NamespaceMode = "unknown"
)

const (
	MemoryMap MapLocation = "mem" // RAM
	DeviceMap MapLocation = "dev" // Block Device
	NoneMap   MapLocation = "none"
)

//NamespaceType type to represent namespace type
type NamespaceType string

//NamespaceMode represents mode of the namespace
type NamespaceMode string

func (mode NamespaceMode) toCMode() C.enum_ndctl_namespace_mode {
	switch mode {
	case DaxMode:
		return C.NDCTL_NS_MODE_DAX
	case FsdaxMode:
		return C.NDCTL_NS_MODE_FSDAX
	case RawMode:
		return C.NDCTL_NS_MODE_RAW
	case SectorMode:
		return C.NDCTL_NS_MODE_SAFE
	}
	return C.NDCTL_NS_MODE_UNKNOWN
}

type MapLocation string

func (loc MapLocation) toCPfnLocation() C.enum_ndctl_pfn_loc {
	if loc == MemoryMap {
		return C.NDCTL_PFN_LOC_RAM
	} else if loc == DeviceMap {
		return C.NDCTL_PFN_LOC_PMEM
	}

	return C.NDCTL_PFN_LOC_NONE
}

// Namespace is a go wrapper for ndctl_namespace.
type Namespace interface {
	// ID returns the namespace id.
	ID() uint
	// Name returns the name of the namespace.
	Name() string
	// DeviceName returns the device name of the namespace.
	DeviceName() string
	// BlockDeviceName returns the block device name of the namespace.
	BlockDeviceName() string
	// Size returns the size of the device provided by the namespace.
	Size() uint64
	// RawSize returns the amount of PMEM used by the namespace
	// in the underlying region, which is more than Size().
	RawSize() uint64
	// Mode returns the namespace mode.
	Mode() NamespaceMode
	// Type returns the namespace type.
	Type() NamespaceType
	// Enabled return true if the namespace is enabled.
	Enabled() bool
	// Active returns true if the namespace is active.
	Active() bool
	// UUID returns the uuid of the namespace.
	UUID() uuid.UUID
	// Location returns the namespace mapping location.
	Location() MapLocation
	// Region returns reference to the region that contains the namespace.
	Region() Region

	// SetAltName changes the alternative name of the namespace.
	SetAltName(name string) error
	// SetSize changes the size of the namespace.
	SetSize(size uint64) error
	// SetUUID changes the uuid of the namespace.
	SetUUID(uid uuid.UUID) error
	// SetSectorSize changes the sector size of the namespace.
	SetSectorSize(sectorSize uint64) error
	// SetEnforceMode changes how the namespace mode.
	SetEnforceMode(mode NamespaceMode) error
	// Enable activates the namespace.
	Enable() error
	// Disable deactivates the namespace.
	Disable() error
	// RawMode enables or disables direct access to the block device.
	SetRawMode(raw bool) error
	// SetPfnSeed creates a PFN for the namespace.
	SetPfnSeed(loc MapLocation, align uint64) error
}

type namespace = C.struct_ndctl_namespace

var _ Namespace = &namespace{}

func (ns *namespace) ID() uint {
	return uint(C.ndctl_namespace_get_id(ns))
}

func (ns *namespace) Name() string {
	return C.GoString(C.ndctl_namespace_get_alt_name(ns))
}

func (ns *namespace) DeviceName() string {
	return C.GoString(C.ndctl_namespace_get_devname(ns))
}

func (ns *namespace) BlockDeviceName() string {
	if DaxMode := C.ndctl_namespace_get_dax(ns); DaxMode != nil {
		/* Chardevice */
		return ""
	}
	var dev *C.char
	btt := C.ndctl_namespace_get_btt(ns)
	pfn := C.ndctl_namespace_get_pfn(ns)

	if btt != nil {
		dev = C.ndctl_btt_get_block_device(btt)
	} else if pfn != nil {
		dev = C.ndctl_pfn_get_block_device(pfn)
	} else {
		dev = C.ndctl_namespace_get_block_device(ns)
	}

	return C.GoString(dev)
}

func (ns *namespace) Size() uint64 {
	var size C.ulonglong

	mode := ns.Mode()

	switch mode {
	case FsdaxMode:
		if pfn := C.ndctl_namespace_get_pfn(ns); pfn != nil {
			size = C.ndctl_pfn_get_size(pfn)
		} else {
			size = C.ndctl_namespace_get_size(ns)
		}
	case DaxMode:
		if DaxMode := C.ndctl_namespace_get_dax(ns); DaxMode != nil {
			size = C.ndctl_dax_get_size(DaxMode)
		}
	case SectorMode:
		if btt := C.ndctl_namespace_get_btt(ns); btt != nil {
			size = C.ndctl_btt_get_size(btt)
		}
	case RawMode:
		size = C.ndctl_namespace_get_size(ns)
	}

	return uint64(size)
}

func (ns *namespace) RawSize() uint64 {
	switch ns.Mode() {
	case FsdaxMode:
		return uint64(C.ndctl_namespace_get_size(ns))
	default:
		return ns.Size()
	}
}

func (ns *namespace) Mode() NamespaceMode {
	mode := C.ndctl_namespace_get_mode(ns)

	if mode == C.NDCTL_NS_MODE_DAX || mode == C.NDCTL_NS_MODE_DEVDAX {
		return DaxMode
	}

	if mode == C.NDCTL_NS_MODE_MEMORY || mode == C.NDCTL_NS_MODE_FSDAX {
		return FsdaxMode
	}

	if mode == C.NDCTL_NS_MODE_RAW {
		return RawMode
	}

	if mode == C.NDCTL_NS_MODE_SAFE {
		return SectorMode
	}

	return UnknownMode
}

func (ns *namespace) Type() NamespaceType {
	switch C.ndctl_namespace_get_type(ns) {
	case C.ND_DEVICE_NAMESPACE_PMEM:
		return PmemNamespace
	case C.ND_DEVICE_NAMESPACE_BLK:
		return BlockNamespace
	case C.ND_DEVICE_NAMESPACE_IO:
		return IoNamespace
	}

	return UnknownType
}

func (ns *namespace) Enabled() bool {
	return C.ndctl_namespace_is_enabled(ns) == 1
}

func (ns *namespace) Active() bool {
	return bool(C.ndctl_namespace_is_active(ns))
}

func (ns *namespace) UUID() uuid.UUID {
	var cuid C.uuid_t

	btt := C.ndctl_namespace_get_btt(ns)
	DaxMode := C.ndctl_namespace_get_dax(ns)
	pfn := C.ndctl_namespace_get_pfn(ns)

	if btt != nil {
		C.ndctl_btt_get_uuid(btt, &cuid[0])
	} else if pfn != nil {
		C.ndctl_pfn_get_uuid(pfn, &cuid[0])
	} else if DaxMode != nil {
		C.ndctl_dax_get_uuid(DaxMode, &cuid[0])
	} else if C.ndctl_namespace_get_type(ns) != C.ND_DEVICE_NAMESPACE_IO {
		C.ndctl_namespace_get_uuid(ns, &cuid[0])
	}

	uidbytes := C.GoBytes(unsafe.Pointer(&cuid[0]), C.sizeof_uuid_t)
	_uuid, err := uuid.FromBytes(uidbytes)
	if err != nil {
		fmt.Printf("WARN: wrong uuid: %s", err.Error())
		return uuid.UUID{}
	}

	return _uuid
}

func (ns *namespace) Location() MapLocation {
	locations := map[uint32]MapLocation{
		C.NDCTL_PFN_LOC_NONE: NoneMap,
		C.NDCTL_PFN_LOC_RAM:  MemoryMap,
		C.NDCTL_PFN_LOC_PMEM: DeviceMap,
	}
	mode := ns.Mode()

	switch mode {
	case FsdaxMode:
		if pfn := C.ndctl_namespace_get_pfn(ns); pfn != nil {
			return locations[C.ndctl_pfn_get_location(pfn)]
		}
		return locations[C.NDCTL_PFN_LOC_RAM]
	case DaxMode:
		if dax := C.ndctl_namespace_get_dax(ns); dax != nil {
			return locations[C.ndctl_dax_get_location(dax)]
		}
	}

	return locations[C.NDCTL_PFN_LOC_NONE]
}

func (ns *namespace) Region() Region {
	return C.ndctl_namespace_get_region(ns)

}

func (ns *namespace) SetAltName(name string) error {
	if rc := C.ndctl_namespace_set_alt_name(ns, C.CString(name)); rc != 0 {
		return fmt.Errorf("Failed to set namespace name: %s", cErrorString(rc))
	}

	return nil
}

func (ns *namespace) SetSize(size uint64) error {

	if rc := C.ndctl_namespace_set_size(ns, C.ulonglong(size)); rc != 0 {
		return fmt.Errorf("Failed to set namespace size: %s", cErrorString(rc))
	}

	return nil
}

func (ns *namespace) SetUUID(uid uuid.UUID) error {

	if rc := C.ndctl_namespace_set_uuid(ns, (*C.uchar)(&uid[0])); rc != 0 {
		return fmt.Errorf("Failed to set namespace uid: %s", cErrorString(rc))
	}
	return nil
}

func (ns *namespace) SetSectorSize(sectorSize uint64) error {

	if sectorSize == 0 {
		sectorSize = 512
	}

	sSize := C.uint(sectorSize)

	for num := C.ndctl_namespace_get_num_sector_sizes(ns) - 1; num >= 0; num-- {
		if C.uint(C.ndctl_namespace_get_supported_sector_size(ns, num)) == sSize {
			if rc := C.ndctl_namespace_set_sector_size(ns, sSize); rc < 0 {
				return fmt.Errorf("Failed to set namespace sector size: %s", cErrorString(rc))
			}
			return nil
		}
	}

	return fmt.Errorf("Sector size %v not supported", sectorSize)
}

func (ns *namespace) SetEnforceMode(mode NamespaceMode) error {

	if rc := C.ndctl_namespace_set_enforce_mode(ns, mode.toCMode()); rc != 0 {
		return fmt.Errorf("Failed to set enforce mode: %s", cErrorString(rc))
	}

	return nil
}

func (ns *namespace) Enable() error {
	if rc := C.ndctl_namespace_enable(ns); rc < 0 {
		return fmt.Errorf("failed to enable namespace: %s", cErrorString(rc))
	}

	return nil
}

func (ns *namespace) Disable() error {
	if rc := C.ndctl_namespace_disable_safe(ns); rc < 0 {
		return fmt.Errorf("failed to disable namespace: %s", cErrorString(rc))
	}

	return nil
}

func (ns *namespace) SetRawMode(raw bool) error {
	var rawMode C.int
	if raw {
		rawMode = 1
	}
	if rc := C.ndctl_namespace_set_raw_mode(ns, rawMode); rc < 0 {
		return fmt.Errorf("failed to set raw mode: %s", cErrorString(rc))
	}

	return nil
}

// String formats all relevant attributes as JSON.
func (ns *namespace) String() string {
	props := map[string]interface{}{
		"id":      ns.ID(),
		"dev":     ns.DeviceName(),
		"mode":    ns.Mode(),
		"size":    ns.Size(),
		"enabled": ns.Enabled(),
		"uuid":    ns.UUID(),
		"name":    ns.Name(),
	}

	if mode := ns.Mode(); mode != DaxMode {
		props["blockdev"] = ns.BlockDeviceName()
	}

	if location := ns.Location(); location != "none" {
		props["map"] = location
	}

	return marshal(props)
}

func (ns *namespace) SetPfnSeed(loc MapLocation, align uint64) error {
	var rc C.int
	r := (ns.Region()).(*region)
	pfn := C.ndctl_region_get_pfn_seed(r)
	if pfn == nil {
		return fmt.Errorf("pfn: no seed")
	}
	uid, _ := uuid.NewUUID()
	if rc = C.ndctl_pfn_set_uuid(pfn, (*C.uchar)(&uid[0])); rc < 0 {
		return fmt.Errorf("pfn: failed to set: %s", cErrorString(rc))
	}
	if rc = C.ndctl_pfn_set_location(pfn, loc.toCPfnLocation()); rc < 0 {
		return fmt.Errorf("pfn: failed to set location")
	}
	if align != 0 && C.ndctl_pfn_has_align(pfn) == 1 {
		if rc = C.ndctl_pfn_set_align(pfn, C.ulong(align)); rc < 0 {
			return fmt.Errorf("pfn: failed to set alignment: %s", cErrorString(rc))
		}
	}

	if rc = C.ndctl_pfn_set_namespace(pfn, ns); rc < 0 {
		return fmt.Errorf("pfn: failed to set namespace")
	}

	if rc = C.ndctl_pfn_enable(pfn); rc < 0 {
		// reset pfn seed in failure case
		C.ndctl_pfn_set_namespace(pfn, nil)
		return fmt.Errorf("pfn: failed to enable")
	}

	return nil
}

func (ns *namespace) setDaxSeed(loc MapLocation, align uint64) error {
	var rc C.int
	r := (ns.Region()).(*region)
	dax := C.ndctl_region_get_dax_seed(r)
	if dax == nil {
		return fmt.Errorf("dax: no seed")
	}

	uid, _ := uuid.NewUUID()
	if rc = C.ndctl_dax_set_uuid(dax, (*C.uchar)(&uid[0])); rc < 0 {
		return fmt.Errorf("dax: failed to set uuid")
	}
	if rc = C.ndctl_dax_set_location(dax, loc.toCPfnLocation()); rc < 0 {
		return fmt.Errorf("dax: failed to set dax location")
	}
	/* device-dax assumes 'align' attribute present */
	if align != 0 {
		if rc = C.ndctl_dax_set_align(dax, C.ulong(align)); rc < 0 {
			return fmt.Errorf("dax: failed to set dax alignment")
		}
	}

	if rc = C.ndctl_dax_set_namespace(dax, ns); rc < 0 {
		return fmt.Errorf("dax: failed to set namespace")
	}

	if rc = C.ndctl_dax_enable(dax); rc < 0 {
		C.ndctl_dax_set_namespace(dax, nil)
		return fmt.Errorf("dax: failed to enable")
	}

	return nil
}

func (ns *namespace) setBttSeed(sectorSize uint64) error {
	r := (ns.Region()).(*region)
	btt := C.ndctl_region_get_btt_seed(r)
	var rc C.int
	if btt == nil {
		return fmt.Errorf("btt: no seed")
	}
	uid, _ := uuid.NewUUID()
	if rc = C.ndctl_btt_set_uuid(btt, (*C.uchar)(&uid[0])); rc < 0 {
		return fmt.Errorf("btt: failed to set btt")
	}
	if rc = C.ndctl_btt_set_sector_size(btt, C.uint(sectorSize)); rc < 0 {
		return fmt.Errorf("btt: failed to set sector size")
	}
	if rc = C.ndctl_btt_set_namespace(btt, ns); rc < 0 {
		return fmt.Errorf("btt: failed to set namespace")
	}
	if rc = C.ndctl_btt_enable(btt); rc < 0 {
		C.ndctl_btt_set_namespace(btt, nil)
		return fmt.Errorf("btt: failed to enable")
	}

	return nil
}
