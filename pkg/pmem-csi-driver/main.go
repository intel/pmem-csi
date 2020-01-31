/*
Copyright 2017 The Kubernetes Authors.
Copyright 2018 Intel Coporation.

SPDX-License-Identifier: Apache-2.0
*/

package pmemcsidriver

import (
	"errors"
	"flag"
	"fmt"

	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/klog"

	pmemcommon "github.com/intel/pmem-csi/pkg/pmem-common"
)

var (
	/* generic options */
	driverName       = flag.String("drivername", "pmem-csi.intel.com", "name of the driver")
	nodeID           = flag.String("nodeid", "nodeid", "node id")
	endpoint         = flag.String("endpoint", "unix:///tmp/pmem-csi.sock", "PMEM CSI endpoint")
	testEndpoint     = flag.Bool("testEndpoint", false, "also expose controller interface via endpoint (for testing only)")
	mode             = flag.String("mode", "controller", "driver run mode: controller or node")
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
	driverStatePath    = flag.String("statePath", "", "Directory path where to persist the state of the driver running on a node, defaults to /var/lib/<drivername>")

	/* scheduler options */
	schedulerListenAddr = flag.String("schedulerListen", "", "listen address (like :8000) for scheduler extender and mutating webhook, disabled by default")

	version = "unknown" // Set version during build time
)

func init() {
	klog.InitFlags(nil)
	flag.Set("logtostderr", "true")
}

func Main() int {
	if *showVersion {
		fmt.Println(version)
		return 0
	}

	klog.V(3).Info("Version: ", version)

	var client *kubernetes.Clientset
	if *schedulerListenAddr != "" {
		if DriverMode(*mode) != Controller {
			pmemcommon.ExitError("scheduler listening", errors.New("only supported in the controller"))
			return 1
		}
		config, err := rest.InClusterConfig()
		if err != nil {
			pmemcommon.ExitError("failed to build in-cluster Kubernetes client configuration", err)
			return 1
		}
		client, err = kubernetes.NewForConfig(config)
		if err != nil {
			pmemcommon.ExitError("failed to create Kubernetes client", err)
			return 1
		}
	}

	driver, err := GetPMEMDriver(Config{
		DriverName:         *driverName,
		NodeID:             *nodeID,
		Endpoint:           *endpoint,
		TestEndpoint:       *testEndpoint,
		Mode:               DriverMode(*mode),
		RegistryEndpoint:   *registryEndpoint,
		CAFile:             *caFile,
		CertFile:           *certFile,
		KeyFile:            *keyFile,
		ClientCertFile:     *clientCertFile,
		ClientKeyFile:      *clientKeyFile,
		ControllerEndpoint: *controllerEndpoint,
		DeviceManager:      *deviceManager,
		StateBasePath:      *driverStatePath,
		Version:            version,
		schedulerListen:    *schedulerListenAddr,
		client:             client,
	})
	if err != nil {
		pmemcommon.ExitError("failed to initialize driver", err)
		return 1
	}

	if err = driver.Run(); err != nil {
		pmemcommon.ExitError("failed to run driver", err)
		return 1
	}

	return 0
}
