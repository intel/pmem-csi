package ndctl

//#cgo pkg-config: libndctl
//#include <string.h>
//#include <stdlib.h>
//#include <ndctl/libndctl.h>
//#define ARRAY_SIZE(a) (sizeof(a) / sizeof((a)[0]))
//#include <ndctl/ndctl.h>
import "C"
import (
	"encoding/json"
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

// Namespace go wrapper for ndctl_namespace
type Namespace C.struct_ndctl_namespace

//ID returns namespace id
func (ns *Namespace) ID() uint {
	ndns := (*C.struct_ndctl_namespace)(ns)
	return uint(C.ndctl_namespace_get_id(ndns))
}

//Name returns name of the namespace
func (ns *Namespace) Name() string {
	ndns := (*C.struct_ndctl_namespace)(ns)
	return C.GoString(C.ndctl_namespace_get_alt_name(ndns))
}

//DeviceName returns namespace device name
func (ns *Namespace) DeviceName() string {
	ndns := (*C.struct_ndctl_namespace)(ns)
	return C.GoString(C.ndctl_namespace_get_devname(ndns))
}

//BlockDeviceName return namespace block device name
func (ns *Namespace) BlockDeviceName() string {
	ndns := (*C.struct_ndctl_namespace)(ns)
	if DaxMode := C.ndctl_namespace_get_dax(ndns); DaxMode != nil {
		/* Chardevice */
		return ""
	}
	var dev *C.char
	btt := C.ndctl_namespace_get_btt(ndns)
	pfn := C.ndctl_namespace_get_pfn(ndns)

	if btt != nil {
		dev = C.ndctl_btt_get_block_device(btt)
	} else if pfn != nil {
		dev = C.ndctl_pfn_get_block_device(pfn)
	} else {
		dev = C.ndctl_namespace_get_block_device(ndns)
	}

	return C.GoString(dev)
}

//Size returns size of the namespace
func (ns *Namespace) Size() uint64 {
	var size C.ulonglong

	ndns := (*C.struct_ndctl_namespace)(ns)
	mode := ns.Mode()

	switch mode {
	case FsdaxMode:
		if pfn := C.ndctl_namespace_get_pfn(ndns); pfn != nil {
			size = C.ndctl_pfn_get_size(pfn)
		} else {
			size = C.ndctl_namespace_get_size(ndns)
		}
	case DaxMode:
		if DaxMode := C.ndctl_namespace_get_dax(ndns); DaxMode != nil {
			size = C.ndctl_dax_get_size(DaxMode)
		}
	case SectorMode:
		if btt := C.ndctl_namespace_get_btt(ndns); btt != nil {
			size = C.ndctl_btt_get_size(btt)
		}
	case RawMode:
		size = C.ndctl_namespace_get_size(ndns)
	}

	return uint64(size)
}

//Mode returns namespace mode
func (ns *Namespace) Mode() NamespaceMode {
	ndns := (*C.struct_ndctl_namespace)(ns)
	mode := C.ndctl_namespace_get_mode(ndns)

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

//Type returns namespace type
func (ns *Namespace) Type() NamespaceType {
	ndns := (*C.struct_ndctl_namespace)(ns)
	switch C.ndctl_namespace_get_type(ndns) {
	case C.ND_DEVICE_NAMESPACE_PMEM:
		return PmemNamespace
	case C.ND_DEVICE_NAMESPACE_BLK:
		return BlockNamespace
	case C.ND_DEVICE_NAMESPACE_IO:
		return IoNamespace
	}

	return UnknownType
}

//NumaNode returns numa node number attached to this namespace
func (ns *Namespace) NumaNode() int {
	ndns := (*C.struct_ndctl_namespace)(ns)
	return int(C.ndctl_namespace_get_numa_node(ndns))
}

//Enabled return if namespace is enabled
func (ns *Namespace) Enabled() bool {
	ndns := (*C.struct_ndctl_namespace)(ns)
	return C.ndctl_namespace_is_enabled(ndns) == 1
}

//Active return if namespace is active
func (ns *Namespace) Active() bool {
	ndns := (*C.struct_ndctl_namespace)(ns)
	return bool(C.ndctl_namespace_is_active(ndns))
}

//UUID returns uuid of the namespace
func (ns *Namespace) UUID() uuid.UUID {
	ndns := (*C.struct_ndctl_namespace)(ns)
	var cuid C.uuid_t

	btt := C.ndctl_namespace_get_btt(ndns)
	DaxMode := C.ndctl_namespace_get_dax(ndns)
	pfn := C.ndctl_namespace_get_pfn(ndns)

	if btt != nil {
		C.ndctl_btt_get_uuid(btt, &cuid[0])
	} else if pfn != nil {
		C.ndctl_pfn_get_uuid(pfn, &cuid[0])
	} else if DaxMode != nil {
		C.ndctl_dax_get_uuid(DaxMode, &cuid[0])
	} else if C.ndctl_namespace_get_type(ndns) != C.ND_DEVICE_NAMESPACE_IO {
		C.ndctl_namespace_get_uuid(ndns, &cuid[0])
	}

	uidbytes := C.GoBytes(unsafe.Pointer(&cuid[0]), C.sizeof_uuid_t)
	_uuid, err := uuid.FromBytes(uidbytes)
	if err != nil {
		fmt.Printf("WARN: wrong uuid: %s", err.Error())
		return uuid.UUID{}
	}

	return _uuid
}

//Location returns namespace mapping location
func (ns *Namespace) Location() MapLocation {
	ndns := (*C.struct_ndctl_namespace)(ns)
	locations := map[uint32]MapLocation{
		C.NDCTL_PFN_LOC_NONE: NoneMap,
		C.NDCTL_PFN_LOC_RAM:  MemoryMap,
		C.NDCTL_PFN_LOC_PMEM: DeviceMap,
	}
	mode := ns.Mode()

	switch mode {
	case FsdaxMode:
		if pfn := C.ndctl_namespace_get_pfn(ndns); pfn != nil {
			return locations[C.ndctl_pfn_get_location(pfn)]
		}
		return locations[C.NDCTL_PFN_LOC_RAM]
	case DaxMode:
		if dax := C.ndctl_namespace_get_dax(ndns); dax != nil {
			return locations[C.ndctl_dax_get_location(dax)]
		}
	}

	return locations[C.NDCTL_PFN_LOC_NONE]
}

//Region returns reference to Region that this namespace is part of
func (ns *Namespace) Region() *Region {
	ndns := (*C.struct_ndctl_namespace)(ns)
	return (*Region)(C.ndctl_namespace_get_region(ndns))

}

func (ns *Namespace) SetAltName(name string) error {
	ndns := (*C.struct_ndctl_namespace)(ns)
	if rc := C.ndctl_namespace_set_alt_name(ndns, C.CString(name)); rc != 0 {
		return fmt.Errorf("Failed to set namespace name: %s", cErrorString(rc))
	}

	return nil
}

func (ns *Namespace) SetSize(size uint64) error {
	ndns := (*C.struct_ndctl_namespace)(ns)

	if rc := C.ndctl_namespace_set_size(ndns, C.ulonglong(size)); rc != 0 {
		return fmt.Errorf("Failed to set namespace size: %s", cErrorString(rc))
	}

	return nil
}

func (ns *Namespace) SetUUID(uid uuid.UUID) error {
	ndns := (*C.struct_ndctl_namespace)(ns)

	if rc := C.ndctl_namespace_set_uuid(ndns, (*C.uchar)(&uid[0])); rc != 0 {
		return fmt.Errorf("Failed to set namespace uid: %s", cErrorString(rc))
	}
	return nil
}

func (ns *Namespace) SetSectorSize(sectorSize uint64) error {
	ndns := (*C.struct_ndctl_namespace)(ns)

	if sectorSize == 0 {
		sectorSize = 512
	}

	sSize := C.uint(sectorSize)

	for num := C.ndctl_namespace_get_num_sector_sizes(ndns) - 1; num >= 0; num-- {
		if C.uint(C.ndctl_namespace_get_supported_sector_size(ndns, num)) == sSize {
			if rc := C.ndctl_namespace_set_sector_size(ndns, sSize); rc < 0 {
				return fmt.Errorf("Failed to set namespace sector size: %s", cErrorString(rc))
			}
			return nil
		}
	}

	return fmt.Errorf("Sector size %v not supported", sectorSize)
}

func (ns *Namespace) SetEnforceMode(mode NamespaceMode) error {
	ndns := (*C.struct_ndctl_namespace)(ns)

	if rc := C.ndctl_namespace_set_enforce_mode(ndns, mode.toCMode()); rc != 0 {
		return fmt.Errorf("Failed to set enforce mode: %s", cErrorString(rc))
	}

	return nil
}

func (ns *Namespace) Enable() error {
	ndns := (*C.struct_ndctl_namespace)(ns)
	if rc := C.ndctl_namespace_enable(ndns); rc < 0 {
		return fmt.Errorf("failed to enable namespace:%s", cErrorString(rc))
	}

	return nil
}

//MarshalJSON returns json encoding of namespace
func (ns *Namespace) MarshalJSON() ([]byte, error) {

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

	return json.Marshal(props)
}

func (ns *Namespace) setPfnSeed(loc MapLocation, align uint64) error {
	var rc C.int
	ndns := (*C.struct_ndctl_namespace)(ns)
	ndr := (*C.struct_ndctl_region)(ns.Region())
	pfn := C.ndctl_region_get_pfn_seed(ndr)
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

	if rc = C.ndctl_pfn_set_namespace(pfn, ndns); rc < 0 {
		return fmt.Errorf("pfn: failed to set namespace")
	}

	if rc = C.ndctl_pfn_enable(pfn); rc < 0 {
		// reset pfn seed in failure case
		C.ndctl_pfn_set_namespace(pfn, nil)
		return fmt.Errorf("pfn: failed to enable")
	}

	return nil
}

func (ns *Namespace) setDaxSeed(loc MapLocation, align uint64) error {
	var rc C.int
	ndns := (*C.struct_ndctl_namespace)(ns)
	ndr := (*C.struct_ndctl_region)(ns.Region())
	dax := C.ndctl_region_get_dax_seed(ndr)
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

	if rc = C.ndctl_dax_set_namespace(dax, ndns); rc < 0 {
		return fmt.Errorf("dax: failed to set namespace")
	}

	if rc = C.ndctl_dax_enable(dax); rc < 0 {
		C.ndctl_dax_set_namespace(dax, nil)
		return fmt.Errorf("dax: failed to enable")
	}

	return nil
}

func (ns *Namespace) setBttSeed(sectorSize uint64) error {
	ndns := (*C.struct_ndctl_namespace)(ns)
	ndr := (*C.struct_ndctl_region)(ns.Region())
	btt := C.ndctl_region_get_btt_seed(ndr)
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
	if rc = C.ndctl_btt_set_namespace(btt, ndns); rc < 0 {
		return fmt.Errorf("btt: failed to set namespace")
	}
	if rc = C.ndctl_btt_enable(btt); rc < 0 {
		C.ndctl_btt_set_namespace(btt, nil)
		return fmt.Errorf("btt: failed to enable")
	}

	return nil
}

/* nullify commented out as unused, was not reliable
func (ns *Namespace) nullify() error {
	ndns := (*C.struct_ndctl_namespace)(ns)
	var rc C.int
	var buf unsafe.Pointer
	var err error
	var file *os.File

	C.ndctl_namespace_set_raw_mode(ndns, 1)
	defer C.ndctl_namespace_set_raw_mode(ndns, 0)
	if err = ns.Enable(); err != nil {
		return err
	}

	if rc = C.posix_memalign(&buf, C.size_t(kib4), C.size_t(kib4)); rc < 0 {
		return fmt.Errorf("memory error: %s", cErrorString(rc))
	}
	defer C.free(buf)
	C.memset(buf, 0, C.size_t(kib4))

	file, err = os.OpenFile("/dev/"+ns.BlockDeviceName(), os.O_RDWR|syscall.O_DIRECT|os.O_EXCL, os.ModeDevice)
	if err != nil {
		return err
	}
	defer file.Close()

	bytes := *(*[]byte)(buf)
	if _, err = file.Write(bytes); err != nil {
		err = fmt.Errorf("failed to zero info block %s", err.Error())
	}

	return err
}*/
