/*
Copyright 2020 The Kubernetes Authors.

SPDX-License-Identifier: Apache-2.0
*/

package main

import (
	"os"

	pmemoperator "github.com/intel/pmem-csi/pkg/pmem-csi-operator"
)

func main() {
	os.Exit(pmemoperator.Main())
}
