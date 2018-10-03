/*
Copyright 2017 The Kubernetes Authors.
Copyright 2018 Intel Corporation.

SPDX-License-Identifier: Apache-2.0
*/

package pmemcsidriver

import (
	"github.com/container-storage-interface/spec/lib/go/csi/v0"
	"github.com/golang/glog"
	"github.com/intel/pmem-csi/pkg/ndctl"
	"os/exec"
	"strings"
)

/* these came with some example, but not used currently
const (
	kib    int64 = 1024
	mib    int64 = kib * 1024
	gib    int64 = mib * 1024
	gib100 int64 = gib * 100
	tib    int64 = gib * 1024
	tib100 int64 = tib * 100
)
*/
type pmemDriver struct {
	driverName string
	nodeID     string
	driver     *CSIDriver

	ids *identityServer
	ns  *nodeServer
	cs  *controllerServer
	ctx *ndctl.Context

	cap   []*csi.VolumeCapability_AccessMode
	cscap []*csi.ControllerServiceCapability
}

var (
	//	pmemDriver     *pmemd
	vendorVersion = "0.0.1"
)

/*
TODO: commenting this out for now.
idea is that it's safer to avoid maintaining state in driver.
Instead, we always look it up from lower side.
Let's see can we keep it that way...

type pmemVolume struct {
	VolName string `json:"volName"`
	VolID   string `json:"volID"`
	VolSize int64  `json:"volSize"`
	VolPath string `json:"volPath"`
}

var pmemVolumes map[string]pmemVolume

func init() {
	pmemVolumes = map[string]pmemVolume{}
}
func getVolumeByID(volumeID string) (pmemVolume, error) {
	if pmemVol, ok := pmemVolumes[volumeID]; ok {
		return pmemVol, nil
	}
	return pmemVolume{}, fmt.Errorf("volume id %s does not exit in the volumes list", volumeID)
}

func getVolumeByName(volName string) (pmemVolume, error) {
	for _, pmemVol := range pmemVolumes {
		if pmemVol.VolName == volName {
			return pmemVol, nil
		}
	}
	return pmemVolume{}, fmt.Errorf("volume name %s does not exit in the volumes list", volName)
}

*/
func GetPMEMDriver() *pmemDriver {
	return &pmemDriver{}
}

func NewIdentityServer(pmemd *pmemDriver) *identityServer {
	return &identityServer{
		DefaultIdentityServer: NewDefaultIdentityServer(pmemd.driver),
	}
}

func NewControllerServer(pmemd *pmemDriver) *controllerServer {
	return &controllerServer{
		DefaultControllerServer: NewDefaultControllerServer(pmemd.driver),
		ctx: pmemd.ctx,
	}
}

func NewNodeServer(pmemd *pmemDriver) *nodeServer {
	return &nodeServer{
		DefaultNodeServer: NewDefaultNodeServer(pmemd.driver),
		ctx:               pmemd.ctx,
	}
}

func (pmemd *pmemDriver) Run(driverName, nodeID, endpoint string, namespacesize int) {
	//s, err := pmemd.Start(driverName, nodeID, endpoint)
	pmemd.driver = NewCSIDriver(driverName, vendorVersion, nodeID)
	if pmemd.driver == nil {
		glog.Fatalln("Failed to initialize CSI Driver.")
	}
	pmemd.driver.AddControllerServiceCapabilities([]csi.ControllerServiceCapability_RPC_Type{
		csi.ControllerServiceCapability_RPC_CREATE_DELETE_VOLUME,
		csi.ControllerServiceCapability_RPC_LIST_VOLUMES})
	pmemd.driver.AddNodeServiceCapabilities([]csi.NodeServiceCapability_RPC_Type{csi.NodeServiceCapability_RPC_STAGE_UNSTAGE_VOLUME})
	pmemd.driver.AddVolumeCapabilityAccessModes([]csi.VolumeCapability_AccessMode_Mode{csi.VolumeCapability_AccessMode_SINGLE_NODE_WRITER})

	ctx, err := ndctl.NewContext()
	if err != nil {
		glog.Fatalln("Failed to initialize pmem context: %s", err.Error())
	}
	pmemd.ctx = ctx
	// Create GRPC servers
	pmemd.ids = NewIdentityServer(pmemd)
	pmemd.ns = NewNodeServer(pmemd)
	pmemd.cs = NewControllerServer(pmemd)

	InitNVdimms(ctx, namespacesize)

	s := NewNonBlockingGRPCServer()
	s.Start(endpoint, pmemd.ids, pmemd.cs, pmemd.ns)
	s.Wait()
}

const (
	// we get this as argument 'namespacesize'
	//namespaceSize uint64 = (4*1024*1024*1024)
	// TODO: try to get rid of hard-coded overhead
	namespaceOverhead = (4*1024*1024)
	// smaller namespace size (in GB) for devel mode in VM
	namespacesizeVM = 2
)

// Try to make all space in all regions consumed by namespaces
// for all regions:
// - Check available size, if bigger than one NS size, create one more, repeat this in loop
func CreateNamespaces(ctx *ndctl.Context, namespacesize int) {

	var nsSize uint64 = (uint64(namespacesize)*1024*1024*1024)
        for _, bus := range ctx.GetBuses() {
                for _, r := range bus.ActiveRegions() {
			glog.Infof("CreateNamespaces in %v with %v bytes available", r.DeviceName(), r.AvailableSize())
			availSize := r.AvailableSize()
			for availSize > nsSize + namespaceOverhead {
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
