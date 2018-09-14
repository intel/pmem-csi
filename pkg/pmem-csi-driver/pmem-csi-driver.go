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

func (pmemd *pmemDriver) Run(driverName, nodeID, endpoint string) {
	//s, err := pmemd.Start(driverName, nodeID, endpoint)
	pmemd.driver = NewCSIDriver(driverName, vendorVersion, nodeID)
	if pmemd.driver == nil {
		glog.Fatalln("Failed to initialize CSI Driver.")
	}
	pmemd.driver.AddControllerServiceCapabilities([]csi.ControllerServiceCapability_RPC_Type{csi.ControllerServiceCapability_RPC_CREATE_DELETE_VOLUME})
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

	s := NewNonBlockingGRPCServer()
	s.Start(endpoint, pmemd.ids, pmemd.cs, pmemd.ns)
	s.Wait()
}
