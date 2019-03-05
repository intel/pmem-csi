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
	"k8s.io/klog/glog"

	pmemcsidriver "github.com/intel/pmem-csi/pkg/pmem-csi-driver"
)

var (
	/* generic options */
	driverName       = flag.String("drivername", "pmem-csi", "name of the driver")
	nodeID           = flag.String("nodeid", "nodeid", "node id")
	endpoint         = flag.String("endpoint", "unix:///tmp/pmem-csi.sock", "PMEM CSI endpoint")
	mode             = flag.String("mode", "unified", "driver run mode : controller, node or unified")
	registryEndpoint = flag.String("registryEndpoint", "", "endpoint to connect/listen registry server")
	caFile           = flag.String("caFile", "", "Root CA certificate file to use for verifying connections")
	certFile         = flag.String("certFile", "", "SSL certificate file to use for authenticating client connections(RegistryServer/NodeControllerServer)")
	keyFile          = flag.String("keyFile", "", "Private key file associated to certificate")
	clientCertFile   = flag.String("clientCertFile", "", "Client SSL certificate file to use for authenticating peer connections, defaults to 'certFile'")
	clientKeyFile    = flag.String("clientKeyFile", "", "Client private key associated to client certificate, defaults to 'keyFile'")
	/* Node mode options */
	controllerEndpoint = flag.String("controllerEndpoint", "", "internal node controller endpoint")
	deviceManager      = flag.String("deviceManager", "lvm", "device manager to use to manage pmem devices. supported types: 'lvm' or 'ndctl'")
	showVersion        = flag.Bool("version", false, "Show release version and exit")

	version = "unknown" // Set version during build time
)

func main() {
	klog.InitFlags(nil)
	flag.Set("logtostderr", "true")
	flag.Parse()

	if *showVersion {
		fmt.Println(version)
		os.Exit(0)
	}

	glog.Info("Version: ", version)

	driver, err := pmemcsidriver.GetPMEMDriver(pmemcsidriver.Config{
		DriverName:         *driverName,
		NodeID:             *nodeID,
		Endpoint:           *endpoint,
		Mode:               pmemcsidriver.DriverMode(*mode),
		RegistryEndpoint:   *registryEndpoint,
		CAFile:             *caFile,
		CertFile:           *certFile,
		KeyFile:            *keyFile,
		ClientCertFile:     *clientCertFile,
		ClientKeyFile:      *clientKeyFile,
		ControllerEndpoint: *controllerEndpoint,
		DeviceManager:      *deviceManager,
		Version:            version,
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
