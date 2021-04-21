/*
Copyright 2021 Intel Corporation.

SPDX-License-Identifier: Apache-2.0
*/

package fake

import (
	"fmt"

	"github.com/google/uuid"
	pmemerr "github.com/intel/pmem-csi/pkg/errors"
	"github.com/intel/pmem-csi/pkg/ndctl"
)

type Region struct {
	ID_                 uint
	DeviceName_         string
	Size_               uint64
	AvailableSize_      uint64
	MaxAvailableExtent_ uint64
	Type_               ndctl.RegionType
	TypeName_           string
	Enabled_            bool
	Readonly_           bool
	InterleaveWays_     uint64
	RegionAlign_        uint64

	Mappings_   []ndctl.Mapping
	Namespaces_ []ndctl.Namespace
	Bus_        ndctl.Bus
}

var _ ndctl.Region = &Region{}

func (r *Region) ID() uint {
	return r.ID_
}

func (r *Region) DeviceName() string {
	return r.DeviceName_
}

func (r *Region) Size() uint64 {
	return r.Size_
}

func (r *Region) AvailableSize() uint64 {
	return r.AvailableSize_
}

func (r *Region) MaxAvailableExtent() uint64 {
	return r.MaxAvailableExtent_
}

func (r *Region) Type() ndctl.RegionType {
	return r.Type_
}

func (r *Region) TypeName() string {
	return r.TypeName_
}

func (r *Region) Enabled() bool {
	return r.Enabled_
}

func (r *Region) Readonly() bool {
	return r.Readonly_
}

func (r *Region) InterleaveWays() uint64 {
	return r.InterleaveWays_
}

func (r *Region) ActiveNamespaces() []ndctl.Namespace {
	var namespaces []ndctl.Namespace
	for _, namespace := range r.Namespaces_ {
		if namespace.Enabled() {
			namespaces = append(namespaces, namespace)
		}
	}
	return namespaces
}

func (r *Region) AllNamespaces() []ndctl.Namespace {
	var namespaces []ndctl.Namespace
	for _, namespace := range r.Namespaces_ {
		if namespace.Size() > 0 {
			namespaces = append(namespaces, namespace)
		}
	}
	return namespaces
}

func (r *Region) Bus() ndctl.Bus {
	return r.Bus_
}

func (r *Region) Mappings() []ndctl.Mapping {
	return r.Mappings_
}

func (r *Region) SeedNamespace() ndctl.Namespace {
	return nil
}

func (r *Region) GetAlign() uint64 {
	return r.RegionAlign_
}

func (r *Region) CreateNamespace(opts ndctl.CreateNamespaceOpts) (ndctl.Namespace, error) {
	var err error
	/* Set defaults */
	if opts.Type == "" {
		opts.Type = ndctl.PmemNamespace
	}
	if opts.Mode == "" {
		if opts.Type == ndctl.PmemNamespace {
			opts.Mode = ndctl.FsdaxMode // == MemoryMode
		} else {
			opts.Mode = ndctl.SectorMode
		}
	}
	if opts.Location == "" {
		opts.Location = ndctl.DeviceMap
	}

	if opts.SectorSize == 0 {
		if opts.Type == ndctl.BlockNamespace || opts.Mode == ndctl.SectorMode {
			// default sector size for blk-type or safe-mode
			opts.SectorSize = kib4
		}
	}

	/* Sanity checks */

	if !r.Enabled() {
		return nil, fmt.Errorf("Region not enabled")
	}
	if r.Readonly() {
		return nil, fmt.Errorf("Cannot create namspace in readonly region")
	}

	if r.Type() == ndctl.BlockRegion {
		if opts.Mode == ndctl.FsdaxMode || opts.Mode == ndctl.DaxMode {
			return nil, fmt.Errorf("Block regions does not support %s mode namespace", opts.Mode)
		}
	}

	if opts.Size != 0 {
		available := r.MaxAvailableExtent()
		if opts.Size > available {
			return nil, fmt.Errorf("create namespace with size %v: %w", opts.Size, pmemerr.NotEnoughSpace)
		}
		align := mib2
		if opts.Size%align != 0 {
			// Round up size to align with next block boundary.
			opts.Size = (opts.Size/align + 1) * align
		}
	}

	/* setup_namespace */

	ns := &Namespace{}

	if ns.Type() != ndctl.IoNamespace {
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
		err = ns.SetSectorSize(opts.SectorSize)
	}
	if err == nil {
		err = ns.SetEnforceMode(opts.Mode)
	}

	if err == nil {
		err = ns.Enable()
	}

	if err != nil {
		return nil, err
	}

	r.Namespaces_ = append(r.Namespaces_, ns)
	return ns, nil
}

func (r *Region) DestroyNamespace(ns ndctl.Namespace, force bool) error {
	for i := range r.Namespaces_ {
		if r.Namespaces_[i] == ns {
			r.Namespaces_ = append(r.Namespaces_[:i], r.Namespaces_[i+1:]...)
			return nil
		}
	}

	return fmt.Errorf("namespace %v not", ns)
}

func (r *Region) AdaptAlign(align uint64) (uint64, error) {
	return align, nil
}

func (r *Region) FsdaxAlignment() (uint64, error) {
	return mib2, nil
}

func (r *Region) namespaces(onlyActive bool) []ndctl.Namespace {
	var namespaces []ndctl.Namespace
	for _, ns := range r.Namespaces_ {
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
