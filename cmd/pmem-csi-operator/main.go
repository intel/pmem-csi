/*
Copyright 2020 The Kubernetes Authors.

SPDX-License-Identifier: Apache-2.0
*/

package main

import (
	"flag"
	"os"

	pmemoperator "github.com/intel/pmem-csi/pkg/pmem-csi-operator"
)

func main() {
	flag.Parse()
	os.Exit(pmemoperator.Main())
}
