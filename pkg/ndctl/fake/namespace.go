/*
Copyright 2021 Intel Corporation.

SPDX-License-Identifier: Apache-2.0
*/

package fake

import (
	"github.com/google/uuid"
	"github.com/intel/pmem-csi/pkg/ndctl"
)

type Namespace struct {
	ID_              uint
	Name_            string
	DeviceName_      string
	BlockDeviceName_ string
	Size_            uint64
	Overhead_        uint64
	Mode_            ndctl.NamespaceMode
	Type_            ndctl.NamespaceType
	Enabled_         bool
	Active_          bool
	UUID_            uuid.UUID
	Location_        ndctl.MapLocation

	Region_ ndctl.Region
}

var _ ndctl.Namespace = &Namespace{}

func (ns *Namespace) ID() uint {
	return ns.ID_
}

func (ns *Namespace) Name() string {
	return ns.Name_
}

func (ns *Namespace) DeviceName() string {
	return ns.DeviceName_
}

func (ns *Namespace) BlockDeviceName() string {
	return ns.BlockDeviceName_
}

func (ns *Namespace) Size() uint64 {
	return ns.Size_
}

func (ns *Namespace) RawSize() uint64 {
	return ns.Size_ + ns.Overhead_
}

func (ns *Namespace) Mode() ndctl.NamespaceMode {
	return ns.Mode_
}

func (ns *Namespace) Type() ndctl.NamespaceType {
	return ns.Type_
}

func (ns *Namespace) Enabled() bool {
	return ns.Enabled_
}

func (ns *Namespace) Active() bool {
	return ns.Active_
}

func (ns *Namespace) UUID() uuid.UUID {
	return ns.UUID_
}

func (ns *Namespace) Location() ndctl.MapLocation {
	return ns.Location_
}

func (ns *Namespace) Region() ndctl.Region {
	return ns.Region_
}

func (ns *Namespace) SetAltName(name string) error {
	return nil
}

func (ns *Namespace) SetSize(size uint64) error {
	ns.Size_ = size
	return nil
}

func (ns *Namespace) SetUUID(uid uuid.UUID) error {
	ns.UUID_ = uid
	return nil
}

func (ns *Namespace) SetSectorSize(sectorSize uint64) error {
	return nil
}

func (ns *Namespace) SetEnforceMode(mode ndctl.NamespaceMode) error {
	ns.Mode_ = mode
	return nil
}

func (ns *Namespace) Enable() error {
	ns.Enabled_ = true
	return nil
}

func (ns *Namespace) Disable() error {
	ns.Enabled_ = false
	return nil
}

func (ns *Namespace) SetRawMode(raw bool) error {
	return nil
}

func (ns *Namespace) SetPfnSeed(loc ndctl.MapLocation, align uint64) error {
	return nil
}
