/*
Copyright 2021 Intel Corporation.

SPDX-License-Identifier: Apache-2.0
*/

package fake

import (
	"github.com/intel/pmem-csi/pkg/ndctl"
)

type Bus struct {
	Provider_   string
	DeviceName_ string
	Dimms_      []ndctl.Dimm
	Regions_    []ndctl.Region
}

var _ ndctl.Bus = &Bus{}

func (b *Bus) Provider() string {
	return b.Provider_
}

func (b *Bus) DeviceName() string {
	return b.DeviceName_
}

func (b *Bus) Dimms() []ndctl.Dimm {
	return b.Dimms_
}

func (b *Bus) ActiveRegions() []ndctl.Region {
	return b.regions(true)
}

func (b *Bus) AllRegions() []ndctl.Region {
	return b.regions(false)
}

func (b *Bus) GetRegionByPhysicalAddress(address uint64) ndctl.Region {
	return nil
}

func (b *Bus) regions(onlyActive bool) []ndctl.Region {
	var regions []ndctl.Region
	for _, region := range b.Regions_ {
		if !onlyActive || region.Enabled() {
			regions = append(regions, region)
		}
	}
	return regions
}
