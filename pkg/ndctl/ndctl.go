package ndctl

//#cgo pkg-config: libndctl
//#include <string.h>
//#define ARRAY_SIZE(a) (sizeof(a) / sizeof((a)[0]))
//#include <ndctl/libndctl.h>
//#include <ndctl/ndctl.h>
import "C"

import (
	"fmt"
	"github.com/pkg/errors"
	"k8s.io/klog/glog"
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
	Align      uint32
	Type       NamespaceType
	Mode       NamespaceMode
	Location   MapLocation
}

// Context go wrapper for ndctl context
type Context C.struct_ndctl_ctx

// NewContext Initializes new context
func NewContext() (*Context, error) {
	var ctx *C.struct_ndctl_ctx

	if rc := C.ndctl_new(&ctx); rc != 0 {
		return nil, fmt.Errorf("Create context failed with error: %s", cErrorString(rc))
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
	var err error
	var ns *Namespace
	for _, bus := range ctx.GetBuses() {
		for _, r := range bus.ActiveRegions() {
			if ns, err = r.CreateNamespace(opts); err == nil {
				glog.Infof("Namespace %s created in %s", ns.Name(), r.DeviceName())
				return ns, nil
			} else {
				glog.Errorf("Namespace creation failure in %s: %s", r.DeviceName(), err.Error())
			}
		}
	}
	return nil, errors.Wrap(err, "failed to create namespace")
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
