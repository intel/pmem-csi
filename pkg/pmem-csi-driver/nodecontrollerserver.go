/*
Copyright 2017 The Kubernetes Authors.

SPDX-License-Identifier: Apache-2.0
*/

package pmemcsidriver

import (
	"fmt"

	"github.com/google/uuid"
	"golang.org/x/net/context"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"k8s.io/klog/glog"

	"github.com/container-storage-interface/spec/lib/go/csi/v0"

	pmemcommon "github.com/intel/pmem-csi/pkg/pmem-common"
	pmdmanager "github.com/intel/pmem-csi/pkg/pmem-device-manager"
	"k8s.io/utils/keymutex"
)

type nodeVolume struct {
	ID     string
	Name   string
	Size   int64
	Erase  bool
	NsMode string
}

type nodeControllerServer struct {
	*DefaultControllerServer
	dm          pmdmanager.PmemDeviceManager
	pmemVolumes map[string]*nodeVolume // map of reqID:nodeVolume
}

var _ csi.ControllerServer = &nodeControllerServer{}
var _ PmemService = &nodeControllerServer{}

var nodeVolumeMutex = keymutex.NewHashed(-1)

func NewNodeControllerServer(driver *CSIDriver, dm pmdmanager.PmemDeviceManager) *nodeControllerServer {
	serverCaps := []csi.ControllerServiceCapability_RPC_Type{}
	serverCaps = append(serverCaps, csi.ControllerServiceCapability_RPC_CREATE_DELETE_VOLUME)
	serverCaps = append(serverCaps, csi.ControllerServiceCapability_RPC_LIST_VOLUMES)
	serverCaps = append(serverCaps, csi.ControllerServiceCapability_RPC_GET_CAPACITY)
	driver.AddControllerServiceCapabilities(serverCaps)

	return &nodeControllerServer{
		DefaultControllerServer: NewDefaultControllerServer(driver),
		dm:                      dm,
		pmemVolumes:             map[string]*nodeVolume{},
	}
}

func (cs *nodeControllerServer) RegisterService(rpcServer *grpc.Server) {
	csi.RegisterControllerServer(rpcServer, cs)
}

func (cs *nodeControllerServer) CreateVolume(ctx context.Context, req *csi.CreateVolumeRequest) (*csi.CreateVolumeResponse, error) {
	var vol *nodeVolume

	volumeID := ""
	eraseafter := true
	nsmode := "fsdax"

	if err := cs.Driver.ValidateControllerServiceRequest(csi.ControllerServiceCapability_RPC_CREATE_DELETE_VOLUME); err != nil {
		pmemcommon.Infof(3, ctx, "invalid create volume req: %v", req)
		return nil, err
	}

	if req.GetVolumeCapabilities() == nil {
		return nil, status.Error(codes.InvalidArgument, "Volume Capabilities missing in request")
	}

	if len(req.GetName()) == 0 {
		return nil, status.Error(codes.InvalidArgument, "Name missing in request")
	}

	// We recognize eraseafter=false/true, defaulting to true
	for key, val := range req.GetParameters() {
		glog.Infof("CreateVolume: parameter: [%v] [%v]", key, val)
		if key == "eraseafter" {
			if val == "true" {
				eraseafter = true
			} else if val == "false" {
				eraseafter = false
			}
		} else if key == "nsmode" {
			if val == "fsdax" || val == "sector" {
				nsmode = val
			}
		} else if key == "_id" {
			/* use master controller provided volume uid */
			volumeID = val
		}
	}

	/* choose volume uid if not provided by master controller */
	if volumeID == "" {
		id, _ := uuid.NewUUID() //nolint: gosec
		volumeID = id.String()
	}

	asked := req.GetCapacityRange().GetRequiredBytes()
	// Required==zero means unspecified by CSI spec, we create a small 4 Mbyte volume
	// as lvcreate does not allow zero size (csi-sanity creates zero-sized volumes)
	if asked <= 0 {
		asked = 4 * 1024 * 1024
	}
	topology := []*csi.Topology{}
	glog.Infof("CreateVolume: Name: %v req.Required: %v req.Limit: %v", req.Name, asked, req.GetCapacityRange().GetLimitBytes())

	glog.Infof("CreateVolume: Special create volume in Unified mode")
	if err := cs.dm.CreateDevice(volumeID, uint64(asked), nsmode); err != nil {
		return nil, status.Errorf(codes.Internal, "CreateVolume: failed to create volume: %s", err.Error())
	}
	topology = append(topology, &csi.Topology{
		Segments: map[string]string{
			"kubernetes.io/hostname": cs.Driver.nodeID,
		},
	})

	vol = &nodeVolume{
		ID:     volumeID,
		Name:   req.GetName(),
		Size:   asked,
		Erase:  eraseafter,
		NsMode: nsmode,
	}
	cs.pmemVolumes[volumeID] = vol
	glog.Infof("CreateVolume: Record new volume as %v", *vol)

	return &csi.CreateVolumeResponse{
		Volume: &csi.Volume{
			Id:                 vol.ID,
			CapacityBytes:      vol.Size,
			AccessibleTopology: topology,
		},
	}, nil
}

func (cs *nodeControllerServer) DeleteVolume(ctx context.Context, req *csi.DeleteVolumeRequest) (*csi.DeleteVolumeResponse, error) {

	// Check arguments
	if len(req.GetVolumeId()) == 0 {
		return nil, status.Error(codes.InvalidArgument, "Volume ID missing in request")
	}

	if err := cs.Driver.ValidateControllerServiceRequest(csi.ControllerServiceCapability_RPC_CREATE_DELETE_VOLUME); err != nil {
		pmemcommon.Infof(3, ctx, "invalid delete volume req: %v", req)
		return nil, err
	}

	// Serialize by VolumeId
	nodeVolumeMutex.LockKey(req.VolumeId)
	defer nodeVolumeMutex.UnlockKey(req.VolumeId)

	pmemcommon.Infof(4, ctx, "DeleteVolume: volumeID: %v", req.GetVolumeId())
	if vol := cs.getVolumeByID(req.GetVolumeId()); vol != nil {
		if err := cs.dm.DeleteDevice(req.VolumeId, vol.Erase); err != nil {
			return nil, status.Errorf(codes.Internal, "Failed to delete volume: %s", err.Error())
		}
		delete(cs.pmemVolumes, vol.ID)
		pmemcommon.Infof(4, ctx, "DeleteVolume: volume %s deleted", req.GetVolumeId())
	} else {
		pmemcommon.Infof(3, ctx, "Volume %s not created by this controller", req.GetVolumeId())
	}
	return &csi.DeleteVolumeResponse{}, nil
}

func (cs *nodeControllerServer) ValidateVolumeCapabilities(ctx context.Context, req *csi.ValidateVolumeCapabilitiesRequest) (*csi.ValidateVolumeCapabilitiesResponse, error) {

	// Check arguments
	if len(req.GetVolumeId()) == 0 {
		return nil, status.Error(codes.InvalidArgument, "Volume ID missing in request")
	}
	if req.GetVolumeCapabilities() == nil {
		return nil, status.Error(codes.InvalidArgument, "Volume capabilities missing in request")
	}

	vol := cs.getVolumeByID(req.GetVolumeId())
	if vol == nil {
		return nil, status.Error(codes.NotFound, "Volume not created by this controller")
	}
	for _, cap := range req.VolumeCapabilities {
		if cap.GetAccessMode().GetMode() != csi.VolumeCapability_AccessMode_SINGLE_NODE_WRITER {
			return &csi.ValidateVolumeCapabilitiesResponse{Supported: false, Message: ""}, nil
		}
	}
	return &csi.ValidateVolumeCapabilitiesResponse{Supported: true, Message: ""}, nil
}

func (cs *nodeControllerServer) ListVolumes(ctx context.Context, req *csi.ListVolumesRequest) (*csi.ListVolumesResponse, error) {
	pmemcommon.Infof(3, ctx, "ListVolumes")
	if err := cs.Driver.ValidateControllerServiceRequest(csi.ControllerServiceCapability_RPC_LIST_VOLUMES); err != nil {
		pmemcommon.Infof(3, ctx, "invalid list volumes req: %v", req)
		return nil, err
	}
	// List namespaces
	var entries []*csi.ListVolumesResponse_Entry
	for _, vol := range cs.pmemVolumes {
		info := &csi.Volume{
			Id:            vol.ID,
			CapacityBytes: int64(vol.Size),
		}
		entries = append(entries, &csi.ListVolumesResponse_Entry{
			Volume:               info,
			XXX_NoUnkeyedLiteral: *new(struct{}),
		})
	}

	response := &csi.ListVolumesResponse{
		Entries:              entries,
		NextToken:            "",
		XXX_NoUnkeyedLiteral: *new(struct{}),
	}
	return response, nil
}

func (cs *nodeControllerServer) GetCapacity(ctx context.Context, req *csi.GetCapacityRequest) (*csi.GetCapacityResponse, error) {
	var capacity int64

	cap, err := cs.dm.GetCapacity()
	if err != nil {
		return nil, status.Errorf(codes.Internal, err.Error())
	}

	nsmode := "fsdax"
	params := req.GetParameters()
	if params != nil {
		if mode, ok := params["nsmode"]; ok {
			nsmode = mode
		}
	}

	if c, ok := cap[nsmode]; ok {
		capacity = int64(c)
	} else {
		return nil, fmt.Errorf("Unknown namespace mode :%s", nsmode)
	}

	return &csi.GetCapacityResponse{
		AvailableCapacity: capacity,
	}, nil
}

func (cs *nodeControllerServer) getVolumeByID(volumeID string) *nodeVolume {
	if pmemVol, ok := cs.pmemVolumes[volumeID]; ok {
		return pmemVol
	}
	return nil
}
