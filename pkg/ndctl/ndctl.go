package ndctl

//#cgo pkg-config: libndctl
//#include <string.h>
//#define ARRAY_SIZE(a) (sizeof(a) / sizeof((a)[0]))
//#include <ndctl/libndctl.h>
//#include <ndctl/ndctl.h>
import "C"

import (
	"fmt"

	"k8s.io/klog/v2"

	pmemerr "github.com/intel/pmem-csi/pkg/errors"
)

const (
	kib  uint64 = 1024
	kib4 uint64 = kib * 4
	mib  uint64 = kib * 1024
	mib2 uint64 = mib * 2
	gib  uint64 = mib * 1024
	tib  uint64 = gib * 1024
)

//CreateNamespaceOpts options to create a namespace
type CreateNamespaceOpts struct {
	Name       string
	Size       uint64
	SectorSize uint64
	Type       NamespaceType
	Mode       NamespaceMode
	Location   MapLocation
}

// Context is a go wrapper for ndctl context
type Context interface {
	// Free destroys the context.
	Free()
	// GetBuses returns all available buses.
	GetBuses() []Bus
}

type context = C.struct_ndctl_ctx

var _ Context = &context{}

// NewContext Initializes new context
func NewContext() (Context, error) {
	var ctx *context

	if rc := C.ndctl_new(&ctx); rc != 0 {
		return nil, fmt.Errorf("Create context failed with error: %s", cErrorString(rc))
	}

	return ctx, nil
}

func (ctx *context) Free() {
	if ctx != nil {
		C.ndctl_unref((*C.struct_ndctl_ctx)(ctx))
	}
}

func (ctx *context) GetBuses() []Bus {
	var buses []Bus

	for ndbus := C.ndctl_bus_get_first(ctx); ndbus != nil; ndbus = C.ndctl_bus_get_next(ndbus) {
		buses = append(buses, ndbus)
	}
	return buses
}

// CreateNamespace creates a new namespace with given opts in some arbitrary
// region. It returns an error if creation fails in all regions.
func CreateNamespace(ctx Context, opts CreateNamespaceOpts) (Namespace, error) {
	var err error
	var ns Namespace
	for _, bus := range ctx.GetBuses() {
		for _, r := range bus.ActiveRegions() {
			if ns, err = r.CreateNamespace(opts); err == nil {
				klog.V(3).Infof("Namespace %s created in %s", ns.Name(), r.DeviceName())
				return ns, nil
			} else {
				klog.Errorf("Namespace creation failure in %s: %s", r.DeviceName(), err.Error())
			}
		}
	}
	return nil, err
}

// DestroyNamespaceByName deletes the namespace with the given name.
func DestroyNamespaceByName(ctx Context, name string) error {
	ns, err := GetNamespaceByName(ctx, name)
	if err != nil {
		return err
	}

	r := ns.Region()
	return r.DestroyNamespace(ns, true)
}

// GetNamespaceByName gets the namespace details for a given name.
func GetNamespaceByName(ctx Context, name string) (Namespace, error) {
	for _, bus := range ctx.GetBuses() {
		for _, r := range bus.AllRegions() {
			for _, ns := range r.AllNamespaces() {
				if ns.Name() == name {
					return ns, nil
				}
			}
		}
	}
	return nil, pmemerr.DeviceNotFound
}

// GetActiveNamespaces returns a list of all active namespaces in all regions.
func GetActiveNamespaces(ctx Context) []Namespace {
	var list []Namespace
	for _, bus := range ctx.GetBuses() {
		for _, r := range bus.ActiveRegions() {
			nss := r.ActiveNamespaces()
			list = append(list, nss...)
		}
	}

	return list
}

// GetAllNamespaces returns a list of all namespaces in all regions including idle namespaces.
func GetAllNamespaces(ctx Context) []Namespace {
	var list []Namespace
	for _, bus := range ctx.GetBuses() {
		for _, r := range bus.AllRegions() {
			nss := r.AllNamespaces()
			list = append(list, nss...)
		}
	}

	return list
}

// IsSpaceAvailable checks if a region is available with given free size.
func IsSpaceAvailable(ctx Context, size uint64) bool {
	for _, bus := range ctx.GetBuses() {
		for _, r := range bus.ActiveRegions() {
			if r.MaxAvailableExtent() >= size && NamespaceType(r.Type()) == PmemNamespace {
				return true
			}
		}
	}

	return false
}

func cErrorString(errno C.int) string {
	if errno < 0 {
		errno = -errno
	}
	return C.GoString(C.strerror(errno))
}
