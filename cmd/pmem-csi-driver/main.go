/*
Copyright 2017 The Kubernetes Authors.
Copyright 2018 Intel Coporation.

SPDX-License-Identifier: Apache-2.0
*/

package main

import (
	"flag"
	"fmt"
	"os"

	"github.com/intel/pmem-csi/pkg/pmem-common"
	"github.com/intel/pmem-csi/pkg/pmem-csi-driver"
)

func init() {
	flag.Set("logtostderr", "true")
}

var (
	endpoint   = flag.String("endpoint", "unix:///tmp/pmem-csi.sock", "PMEM CSI endpoint")
	driverName = flag.String("drivername", "pmem-csi-driver", "name of the driver")
	nodeID     = flag.String("nodeid", "nodeid", "node id")
	namespacesize  = flag.Int("namespacesize", 32, "NVDIMM namespace size in GB")
)

func main() {
	flag.Parse()
	closer, err := pmemcommon.InitTracer(*driverName)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to initialize tracer: %s\n", err)
		os.Exit(1)
	}
	defer closer.Close()

	driver := pmemcsidriver.GetPMEMDriver()
	driver.Run(*driverName, *nodeID, *endpoint, *namespacesize)
}
