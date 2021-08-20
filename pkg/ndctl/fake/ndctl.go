/*
Copyright 2021 Intel Corporation.

SPDX-License-Identifier: Apache-2.0
*/

package fake

import (
	"github.com/intel/pmem-csi/pkg/ndctl"
)

const (
	kib  uint64 = 1024
	kib4 uint64 = kib * 4
	mib  uint64 = kib * 1024
	mib2 uint64 = mib * 2
)

type Context struct {
	Buses []ndctl.Bus
}

var _ ndctl.Context = &Context{}

// NewContext initializes the cross-references between
// items.
func NewContext(ctx *Context) *Context {
	for _, bus := range ctx.Buses {
		bus, ok := bus.(*Bus)
		if !ok {
			continue
		}
		for _, region := range bus.Regions_ {
			region, ok := region.(*Region)
			if !ok {
				continue
			}
			for _, mapping := range region.Mappings_ {
				if mapping, ok := mapping.(*Mapping); ok {
					mapping.Region_ = region
				}
			}
			for _, namespace := range region.Namespaces_ {
				if namespace, ok := namespace.(*Namespace); ok {
					namespace.Region_ = region
				}
			}
			region.Bus_ = bus
		}
	}

	return ctx
}

func (ctx *Context) Free() {
}

func (ctx *Context) GetBuses() []ndctl.Bus {
	return ctx.Buses
}
