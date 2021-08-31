/*
Copyright 2017 The Kubernetes Authors.
Copyright 2018 Intel Coporation.

SPDX-License-Identifier: Apache-2.0
*/

package main

import (
	"os"

	pmemcsidriver "github.com/intel/pmem-csi/pkg/pmem-csi-driver"
)

func main() {
	os.Exit(pmemcsidriver.Main())
}
