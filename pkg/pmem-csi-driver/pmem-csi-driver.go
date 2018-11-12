/*
Copyright 2017 The Kubernetes Authors.
Copyright 2018 Intel Corporation.

SPDX-License-Identifier: Apache-2.0
*/

package pmemcsidriver

import (
	"context"
	"fmt"
	"time"

	"github.com/container-storage-interface/spec/lib/go/csi/v0"
	"github.com/golang/glog"
	pmdmanager "github.com/intel/csi-pmem/pkg/pmem-device-manager"
	pmemgrpc "github.com/intel/csi-pmem/pkg/pmem-grpc"
	registry "github.com/intel/csi-pmem/pkg/pmem-registry"
	"google.golang.org/grpc"
)

const (
	vendorVersion     string        = "0.0.1"
	connectionTimeout time.Duration = 10 * time.Second
	retryTimeout      time.Duration = 10 * time.Second
	requestTimeout    time.Duration = 10 * time.Second
)

type DriverMode string

const (
	//Controller defintion for controller driver mode
	Controller DriverMode = "controller"
	//Node defintion for noder driver mode
	Node DriverMode = "node"
	//Unified defintion for unified driver mode
	Unified DriverMode = "unified"
)

//Config type for driver configuration
type Config struct {
	//DriverName name of the csi driver
	DriverName string
	//NodeID node id on which this csi driver is running
	NodeID string
	//Endpoint exported csi driver endpoint
	Endpoint string
	//Mode mode fo the driver
	Mode DriverMode
	//RegistryEndpoint exported registry server endpoint
	RegistryEndpoint string
	//ControllerEndpoint exported node controller endpoint
	ControllerEndpoint string
	//DeviceManager device manager to use
	DeviceManager string
}

type pmemDriver struct {
	driver *CSIDriver
	cfg    Config
	ids    csi.IdentityServer
	ns     csi.NodeServer
	cs     csi.ControllerServer
	rs     *registryServer
}

func GetPMEMDriver(cfg Config) (*pmemDriver, error) {
	validModes := map[DriverMode]struct{}{
		Controller: struct{}{},
		Node:       struct{}{},
		Unified:    struct{}{},
	}
	if _, ok := validModes[cfg.Mode]; !ok {
		return nil, fmt.Errorf("Invalid driver mode: %s", string(cfg.Mode))
	}
	if cfg.DriverName == "" || cfg.NodeID == "" || cfg.Endpoint == "" {
		return nil, fmt.Errorf("One of mandatory(Drivername Node id or Endpoint) configuration option missing")
	}
	if cfg.RegistryEndpoint == "" {
		cfg.RegistryEndpoint = cfg.Endpoint
	}
	if cfg.ControllerEndpoint == "" {
		cfg.ControllerEndpoint = cfg.Endpoint
	}

	driver, err := NewCSIDriver(cfg.DriverName, vendorVersion, cfg.NodeID)
	if err != nil {
		return nil, err
	}
	driver.AddVolumeCapabilityAccessModes([]csi.VolumeCapability_AccessMode_Mode{
		csi.VolumeCapability_AccessMode_SINGLE_NODE_WRITER,
	})

	return &pmemDriver{
		cfg:    cfg,
		driver: driver,
	}, nil
}

func (pmemd *pmemDriver) Run() error {
	// Create GRPC servers
	pmemd.ids = NewIdentityServer(pmemd)
	s := NewNonBlockingGRPCServer()

	if pmemd.cfg.Mode == Controller {
		pmemd.rs = NewRegistryServer()
		pmemd.cs = NewControllerServer(pmemd.driver, pmemd.cfg.Mode, pmemd.rs, nil)

		if pmemd.cfg.Endpoint != pmemd.cfg.RegistryEndpoint {
			if err := s.Start(pmemd.cfg.Endpoint, func(server *grpc.Server) {
				csi.RegisterIdentityServer(server, pmemd.ids)
				csi.RegisterControllerServer(server, pmemd.cs)
			}); err != nil {
				return err
			}
			if err := s.Start(pmemd.cfg.RegistryEndpoint, func(server *grpc.Server) {
				registry.RegisterRegistryServer(server, pmemd.rs)
			}); err != nil {
				return err
			}
		} else {
			if err := s.Start(pmemd.cfg.Endpoint, func(server *grpc.Server) {
				csi.RegisterIdentityServer(server, pmemd.ids)
				csi.RegisterControllerServer(server, pmemd.cs)
				registry.RegisterRegistryServer(server, pmemd.rs)
			}); err != nil {
				return err
			}
		}
	} else {
		dm, err := newDeviceManager(pmemd.cfg.DeviceManager)
		if err != nil {
			return err
		}
		pmemd.ns = NewNodeServer(pmemd.driver, dm)
		pmemd.cs = NewControllerServer(pmemd.driver, pmemd.cfg.Mode, nil, dm)

		if pmemd.cfg.Mode == Node {
			if pmemd.cfg.Endpoint != pmemd.cfg.ControllerEndpoint {
				if err := s.Start(pmemd.cfg.Endpoint, func(server *grpc.Server) {
					csi.RegisterIdentityServer(server, pmemd.ids)
					csi.RegisterNodeServer(server, pmemd.ns)
				}); err != nil {
					return err
				}
				if err := s.Start(pmemd.cfg.ControllerEndpoint, func(server *grpc.Server) {
					csi.RegisterControllerServer(server, pmemd.cs)
					//registry.RegisterNodeControllerServer(server, pmemd.ncs)
				}); err != nil {
					return err
				}
			} else {
				if err := s.Start(pmemd.cfg.Endpoint, func(server *grpc.Server) {
					csi.RegisterIdentityServer(server, pmemd.ids)
					csi.RegisterControllerServer(server, pmemd.cs)
					csi.RegisterNodeServer(server, pmemd.ns)
				}); err != nil {
					return err
				}
			}
			if err := pmemd.registerNodeController(dm); err != nil {
				return err
			}
		} else /* if pmemd.cfg.Mode == Unified */ {
			if err := s.Start(pmemd.cfg.Endpoint, func(server *grpc.Server) {
				csi.RegisterIdentityServer(server, pmemd.ids)
				csi.RegisterControllerServer(server, pmemd.cs)
				csi.RegisterNodeServer(server, pmemd.ns)
			}); err != nil {
				return err
			}
		}
	}

	defer s.Stop()
	s.Wait()

	return nil
}

func (pmemd *pmemDriver) registerNodeController(dm pmdmanager.PmemDeviceManager) error {
	fmt.Printf("Connecting to Registry at : %s\n", pmemd.cfg.RegistryEndpoint)
	var err error
	var conn *grpc.ClientConn

	for {
		conn, err = pmemgrpc.Connect(pmemd.cfg.RegistryEndpoint, connectionTimeout)
		if err == nil {
			glog.Infof("Conneted to RegistryServer!!!")
			break
		}
		/* TODO: Retry loop */
		glog.Infof("Failed to connect RegistryServer : %s, retrying after 10 seconds...", err.Error())
		time.Sleep(10 * time.Second)
	}
	client := registry.NewRegistryClient(conn)
	capacity, err := dm.GetCapacity()
	if err != nil {
		glog.Warningf("Error while preparing node capacity: %s", err.Error())
	}
	req := registry.RegisterControllerRequest{
		NodeId:   pmemd.driver.nodeID,
		Endpoint: pmemd.cfg.ControllerEndpoint,
		Capacity: capacity,
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()

	if _, err = client.RegisterController(ctx, &req); err != nil {
		return fmt.Errorf("Fail to register with server at '%s' : %s", pmemd.cfg.RegistryEndpoint, err.Error())
	}

	return nil
}

func newDeviceManager(dmType string) (pmdmanager.PmemDeviceManager, error) {
	switch dmType {
	case "lvm":
		return pmdmanager.NewPmemDeviceManagerLVM()
	case "ndctl":
		return pmdmanager.NewPmemDeviceManagerNdctl()
	}
	return nil, fmt.Errorf("Unsupported device manager type '%s", dmType)
}
