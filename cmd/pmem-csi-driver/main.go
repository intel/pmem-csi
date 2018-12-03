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

	"k8s.io/klog"

	"github.com/intel/csi-pmem/pkg/pmem-csi-driver"
)

var (
	/* generic options */
	driverName       = flag.String("drivername", "csi-pmem", "name of the driver")
	nodeID           = flag.String("nodeid", "nodeid", "node id")
	endpoint         = flag.String("endpoint", "unix:///tmp/csi-pmem.sock", "PMEM CSI endpoint")
	mode             = flag.String("mode", "unified", "driver run mode : controller, node or unified")
	registryEndpoint = flag.String("registryEndpoint", "", "endpoint to connect/listen resgistery server")
	/* node mode options */
	controllerEndpoint = flag.String("controllerEndpoint", "", "internal node controller endpoint")
	deviceManager      = flag.String("deviceManager", "lvm", "device manager to use to manage pmem devices. supported types: 'lvm' or 'ndctl'")
)

func main() {
	klog.InitFlags(nil)
	flag.Set("logtostderr", "true")
	flag.Parse()

	driver, err := pmemcsidriver.GetPMEMDriver(pmemcsidriver.Config{
		DriverName:         *driverName,
		NodeID:             *nodeID,
		Endpoint:           *endpoint,
		Mode:               pmemcsidriver.DriverMode(*mode),
		RegistryEndpoint:   *registryEndpoint,
		ControllerEndpoint: *controllerEndpoint,
		DeviceManager:      *deviceManager,
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
