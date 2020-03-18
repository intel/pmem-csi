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

	"k8s.io/klog"

	"github.com/intel/pmem-csi/pkg/k8sutil"
	pmemcommon "github.com/intel/pmem-csi/pkg/pmem-common"
)

var (
	config = Config{
		Mode:          Controller,
		DeviceManager: LVM,
	}
	showVersion = flag.Bool("version", false, "Show release version and exit")
	version     = "unknown" // Set version during build time
)

func init() {
	/* generic options */
	flag.StringVar(&config.DriverName, "drivername", "pmem-csi.intel.com", "name of the driver")
	flag.StringVar(&config.NodeID, "nodeid", "nodeid", "node id")
	flag.StringVar(&config.Endpoint, "endpoint", "unix:///tmp/pmem-csi.sock", "PMEM CSI endpoint")
	flag.BoolVar(&config.TestEndpoint, "testEndpoint", false, "also expose controller interface via endpoint (for testing only)")
	flag.Var(&config.Mode, "mode", "driver run mode: controller or node")
	flag.StringVar(&config.RegistryEndpoint, "registryEndpoint", "", "endpoint to connect/listen registry server")
	flag.StringVar(&config.CAFile, "caFile", "", "Root CA certificate file to use for verifying connections")
	flag.StringVar(&config.CertFile, "certFile", "", "SSL certificate file to use for authenticating client connections(RegistryServer/NodeControllerServer)")
	flag.StringVar(&config.KeyFile, "keyFile", "", "Private key file associated to certificate")
	flag.StringVar(&config.ClientCertFile, "clientCertFile", "", "Client SSL certificate file to use for authenticating peer connections, defaults to 'certFile'")
	flag.StringVar(&config.ClientKeyFile, "clientKeyFile", "", "Client private key associated to client certificate, defaults to 'keyFile'")
	/* Node mode options */
	flag.StringVar(&config.ControllerEndpoint, "controllerEndpoint", "", "internal node controller endpoint")
	flag.Var(&config.DeviceManager, "deviceManager", "device manager to use to manage pmem devices, supported types: 'lvm' or 'direct' (= 'ndctl')")
	flag.StringVar(&config.StateBasePath, "statePath", "", "Directory path where to persist the state of the driver running on a node, defaults to /var/lib/<drivername>")

	/* scheduler options */
	flag.StringVar(&config.schedulerListen, "schedulerListen", "", "listen address (like :8000) for scheduler extender and mutating webhook, disabled by default")

	/* metrics options */
	flag.StringVar(&config.metricsListen, "metricsListen", "", "listen address (like :8001) for prometheus metrics endpoint, disabled by default")
	flag.StringVar(&config.metricsPath, "metricsPath", "/metrics", "The HTTP path where prometheus metrics will be exposed. Default is `/metrics`.")

	flag.Set("logtostderr", "true")
}

func Main() int {
	if *showVersion {
		fmt.Println(version)
		return 0
	}

	klog.V(3).Info("Version: ", version)

	if config.schedulerListen != "" {
		if config.Mode != Controller {
			pmemcommon.ExitError("scheduler listening", errors.New("only supported in the controller"))
			return 1
		}
		c, err := k8sutil.NewInClusterClient()
		if err != nil {
			pmemcommon.ExitError("scheduler setup", err)
			return 1
		}
		config.client = c
	}

	config.Version = version
	driver, err := GetPMEMDriver(config)
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
