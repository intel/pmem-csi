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
	/* generic options */
	driverName       = flag.String("drivername", "csi-pmem", "name of the driver")
	nodeID           = flag.String("nodeid", "nodeid", "node id")
	endpoint         = flag.String("endpoint", "unix:///tmp/pmem-csi.sock", "PMEM CSI endpoint")
	mode             = flag.String("mode", "unified", "driver run mode : controller, node or unified")
	registryEndpoint = flag.String("registryEndpoint", "", "endpoint to connect/listen resgistery server")
	/* node mode options */
	controllerEndpoint = flag.String("controllerEndpoint", "", "internal node controller endpoint")
	namespacesize      = flag.Int("namespacesize", 32, "NVDIMM namespace size in GB")
)

func main() {
	flag.Parse()
	closer, err := pmemcommon.InitTracer(*driverName)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to initialize tracer: %s\n", err)
		os.Exit(1)
	}
	defer closer.Close()

	driver, err := pmemcsidriver.GetPMEMDriver(pmemcsidriver.Config{
		DriverName:         *driverName,
		NodeID:             *nodeID,
		Endpoint:           *endpoint,
		Mode:               pmemcsidriver.DriverMode(*mode),
		RegistryEndpoint:   *registryEndpoint,
		ControllerEndpoint: *controllerEndpoint,
		NamespaceSize:      *namespacesize,
	})
	if err != nil {
		fmt.Printf("Failed to Initialized driver: %s", err.Error())
		os.Exit(1)
	}

	if err = driver.Run(); err != nil {
		fmt.Printf("Failed to run driver: %s", err.Error())
		os.Exit(1)
	}
}
