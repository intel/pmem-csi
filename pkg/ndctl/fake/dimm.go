/*
Copyright 2021 Intel Corporation.

SPDX-License-Identifier: Apache-2.0
*/

package fake

import (
	"github.com/intel/pmem-csi/pkg/ndctl"
)

type Dimm struct {
	Enabled_    bool
	Active_     bool
	ID_         string
	PhysicalID_ int
	DeviceName_ string
	Handle_     int16
}

var _ ndctl.Dimm = &Dimm{}

func (d *Dimm) Enabled() bool {
	return d.Enabled_
}

func (d *Dimm) Active() bool {
	return d.Active_
}

func (d *Dimm) ID() string {
	return d.ID_
}

func (d *Dimm) PhysicalID() int {
	return d.PhysicalID_
}

func (d *Dimm) DeviceName() string {
	return d.DeviceName_
}

func (d *Dimm) Handle() int16 {
	return d.Handle_
}
