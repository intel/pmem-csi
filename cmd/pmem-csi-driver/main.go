/*
Copyright 2017 The Kubernetes Authors.
Copyright 2018 Intel Coporation.

SPDX-License-Identifier: Apache-2.0
*/

package main

import (
	"flag"
	"os"

	"k8s.io/klog/v2"

	"github.com/intel/pmem-csi/pkg/pmem-csi-driver"
)

func init() {
	klog.InitFlags(nil)
}

func main() {
	flag.Parse()
	os.Exit(pmemcsidriver.Main())
}
