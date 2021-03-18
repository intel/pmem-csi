/*
Copyright 2021 Intel Corporation.

SPDX-License-Identifier: Apache-2.0
*/
package fake

import (
	"github.com/intel/pmem-csi/pkg/ndctl"
)

type Mapping struct {
	Offset_   uint64
	Length_   uint64
	Position_ int

	Region_ ndctl.Region
	Dimm_   ndctl.Dimm
}

var _ ndctl.Mapping = &Mapping{}

func (m *Mapping) Offset() uint64 {
	return m.Offset_
}

func (m *Mapping) Length() uint64 {
	return m.Length_
}

func (m *Mapping) Position() int {
	return m.Position_
}

func (m *Mapping) Region() ndctl.Region {
	return m.Region_
}

func (m *Mapping) Dimm() ndctl.Dimm {
	return m.Dimm_
}
