/*
Copyright 2017 The Kubernetes Authors.
Copyright 2018 Intel Coporation.

SPDX-License-Identifier: Apache-2.0
*/

package main

import (
	"flag"
	"fmt"
	"io/ioutil"
	"os"

	"k8s.io/klog"
	"k8s.io/klog/glog"

	pmemcsidriver "github.com/intel/pmem-csi/pkg/pmem-csi-driver"
)

var (
	/* generic options */
	driverName       = flag.String("drivername", "pmem-csi.intel.com", "name of the driver")
	nodeID           = flag.String("nodeid", "nodeid", "node id")
	endpoint         = flag.String("endpoint", "unix:///tmp/pmem-csi.sock", "PMEM CSI endpoint")
	mode             = flag.String("mode", "", "driver run mode: controller or node")
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

	glog.V(3).Info("Version: ", version)

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
		errorexit("Failed to initialize driver:", err)
	}

	if err = driver.Run(); err != nil {
		errorexit("Failed to run driver:", err)
	}
}

func errorexit(msg string, e error) {
	str := msg + e.Error()
	fmt.Println(str)
	terminationMsgPath := os.Getenv("TERMINATION_LOG_PATH")
	if terminationMsgPath != "" {
		err := ioutil.WriteFile(terminationMsgPath, []byte(str), os.FileMode(0644))
		if err != nil {
			fmt.Println("Can not create termination log file:" + terminationMsgPath)
		}
	}

	os.Exit(1)
}
