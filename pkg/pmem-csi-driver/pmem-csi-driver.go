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

	"os/exec"
	"strings"

	"github.com/container-storage-interface/spec/lib/go/csi/v0"
	"github.com/golang/glog"
	"github.com/intel/pmem-csi/pkg/ndctl"
	pmemgrpc "github.com/intel/pmem-csi/pkg/pmem-grpc"
	registry "github.com/intel/pmem-csi/pkg/pmem-registry"
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
	//NamespaceSize size(in GB) of namespace block
	NamespaceSize int
}

type pmemDriver struct {
	driver *CSIDriver
	cfg    Config
	ctx    *ndctl.Context
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

	ctx, err := ndctl.NewContext()
	if err != nil {
		return nil, fmt.Errorf("Failed to initialize pmem context: %s", err.Error())
	}

	return &pmemDriver{
		cfg:    cfg,
		driver: driver,
		ctx:    ctx,
	}, nil
}

func (pmemd *pmemDriver) Run() error {
	// Create GRPC servers
	pmemd.ids = NewIdentityServer(pmemd)
	s := NewNonBlockingGRPCServer()

	if pmemd.cfg.Mode == Controller {
		pmemd.rs = NewRegistryServer()
		pmemd.cs = NewControllerServer(pmemd.driver, pmemd.cfg.Mode, pmemd.rs, pmemd.ctx)
		pmemd.driver.AddControllerServiceCapabilities([]csi.ControllerServiceCapability_RPC_Type{
			csi.ControllerServiceCapability_RPC_CREATE_DELETE_VOLUME,
			csi.ControllerServiceCapability_RPC_PUBLISH_UNPUBLISH_VOLUME,
			csi.ControllerServiceCapability_RPC_LIST_VOLUMES,
		})

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
		pmemd.ns = NewNodeServer(pmemd.driver, pmemd.ctx)
		pmemd.cs = NewControllerServer(pmemd.driver, pmemd.cfg.Mode, nil, pmemd.ctx)
		pmemd.driver.AddControllerServiceCapabilities([]csi.ControllerServiceCapability_RPC_Type{
			csi.ControllerServiceCapability_RPC_PUBLISH_UNPUBLISH_VOLUME,
			csi.ControllerServiceCapability_RPC_LIST_VOLUMES,
		})
		pmemd.driver.AddNodeServiceCapabilities([]csi.NodeServiceCapability_RPC_Type{
			csi.NodeServiceCapability_RPC_STAGE_UNSTAGE_VOLUME,
		})

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
			if err := pmemd.registerNodeController(); err != nil {
				return err
			}
		} else /* if pmemd.cfg.Mode == Unified */ {
			pmemd.driver.AddControllerServiceCapabilities([]csi.ControllerServiceCapability_RPC_Type{
				csi.ControllerServiceCapability_RPC_CREATE_DELETE_VOLUME,
			})
			if err := s.Start(pmemd.cfg.Endpoint, func(server *grpc.Server) {
				csi.RegisterIdentityServer(server, pmemd.ids)
				csi.RegisterControllerServer(server, pmemd.cs)
				csi.RegisterNodeServer(server, pmemd.ns)
			}); err != nil {
				return err
			}
		}
		InitNVdimms(pmemd.ctx, pmemd.cfg.NamespaceSize)
	}

	defer s.Stop()
	s.Wait()

	return nil
}

const (
	// we get this as argument 'namespacesize'
	//namespaceSize uint64 = (4*1024*1024*1024)
	// TODO: try to get rid of hard-coded overhead
	namespaceOverhead = (4 * 1024 * 1024)
	// smaller namespace size (in GB) for devel mode in VM
	namespacesizeVM = 2
)

// Try to make all space in all regions consumed by namespaces
// for all regions:
// - Check available size, if bigger than one NS size, create one more, repeat this in loop
func CreateNamespaces(ctx *ndctl.Context, namespacesize int) {

	var nsSize uint64 = (uint64(namespacesize) * 1024 * 1024 * 1024)
	for _, bus := range ctx.GetBuses() {
		for _, r := range bus.ActiveRegions() {
			glog.Infof("CreateNamespaces in %v with %v bytes available", r.DeviceName(), r.AvailableSize())
			availSize := r.AvailableSize()
			for availSize > nsSize+namespaceOverhead {
				glog.Info("Available is greater than one NS size, try to create a Namespace")
				_, err := ctx.CreateNamespace(ndctl.CreateNamespaceOpts{
					Size: nsSize,
				})
				if err != nil {
					glog.Fatalln("Failed to create namespace: %s", err.Error())
				}
				availSize -= nsSize
				availSize -= namespaceOverhead
				glog.Infof("Remaining Available in %v is %v", r.DeviceName(), availSize)
			}
			glog.Infof("avail_size in %v not enough for adding more namespaces", r.DeviceName())
		}
	}
}

func VGName(bus *ndctl.Bus, region *ndctl.Region) string {
	return bus.DeviceName() + region.DeviceName()
}

// TODO: could combine loops in above and below function, as loops traverse
// same buses, regions, namespaces. But readability,clarity will suffer, so I prefer not to combine right now

// for all regions:
// - Check that VG exists for this region. Create if does not exist
// - For all namespaces in region:
//   - check that PVol exists provided by that namespace, is in current VG, add if does not exist
// Edge cases are when no PVol or VG structures (or partially) dont exist yet
func CheckVG(ctx *ndctl.Context) {
	var vgMissing bool = false
	for _, bus := range ctx.GetBuses() {
		glog.Infof("CheckVG: Bus: %v", bus.DeviceName())
		for _, r := range bus.ActiveRegions() {
			glog.Infof("CheckVG: Region: %v", r.DeviceName())
			vgName := VGName(bus, r)
			_, err := exec.Command("vgdisplay", vgName).CombinedOutput()
			if err != nil {
				glog.Infof("No Vgroup with name %v, mark for creation", vgName)
				// by common structured logic, we should create VG here, but vgcreate wants at least one PVol
				// means we set a flag here "no-vg" and create VG below in loop over workspaces where
				// we already have a 1st PVol known
				vgMissing = true
			} else {
				glog.Infof("CheckVG: VolGroup %v exists", vgName)
				vgMissing = false
			}
			nss := r.ActiveNamespaces()
			for _, ns := range nss {
				devicepath := "/dev/" + ns.BlockDeviceName()
				output, err := exec.Command("pvs", "--noheadings", "-o", "vg_name", devicepath).CombinedOutput()
				// NOTE I have seen this "pvs" showing, in addition to regular output (just one word as vgroup):
				// happened in VM combined with unstable NVDIMM namespaces and pfn-related errors
				// WARNING: Device for PV oIDCwu-TSqc-ED1g-nKzy-I0SX-kXHK-t5Td1C not found or rejected by a filter.
				// This messes up parsing, and shows all PVs seemingly in wrong VG.
				// This WARNING seems cant be filtered out by options (like -q).
				// On host, this fixes it: vgreduce --removemissing VG
				grp := strings.TrimSpace(string(output))
				if err != nil {
					// no PVol created from this NS yet, perform pvcreate
					glog.Infof("No PVol for %v, perform pvcreate", devicepath)
					_, err := exec.Command("pvcreate", devicepath).CombinedOutput()
					if err != nil {
						glog.Infof("pvcreate %v failed", devicepath)
					}
					if vgMissing {
						glog.Infof("PVol to be added, but VG missing, vgcreate VG %v with first PVol %v", vgName, devicepath)
						_, err := exec.Command("vgcreate", vgName, devicepath).CombinedOutput()
						if err != nil {
							glog.Infof("vgcreate failed")
						} else {
							vgMissing = false
						}
					} else {
						glog.Infof("PVol to be added, VG exists, run vgextend %v %v", vgName, devicepath)
						_, err = exec.Command("vgextend", vgName, devicepath).CombinedOutput()
						if err != nil {
							glog.Infof("vgextend failed")
						}
					}
				} else {
					//glog.Infof("PVol %v exists, in group: [%v]", devicepath, grp)
					if grp != vgName {
						// the case with group name mismatch is bit of edge case(?), happens on reconfig of previously configured system?
						glog.Infof("PVol %v is reported in group [%v] which is not correct group %v, try adding using vgextend",
							devicepath, grp, vgName)
						// TODO: (edge case?) what if PVol is in some other group, chould be detached from there first?
						_, err = exec.Command("vgextend", vgName, devicepath).CombinedOutput()
						if err != nil {
							glog.Infof("vgextend failed")
						}
					} else {
						glog.Infof("PVol %v exists and belongs to VolGroup %v, no action", devicepath, vgName)
					}
				}
			}
		}
	}
}

func InitNVdimms(ctx *ndctl.Context, namespacesize int) {
	// check is there physical NVDIMM(s) present. What happens if we run this without NVDIMM:
	// verified on a VM without NVDIMMs:
	// loop attempt in CreateNamespaces over buses-regions-namespaces makes zero loops,
	// and CheckVG() creates zero PVs, then driver starts running,
	// but ops will fail as there is no regions and PVs, so it's safe.
	// TODO: Should we detect device(s) explicitly here?

	// check are we running in VM. Useful for dev-mode with emulated NVDIMM case, set namespacesize smaller
	// TODO: this depends on systemd-detect-virt, to be clarified is this present always.
	// At least present in CentOS-7, openSUSE-15, Fedora-28, Ubuntu-18.04, Clear
	// if cmd returns 0, we are in VM (and text shows which type)
	// if cmd returns nonzero, its either "cmd exists and we run on baremetal (retval 1)" or "cmd does not exist: retval 127"
	// in both cases we settle to baremetal mode, no harm done
	glog.Infof("Configured namespacesize; %v GB", namespacesize)
	output, err := exec.Command("systemd-detect-virt", "-v").CombinedOutput()
	if err != nil {
		glog.Infof("VM detection: [%v] [%v], seem to run on bare metal, use default %v GB namespace size",
			strings.TrimSpace(string(output)), err, namespacesize)
	} else {
		glog.Infof("VM detection: [%v], this looks like VM, use smaller %v GB namespace size",
			strings.TrimSpace(string(output)), namespacesizeVM)
		namespacesize = namespacesizeVM
	}
	CreateNamespaces(ctx, namespacesize)
	CheckVG(ctx)
	/* for debug
		nss := ctx.GetActiveNamespaces()
	        glog.Info("elems in Namespaces:", len(nss))
	        for _, ns := range nss {
	                glog.Info("Namespace Name:", ns.Name())
	                glog.Info("    Size:", ns.Size())
	                glog.Info("    Device:", ns.DeviceName())
	                glog.Info("    Mode:", ns.Mode())
	                glog.Info("    BlockDevice:", ns.BlockDeviceName())
		}*/
}

func (pmemd *pmemDriver) registerNodeController() error {
	fmt.Printf("Connecting to Registry at : %s\n", pmemd.cfg.RegistryEndpoint)
	var err error
	var conn *grpc.ClientConn

	for true {
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
	req := registry.RegisterControllerRequest{
		NodeId:   pmemd.driver.nodeID,
		Endpoint: pmemd.cfg.ControllerEndpoint,
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()

	if _, err = client.RegisterController(ctx, &req); err != nil {
		return fmt.Errorf("Fail to register with server at '%s' : %s", pmemd.cfg.RegistryEndpoint, err.Error())
	}

	return nil
}
