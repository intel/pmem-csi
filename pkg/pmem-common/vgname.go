/*
Copyright 2020 Intel Coporation.

SPDX-License-Identifier: Apache-2.0
*/

package pmemcommon

import (
	"github.com/intel/pmem-csi/pkg/ndctl"
)

func VgName(bus *ndctl.Bus, region *ndctl.Region) string {
	// Hard-coded string to indicate all namespaces are in "FSDAX" mode.
	nsmode := "fsdax"
	// This is present to avoid API break: names used to indicate nsmode
	// before the sector-mode support was dropped.
	return bus.DeviceName() + region.DeviceName() + nsmode
}
