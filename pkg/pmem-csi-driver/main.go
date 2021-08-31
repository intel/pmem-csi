/*
Copyright 2017 The Kubernetes Authors.
Copyright 2018 Intel Coporation.

SPDX-License-Identifier: Apache-2.0
*/

package pmemcsidriver

import (
	"context"
	"errors"
	"flag"
	"fmt"

	api "github.com/intel/pmem-csi/pkg/apis/pmemcsi/v1beta1"
	"github.com/intel/pmem-csi/pkg/logger"
	pmemlog "github.com/intel/pmem-csi/pkg/logger"
	pmemcommon "github.com/intel/pmem-csi/pkg/pmem-common"
)

var (
	config = Config{
		Mode:          Node,
		DeviceManager: api.DeviceModeLVM,
	}
	showVersion = flag.Bool("version", false, "Show release version and exit")
	logFormat   = logger.NewFlag()
	version     = "unknown" // Set version during build time
)

func init() {
	/* generic options */
	flag.StringVar(&config.DriverName, "drivername", "pmem-csi.intel.com", "name of the driver")
	flag.StringVar(&config.NodeID, "nodeid", "nodeid", "node id")
	flag.StringVar(&config.Endpoint, "endpoint", "unix:///tmp/pmem-csi.sock", "PMEM CSI endpoint")
	flag.Var(&config.Mode, "mode", "driver run mode")
	flag.StringVar(&config.CAFile, "caFile", "ca.pem", "Root CA certificate file to use for verifying clients (optional, can be empty)")
	flag.StringVar(&config.CertFile, "certFile", "pmem-controller.pem", "SSL certificate file to be used by the PMEM-CSI controller")
	flag.StringVar(&config.KeyFile, "keyFile", "pmem-controller-key.pem", "Private key file associated with the certificate")

	flag.Float64Var(&config.KubeAPIQPS, "kube-api-qps", 5, "QPS to use while communicating with the Kubernetes apiserver. Defaults to 5.0.")
	flag.IntVar(&config.KubeAPIBurst, "kube-api-burst", 10, "Burst to use while communicating with the Kubernetes apiserver. Defaults to 10.")

	/* metrics options */
	flag.StringVar(&config.metricsListen, "metricsListen", "", "listen address (like :8001) for prometheus metrics endpoint, disabled by default")
	flag.StringVar(&config.metricsPath, "metricsPath", "/metrics", "The HTTP path where prometheus metrics will be exposed. Default is `/metrics`.")

	/* Controller mode options */
	flag.StringVar(&config.schedulerListen, "schedulerListen", "", "controller: HTTPS listen address (like :8000) for scheduler extender and mutating webhook, disabled by default (needs caFile, certFile, keyFile)")
	flag.StringVar(&config.insecureSchedulerListen, "insecureSchedulerListen", "", "controller: HTTP listen address (like :8001) for scheduler extender and mutating webhook, disabled by default (does not use TLS config)")
	flag.Var(&config.nodeSelector, "nodeSelector", "controller: reschedule PVCs with a selected node where PMEM-CSI is not meant to run because the node does not have these labels (represented as JSON map)")

	/* Node mode options */
	flag.Var(&config.DeviceManager, "deviceManager", "node: device manager to use to manage pmem devices, supported types: 'lvm' or 'direct' (= 'ndctl')")
	flag.StringVar(&config.StateBasePath, "statePath", "", "node: directory path where to persist the state of the driver, defaults to /var/lib/<drivername>")
	flag.UintVar(&config.PmemPercentage, "pmemPercentage", 100, "node: percentage of space to be used by the driver in each PMEM region")

	_ = flag.Set("logtostderr", "true")
}

func Main() int {
	flag.Parse()
	logFormat.Apply()
	if *showVersion {
		fmt.Println(version)
		return 0
	}

	ctx := context.Background()
	logger := pmemlog.Get(ctx)
	ctx = pmemlog.Set(ctx, logger)

	logger.Info("PMEM-CSI started.", "version", version)
	defer logger.Info("PMEM-CSI stopped.")

	if config.schedulerListen != "" && config.Mode != Webhooks {
		pmemcommon.ExitError("scheduler listening", errors.New("only supported in the controller"))
		return 1
	}

	config.Version = version
	driver, err := GetCSIDriver(config)
	if err != nil {
		pmemcommon.ExitError("failed to initialize driver", err)
		return 1
	}

	if err = driver.Run(ctx); err != nil {
		pmemcommon.ExitError("failed to run driver", err)
		return 1
	}

	return 0
}
