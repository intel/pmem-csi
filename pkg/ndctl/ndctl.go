package ndctl

//#cgo pkg-config: libndctl uuid
//#cgo CFLAGS: -DHAVE_LIBUUID
//#define ARRAY_SIZE(a) (sizeof(a) / sizeof((a)[0]))
//#define _GNU_SOURCE
//#include <string.h>
//#include <uuid/uuid.h>
//#include <stdio.h>
//#include <stdlib.h>
//#include <string.h>
//#include <fcntl.h>
//#include <limits.h>
//#include <ndctl/libndctl.h>
//#include <ndctl/ndctl.h>
//
//#define debug(fmt, ...) \
//		fprintf(stderr, "[DBG] %s:%d: " fmt, __func__, __LINE__, ##__VA_ARGS__)
//
//#define warn(fmt, ...) \
//     fprintf(stderr, "[WARN] %s:%d: " fmt, __func__, __LINE__, ##__VA_ARGS__)
//
//#define try(prefix, action, ...) \
//do { \
//	rc = prefix##_##action(__VA_ARGS__); \
//	if (rc) { \
//		warn(#prefix"_"#action ": failed with error : %s\n", strerror(-rc)); \
//		goto end; \
//	} else { \
//		debug(#prefix "_" #action " : success\n"); \
//	} \
//}while(0);
//
//#define SZ_4K (4 * 1024)
//#define SZ_1M (1024 * 1024)
//#define SZ_2M (2 * SZ_1M)
//#define SZ_1G (1024 * SZ_1M)
//
//enum ndctl_namespace_type {
//     NDCTL_NS_TYPE_UNKNOWN,
//     NDCTL_NS_TYPE_PMEM = ND_DEVICE_REGION_PMEM,
//     NDCTL_NS_TYPE_BLK = ND_DRIVER_REGION_BLK,
//};
//
//struct ndctl_namespace_create_opts {
// 	   char name[64];                  /* name of the namespace */
//     enum ndctl_namespace_type ns_type; /* type of namespace:  ND_NS_TYPE_{PMEM|BLK} [default: PMEM] */
//     enum ndctl_namespace_mode mode; /* operation mode : ND_NS_MODE_{MEMROY|SAFE|DAX|FSDAX|RAS} [default: MEMORY] */
// 	   unsigned long long size;        /* size in bytes [default: available size] */
//     unsigned long sector_size;      /* logical sector sector size [default to namespace sector size] */
//     const char *map_location;       /* location of the memmap : 'mem' or 'dev' [default: dev] */
//     unsigned long align;            /* namespace alignment in bytes [default: 2 * 1024 * 1024] */
//} default_options ;
//
// void ndctl_namespace_options_set_name(struct ndctl_namespace_create_opts *opts, const char *name) {
//	if (opts && name) {
//		snprintf(opts->name, 64, "%s", name);
//	}
//}
//
//static void _reset_default_options() {
//     sprintf(default_options.name, "%s", "");
//     default_options.ns_type = NDCTL_NS_TYPE_PMEM;
//     default_options.mode = NDCTL_NS_MODE_MEMORY;
//     default_options.size = 0;
//     default_options.sector_size = 0;
//     default_options.map_location = "dev";
//     default_options.align = SZ_2M;
//}
//
//static int _set_btt_sector_size(struct ndctl_btt *btt, unsigned long size) {
//	int rc = -EINVAL;
//	int num;
//	if (!btt) {
//		return rc;
//	}
//
//	num = ndctl_btt_get_num_sector_sizes(btt) - 1;
//	for (; num >= 0; num--) {
//		if (ndctl_btt_get_supported_sector_size(btt, num) == size) {
//			try(ndctl_btt, set_sector_size, btt, size);
//		}
//	}
//end:
//	return rc;
//}
//
//static int _set_namespace_sector_size(struct ndctl_namespace *ndns, unsigned long size) {
//	int num = 0;
//	int rc = -EINVAL;
//	if (!ndns) {
//		return rc;
//	}
//	num = ndctl_namespace_get_num_sector_sizes(ndns);
//	for (; num >= 0; num--) {
// 		if (ndctl_namespace_get_supported_sector_size(ndns, num) == size) {
//			try(ndctl_namespace, set_sector_size, ndns, size);
//		}
//	}
//end:
//	return rc;
//}
//
//int ndctl_region_create_namespace(struct ndctl_region *region,
//         struct ndctl_namespace_create_opts *opts,
//         struct ndctl_namespace **ndns_out) {
//	unsigned long long available_size;
//  struct ndctl_namespace *ndns = NULL;
//  enum ndctl_pfn_loc pfn_loc = NDCTL_PFN_LOC_NONE;
//  const char *region_name = ndctl_region_get_devname(region);
//  struct ndctl_btt *btt = NULL;
//  unsigned long long resource;
//  unsigned long long size_align = 0;
//  uuid_t uid;
//  int rc;
//
//  if (!ndctl_region_is_enabled(region)) {
//		warn("Region '%s' not enabled, hence ignoring\n", region_name);
//  	return -EAGAIN;
//  }
//  if (ndctl_region_get_ro(region)) {
//		warn("Readonly region: %s\n", region_name);
//  	return -EAGAIN;
//  }
//
//  available_size = ndctl_region_get_max_available_extent(region);
//  if (available_size == ULLONG_MAX)
// 		available_size = ndctl_region_get_available_size(region);
//
//	if (opts->size == 0)
//     opts->size = available_size;
//  else if (opts->size > available_size) {
//      debug("Not enough(%llu) space in region %s(%llu)\n", opts->size, ndctl_region_get_devname(region), available_size);
//  	return -EAGAIN;
//  }
//
//	if (opts->map_location) {
//		pfn_loc = !strcmp("mem", opts->map_location) ? NDCTL_PFN_LOC_RAM : NDCTL_PFN_LOC_PMEM;
//		if (opts->mode != NDCTL_NS_MODE_MEMORY
//       && opts->mode != NDCTL_NS_MODE_DAX) {
//			warn("%s: pfn map location(%s) only valid for fsdax mode namespace\n", \
//                region_name, opts->map_location);
//			return -EINVAL;
//		}
//	} else if (opts->mode == NDCTL_NS_MODE_MEMORY
//          || opts->mode == NDCTL_NS_MODE_DAX) {
//		debug("Using NDCTL_PFN_LOC_PMEM pfn location\n");
//		pfn_loc = NDCTL_PFN_LOC_PMEM;
//	}
//
//	if (opts->align) {
//		size_align = opts->align;
//		if (opts->mode == NDCTL_NS_MODE_SAFE
//			|| opts->mode == NDCTL_NS_MODE_RAW) {
//			warn("%s mode does not support setting an alignment, hence ignoring alignment\n",
//				 opts->mode == NDCTL_NS_MODE_SAFE ? "sector" : "raw");
//		} else {
//			resource = ndctl_region_get_resource(region);
//			if (resource < ULLONG_MAX && (resource & (SZ_2M - 1))) {
//				debug("%s: falling back to a 4K alignment\n", region_name);
//				size_align = SZ_4K;
//			} else if (size_align != SZ_4K && size_align != SZ_2M && size_align != SZ_1G) {
//				warn("unsupported alignment: %lld", size_align);
//				return -ENXIO;
//			}
//		}
//	} else {
//		/* default to 2MB alignment */
//		size_align = SZ_2M;
//	}
//
//	/* setup_namespace */
//
//	ndns = ndctl_region_get_namespace_seed(region);
//	if (!ndns || ndctl_namespace_is_active(ndns)) {
//		warn("No available namespace found in region %s\n", region_name);
//		return -EAGAIN;
//	}
//
//	if (ndctl_namespace_get_type(ndns) != ND_DEVICE_NAMESPACE_IO) {
//		uuid_generate(uid);
//		try(ndctl_namespace, set_alt_name, ndns, opts->name);
//		try(ndctl_namespace, set_uuid, ndns, uid);
//		try(ndctl_namespace, set_size, ndns, opts->size);
//	}
//
//	_set_namespace_sector_size(ndns, opts->sector_size);
//
//	ndctl_namespace_set_enforce_mode(ndns, opts->mode);
//
//	uuid_generate(uid);
//	if (opts->mode == NDCTL_NS_MODE_MEMORY
//	 && ndctl_namespace_get_mode(ndns) != NDCTL_NS_MODE_MEMORY
//   && pfn_loc == NDCTL_PFN_LOC_PMEM) {
//		struct ndctl_pfn *pfn = ndctl_region_get_pfn_seed(region);
//		if (!pfn) {
//			warn("%s: no pfn seed", region_name);
//			rc = -EINVAL;
//			goto end;
//		}
//		try(ndctl_pfn, set_uuid, pfn, uid);
//		try(ndctl_pfn, set_location, pfn, pfn_loc);
//		if (ndctl_pfn_has_align(pfn)) {
//			try(ndctl_pfn, set_align, pfn, size_align);
//		} else if(size_align){
//			warn("Ignoring alignment(%lld) as '%s' region not support "
//				 "alignment for devdax mode\n", size_align, region_name);
//		}
//		try(ndctl_pfn, set_namespace, pfn, ndns);
//		rc = ndctl_pfn_enable(pfn);
//		/* reset pfn seed in failure case */
//		if (rc) ndctl_pfn_set_namespace(pfn, NULL);
//
//	} else if (opts->mode == NDCTL_NS_MODE_DAX) {
//		struct ndctl_dax *dax = ndctl_region_get_dax_seed(region);
//		if (!dax) {
//			warn("%s: no dax seed", region_name);
//			rc = -EINVAL;
//			goto end;
//		}
//
//		try(ndctl_dax, set_uuid, dax, uid);
//		try(ndctl_dax, set_location, dax, pfn_loc);
//		if (ndctl_dax_has_align(dax)) {
//			try(ndctl_dax, set_align, dax, size_align);
//		} else if(size_align) {
//			warn("Ignoring alignment(%lld) as '%s' region not support "
//				 "alignment for fsvdax mode\n", size_align, region_name);
//		}
//		try(ndctl_dax, set_namespace, dax, ndns);
//		rc = ndctl_dax_enable(dax);
//		/* reset dax seed in failure case */
//		if (rc) ndctl_dax_set_namespace(dax, NULL);
//	} else if (opts->mode == NDCTL_NS_MODE_SAFE) {
//		struct ndctl_btt *btt = ndctl_region_get_btt_seed(region);
//		unsigned long sector_size = opts->sector_size == UINT_MAX ? 4096 : opts->sector_size;
//		if (!btt) {
//			warn("%s: does not support 'sector' mode\n", region_name);
//			rc = -EINVAL;
//			goto end;
//		}
//		if (_set_btt_sector_size(btt, sector_size) == 0) {
//			warn("failed to set btt sector size(%ld)", sector_size);
//			rc = -EINVAL;
//			goto end;
//		}
//		try(ndctl_btt, set_uuid, btt, uid);
//		try(ndctl_btt, set_namespace, btt, ndns);
//		rc = ndctl_btt_enable(btt);
//		/* reset btt seed in failure case */
//		if (rc) ndctl_btt_set_namespace(btt, NULL);
//	} else {
//		rc = ndctl_namespace_enable(ndns);
//	}
//
//end:
//	if (rc) {
//		/* reset namespace seed in failure case */
//		ndctl_namespace_set_enforce_mode(ndns, NDCTL_NS_MODE_RAW);
//		ndctl_namespace_delete(ndns);
//	} else if(ndns_out) {
//		*ndns_out = ndns;
//	}
//
//	return rc;
//}
//
//int ndctl_bus_create_namespace(struct ndctl_bus *bus, struct ndctl_namespace_create_opts *opts,
//	struct ndctl_namespace **ndns) {
//	struct ndctl_region *region;
//
//	ndctl_region_foreach(bus, region) {
//		const char *name = ndctl_region_get_devname(region);
//		if (opts->ns_type != NDCTL_NS_TYPE_UNKNOWN
//       && opts->ns_type != ndctl_region_get_type(region)) {
//          debug("Region '%s' type: %d, requested namespace type: %d\n", name, \
//                ndctl_region_get_type(region), opts->ns_type);\
//      	continue;
//      }
//      if (opts->size) {
//      	unsigned long long available = ndctl_region_get_max_available_extent(region);
//          if (available == ULLONG_MAX)
//          	available = ndctl_region_get_available_size(region);
//			debug("Region: %s, available size: %lld\n",
//                name, available);
//          if (opts->size > available)
//          	continue;
//      }
//		return ndctl_region_create_namespace(region, opts, ndns);
//  }
//
//	debug("No valid region found with size %lld\n", opts->size);
//
//	return -ENXIO;
//}
//
//int ndctl_context_create_namespace(struct ndctl_ctx *ctx, struct ndctl_namespace_create_opts *opts,
//      struct ndctl_namespace **ndns) {
//	struct ndctl_bus *bus;
//
//	if (!opts) {
//  	_reset_default_options();
//      opts = &default_options;
//	} else {
//		if (opts->ns_type == NDCTL_NS_TYPE_UNKNOWN) {
//			opts->ns_type = NDCTL_NS_TYPE_PMEM;
//		}
//		if (opts->mode == NDCTL_NS_MODE_UNKNOWN) {
//			opts->mode = NDCTL_NS_MODE_MEMORY;
//		}
//		if (opts->map_location == NULL) {
//	    	opts->map_location = "dev";
//		}
//		if (opts->align == 0) {
//     		opts->align = SZ_2M;
//		}
//	}
//	debug("name: %s, type: %d, mode: %d, size: %lld, algin: %ld, map location: %s\n",
//        opts->name, opts->ns_type, opts->mode, opts->size, opts->align, opts->map_location);
//
//	bus = ndctl_bus_get_first(ctx);
//	return ndctl_bus_create_namespace(bus, opts, ndns);
//}
//
//static int _zero_info_block(struct ndctl_namespace *ndns)
//{
// 	const char *devname = ndctl_namespace_get_devname(ndns);
// 	int fd, rc = -ENXIO;
// 	void *buf = NULL;
// 	char path[50];
//
// 	ndctl_namespace_set_raw_mode(ndns, 1);
// 	rc = ndctl_namespace_enable(ndns);
// 	if (rc < 0) {
// 		debug("%s failed to enable for zeroing, continuing\n", devname);
// 		rc = 0;
// 		goto out;
// 	}
//
// 	if (posix_memalign(&buf, 4096, 4096) != 0)
// 		return -ENXIO;
//
// 	sprintf(path, "/dev/%s", ndctl_namespace_get_block_device(ndns));
// 	fd = open(path, O_RDWR|O_DIRECT|O_EXCL);
// 	if (fd < 0) {
// 		debug("%s: failed to open %s to zero info block\n",
// 				devname, path);
// 		goto out;
// 	}
//
// 	memset(buf, 0, 4096);
// 	rc = pwrite(fd, buf, 4096, 4096);
// 	if (rc < 4096) {
// 		debug("%s: failed to zero info block %s\n",
// 				devname, path);
// 		rc = -ENXIO;
// 	} else
// 		rc = 0;
// 	close(fd);
//  out:
// 	ndctl_namespace_set_raw_mode(ndns, 0);
// 	ndctl_namespace_disable_invalidate(ndns);
// 	free(buf);
// 	return rc;
//}
//
//int ndctl_region_destroy_namespace(struct ndctl_region *region, struct ndctl_namespace *ndns, bool force) {
//	const char *devname = ndctl_namespace_get_devname(ndns);
//	struct ndctl_pfn *pfn = ndctl_namespace_get_pfn(ndns);
//	struct ndctl_dax *dax = ndctl_namespace_get_dax(ndns);
//	struct ndctl_btt *btt = ndctl_namespace_get_btt(ndns);
// 	int rc;
//
// 	if (ndctl_region_get_ro(region)) {
// 		warn("%s: read-only\n", devname);
// 		return -ENXIO;
// 	}
//
// 	if (ndctl_namespace_is_active(ndns) && !force) {
// 		warn("%s is active\n", devname);
// 		return -EBUSY;
// 	} else if (rc = ndctl_namespace_disable_safe(ndns)) {
// 		return rc;
// 	}
//
// 	ndctl_namespace_set_enforce_mode(ndns, NDCTL_NS_MODE_RAW);
//
//	if ((pfn || btt || dax) && (rc = _zero_info_block(ndns))) {
// 		return rc;
// 	}
//
// 	rc = ndctl_namespace_delete(ndns);
// 	if (rc)
// 		debug("%s: failed to reclaim\n", devname);
//
// 	return 0;
//}
import "C"
import (
	"encoding/json"
	"fmt"
	"unsafe"

	guuid "github.com/google/uuid"
)

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

//Type retrurns region type
func (r *Region) Type() string {
	ndr := (*C.struct_ndctl_region)(r)
	return C.GoString(C.ndctl_region_get_type_name(ndr))
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

//ActiveNamespaces returns all active namespaces in the region
func (r *Region) ActiveNamespaces() []*Namespace {
	return r.namespaces(true)
}

//AllNamespaces returns all namespaces in the region
func (r *Region) AllNamespaces() []*Namespace {
	return r.namespaces(false)
}

//Bus get assosiated bus
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

func (r *Region) CreateNamespace(opts CreateNamespaceOpts) (*Namespace, error) {
	var ndns *C.struct_ndctl_namespace

	copts := opts.toCOptions()
	ndr := (*C.struct_ndctl_region)(r)
	rc := C.ndctl_region_create_namespace(ndr, copts, &ndns)
	if rc != 0 {
		return nil, fmt.Errorf("failed to create namespace: %s(%d)",
			C.GoString(C.strerror(-rc)), -rc)
	}

	return (*Namespace)(ndns), nil
}

//DestroyNamespace destroys the given namespace ns in the region
func (r *Region) DestroyNamespace(ns *Namespace, force bool) error {
	ndr := (*C.struct_ndctl_region)(r)
	ndns := (*C.struct_ndctl_namespace)(ns)
	if ndns == nil {
		return fmt.Errorf("null namespace")
	}
	rc := C.ndctl_region_destroy_namespace(ndr, ndns, C.bool(force))
	if int(rc) != 0 {
		return fmt.Errorf("%s", C.GoString(C.strerror(-rc)))
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
	if dax := C.ndctl_namespace_get_dax(ndns); dax != nil {
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
	cmode := C.ndctl_namespace_get_mode(ndns)

	switch cmode {
	case C.NDCTL_NS_MODE_MEMORY:
		if pfn := C.ndctl_namespace_get_pfn(ndns); pfn != nil {
			size = C.ndctl_pfn_get_size(pfn)
		} else {
			size = C.ndctl_namespace_get_size(ndns)
		}
	case C.NDCTL_NS_MODE_DAX:
		if dax := C.ndctl_namespace_get_dax(ndns); dax != nil {
			size = C.ndctl_dax_get_size(dax)
		}
	case C.NDCTL_NS_MODE_SAFE:
		if btt := C.ndctl_namespace_get_btt(ndns); btt != nil {
			size = C.ndctl_btt_get_size(btt)
		}
	case C.NDCTL_NS_MODE_RAW:
		size = C.ndctl_namespace_get_size(ndns)
	}

	return uint64(size)
}

//Mode returns namespace mode
func (ns *Namespace) Mode() NamespaceMode {
	ndns := (*C.struct_ndctl_namespace)(ns)
	return toNamespaceMode(C.ndctl_namespace_get_mode(ndns))
}

//Type returns namespace type
func (ns *Namespace) Type() NamespaceType {
	ndns := (*C.struct_ndctl_namespace)(ns)
	return NamespaceType(C.ndctl_namespace_get_type(ndns))
}

//Enabled return if namespace is enabled
func (ns *Namespace) Enabled() bool {
	ndns := (*C.struct_ndctl_namespace)(ns)
	return (C.ndctl_namespace_is_enabled(ndns) == 1)
}

//UUID returns uuid of the namespace
func (ns *Namespace) UUID() guuid.UUID {
	ndns := (*C.struct_ndctl_namespace)(ns)
	var cuid C.uuid_t

	btt := C.ndctl_namespace_get_btt(ndns)
	dax := C.ndctl_namespace_get_dax(ndns)
	pfn := C.ndctl_namespace_get_pfn(ndns)

	if btt != nil {
		C.ndctl_btt_get_uuid(btt, &cuid[0])
	} else if pfn != nil {
		C.ndctl_pfn_get_uuid(pfn, &cuid[0])
	} else if dax != nil {
		C.ndctl_dax_get_uuid(dax, &cuid[0])
	} else if C.ndctl_namespace_get_type(ndns) != C.ND_DEVICE_NAMESPACE_IO {
		C.ndctl_namespace_get_uuid(ndns, &cuid[0])
	}

	uidbytes := C.GoBytes(unsafe.Pointer(&cuid[0]), C.sizeof_uuid_t)
	_uuid, err := guuid.FromBytes(uidbytes)
	if err != nil {
		fmt.Printf("WARN: worng uuid: %s", err.Error())
		return guuid.UUID{}
	}

	return _uuid
}

//Location returns namespace mapping location
func (ns *Namespace) Location() string {
	ndns := (*C.struct_ndctl_namespace)(ns)
	locations := map[uint32]string{
		C.NDCTL_PFN_LOC_NONE: "none",
		C.NDCTL_PFN_LOC_RAM:  "mem",
		C.NDCTL_PFN_LOC_PMEM: "dev",
	}
	mode := C.ndctl_namespace_get_mode(ndns)

	switch mode {
	case C.NDCTL_NS_MODE_MEMORY:
		if pfn := C.ndctl_namespace_get_pfn(ndns); pfn != nil {
			return locations[C.ndctl_pfn_get_location(pfn)]
		} else {
			return locations[C.NDCTL_PFN_LOC_RAM]
		}
	case C.NDCTL_NS_MODE_DAX:
		if dax := C.ndctl_namespace_get_dax(ndns); dax != nil {
			return locations[C.ndctl_dax_get_location(dax)]
		}
	}

	return locations[C.NDCTL_PFN_LOC_NONE]
}

//Region returns referenct to Region that this namespace is part of
func (ns *Namespace) Region() *Region {
	ndns := (*C.struct_ndctl_namespace)(ns)
	return (*Region)(C.ndctl_namespace_get_region(ndns))

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

	if mode := ns.Mode(); mode != DAX {
		props["blockdev"] = ns.BlockDeviceName()
	}

	if location := ns.Location(); location != "none" {
		props["map"] = location
	}

	return json.Marshal(props)
}

//NamespaceMode represetns mode of the namespace
type NamespaceMode string

const (
	FSDAX   NamespaceMode = "fsdax"
	SAFE    NamespaceMode = "safe"
	RAW     NamespaceMode = "raw"
	DAX     NamespaceMode = "dax"
	UNKNOWN NamespaceMode = "unknown"
)

func toNamespaceMode(mode C.enum_ndctl_namespace_mode) NamespaceMode {
	switch mode {
	case C.NDCTL_NS_MODE_FSDAX:
		return FSDAX
	case C.NDCTL_NS_MODE_SAFE:
		return SAFE
	case C.NDCTL_NS_MODE_RAW:
		return RAW
	case C.NDCTL_NS_MODE_DAX:
		return DAX
	}
	return UNKNOWN
}

func (nsmode NamespaceMode) toCMode() C.enum_ndctl_namespace_mode {
	switch nsmode {
	case FSDAX:
		return C.NDCTL_NS_MODE_FSDAX
	case SAFE:
		return C.NDCTL_NS_MODE_SAFE
	case RAW:
		return C.NDCTL_NS_MODE_RAW
	case DAX:
		return C.NDCTL_NS_MODE_DAX
	}

	return C.NDCTL_NS_MODE_UNKNOWN
}

//NamespaceType type to represent namespace type
type NamespaceType string

const (
	//Pmem pmem type namespace
	Pmem NamespaceType = "pmem"
	//Block block type namespace
	Block NamespaceType = "blk"
)

func (t NamespaceType) toCType() C.enum_ndctl_namespace_type {
	if t == Pmem {
		return C.NDCTL_NS_TYPE_PMEM
	} else if t == Block {
		return C.NDCTL_NS_TYPE_BLK
	}

	return C.NDCTL_NS_TYPE_UNKNOWN
}

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

//Region get assosiated Region
func (m *Mapping) Region() *Region {
	ndm := (*C.struct_ndctl_mapping)(m)
	return (*Region)(C.ndctl_mapping_get_region(ndm))
}

//Dimm get assosiated Dimm
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

// Context go wrapper for ndctl context
type Context C.struct_ndctl_ctx

// NewContext Initializes new context
func NewContext() (*Context, error) {
	var ctx *C.struct_ndctl_ctx
	var rc C.int

	if rc = C.ndctl_new(&ctx); rc != 0 {
		return nil, fmt.Errorf("Create context failed with error: %d", rc)
	}

	return (*Context)(ctx), nil
}

// Free destroy context
func (ctx *Context) Free() {
	if ctx != nil {
		C.ndctl_unref((*C.struct_ndctl_ctx)(ctx))
	}
}

// GetBuses returns available buses
func (ctx *Context) GetBuses() []*Bus {
	var buses []*Bus
	ndctx := (*C.struct_ndctl_ctx)(ctx)

	for ndbus := C.ndctl_bus_get_first(ndctx); ndbus != nil; ndbus = C.ndctl_bus_get_next(ndbus) {
		buses = append(buses, (*Bus)(ndbus))
	}
	return buses
}

//CreateNamespace create new namespace with given opts
func (ctx *Context) CreateNamespace(opts CreateNamespaceOpts) (*Namespace, error) {
	var ndns *C.struct_ndctl_namespace

	copts := opts.toCOptions()

	rc := C.ndctl_context_create_namespace((*C.struct_ndctl_ctx)(ctx),
		copts, &ndns)
	if rc != 0 {
		return nil, fmt.Errorf("failed to create namespace: %s(%d)",
			C.GoString(C.strerror(-rc)), -rc)
	}

	return (*Namespace)(ndns), nil
}

//DestroyNamespaceByName deletes namespace with given name
func (ctx *Context) DestroyNamespaceByName(name string) error {
	ns, err := ctx.GetNamespaceByName(name)
	if err != nil {
		return err
	}

	r := ns.Region()
	return r.DestroyNamespace(ns, true)
}

//GetNamespaceByName gets namespace details for given name
func (ctx *Context) GetNamespaceByName(name string) (*Namespace, error) {
	for _, bus := range ctx.GetBuses() {
		for _, r := range bus.AllRegions() {
			for _, ns := range r.AllNamespaces() {
				if ns.Name() == name {
					return ns, nil
				}
			}
		}
	}
	return nil, fmt.Errorf("Not found")
}

//GetActiveNamespaces returns list of all active namespaces in all regions
func (ctx *Context) GetActiveNamespaces() []*Namespace {
	var list []*Namespace
	for _, bus := range ctx.GetBuses() {
		for _, r := range bus.ActiveRegions() {
			nss := r.ActiveNamespaces()
			list = append(list, nss...)
		}
	}

	return list
}

//GetAllNamespaces returns list of all namespaces in all regions including idle namespaces
func (ctx *Context) GetAllNamespaces() []*Namespace {
	var list []*Namespace
	for _, bus := range ctx.GetBuses() {
		for _, r := range bus.AllRegions() {
			nss := r.AllNamespaces()
			list = append(list, nss...)
		}
	}

	return list
}

//IsSpaceAvailable checks if a region available with given free size
func (ctx *Context) IsSpaceAvailable(size uint64) bool {
	for _, bus := range ctx.GetBuses() {
		for _, r := range bus.ActiveRegions() {
			if r.MaxAvailableExtent() >= size && NamespaceType(r.Type()) == Pmem {
				return true
			}
		}
	}

	return false
}

type MapLocation string

func (m MapLocation) toCMapLocation() *C.char {
	return C.CString(string(m))
}

const (
	MemoryMap MapLocation = "map"
	BlockMap  MapLocation = "blk"
)

//CreateNamespaceOpts options to create a namespace
type CreateNamespaceOpts struct {
	Name       string
	Size       uint64
	SectorSize uint64
	Align      uint32
	Type       NamespaceType
	Mode       NamespaceMode
	Location   MapLocation
}

func (opts CreateNamespaceOpts) toCOptions() *C.struct_ndctl_namespace_create_opts {
	copts := C.struct_ndctl_namespace_create_opts{}

	if opts.Name != "" {
		name := C.CString(opts.Name)
		defer C.free(unsafe.Pointer(name))
		C.ndctl_namespace_options_set_name(&copts, name)
	}
	if opts.Type != "" {
		copts.ns_type = opts.Type.toCType()
	}
	if opts.Mode != "" {
		copts.mode = opts.Mode.toCMode()
	}
	if opts.Location != "" {
		copts.map_location = opts.Location.toCMapLocation()
	}
	if opts.Size != 0 {
		copts.size = C.ulonglong(opts.Size)
	}
	if opts.SectorSize != 0 {
		copts.sector_size = C.ulong(opts.SectorSize)
	}
	if opts.Align != 0 {
		copts.align = C.ulong(opts.Align)
	}

	return &copts
}
