/*
Copyright 2020 Intel Coporation.

SPDX-License-Identifier: Apache-2.0
*/

package pmemcommon

import (
	"github.com/intel/pmem-csi/pkg/ndctl"
)

func VgName(bus *ndctl.Bus, region *ndctl.Region) string {
	return bus.DeviceName() + region.DeviceName()
}
