/*
Copyright 2017 The Kubernetes Authors.

SPDX-License-Identifier: Apache-2.0
*/

package pmemcsidriver

import (
	"fmt"
	"strconv"

	"github.com/google/uuid"
	"golang.org/x/net/context"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"k8s.io/klog/glog"

	"github.com/container-storage-interface/spec/lib/go/csi"

	pmdmanager "github.com/intel/pmem-csi/pkg/pmem-device-manager"
	"k8s.io/utils/keymutex"
)

const (
	pmemParameterKeyNamespaceMode = "nsmode"
	pmemParameterKeyEraseAfter    = "eraseafter"

	pmemNamespaceModeFsdax  = "fsdax"
	pmemNamespaceModeSector = "sector"
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
	nodeID      string
	dm          pmdmanager.PmemDeviceManager
	pmemVolumes map[string]*nodeVolume // map of reqID:nodeVolume
}

var _ csi.ControllerServer = &nodeControllerServer{}
var _ PmemService = &nodeControllerServer{}

var nodeVolumeMutex = keymutex.NewHashed(-1)

func NewNodeControllerServer(nodeID string, dm pmdmanager.PmemDeviceManager) *nodeControllerServer {
	serverCaps := []csi.ControllerServiceCapability_RPC_Type{
		csi.ControllerServiceCapability_RPC_CREATE_DELETE_VOLUME,
		csi.ControllerServiceCapability_RPC_LIST_VOLUMES,
		csi.ControllerServiceCapability_RPC_GET_CAPACITY,
	}
	return &nodeControllerServer{
		DefaultControllerServer: NewDefaultControllerServer(serverCaps),
		nodeID:                  nodeID,
		dm:                      dm,
		pmemVolumes:             map[string]*nodeVolume{},
	}
}

func (cs *nodeControllerServer) RegisterService(rpcServer *grpc.Server) {
	csi.RegisterControllerServer(rpcServer, cs)
}

func (cs *nodeControllerServer) CreateVolume(ctx context.Context, req *csi.CreateVolumeRequest) (*csi.CreateVolumeResponse, error) {
	var vol *nodeVolume
	topology := []*csi.Topology{}
	volumeID := ""
	eraseafter := true
	nsmode := pmemNamespaceModeFsdax

	if err := cs.ValidateControllerServiceRequest(csi.ControllerServiceCapability_RPC_CREATE_DELETE_VOLUME); err != nil {
		glog.Errorf("invalid create volume req: %v", req)
		return nil, err
	}

	if req.GetVolumeCapabilities() == nil {
		return nil, status.Error(codes.InvalidArgument, "Volume Capabilities missing in request")
	}

	if len(req.GetName()) == 0 {
		return nil, status.Error(codes.InvalidArgument, "Name missing in request")
	}

	// We recognize eraseafter=false/true, defaulting to true
	if params := req.GetParameters(); params != nil {
		if val, ok := params[pmemParameterKeyEraseAfter]; ok {
			if bVal, err := strconv.ParseBool(val); err == nil {
				eraseafter = bVal
			} else {
				glog.Warningf("Ignoring parameter %s:%s, reason: %s", pmemParameterKeyEraseAfter, val, err.Error())
			}
		}
		if val, ok := params[pmemParameterKeyNamespaceMode]; ok {
			if val == pmemNamespaceModeFsdax || val == pmemNamespaceModeSector {
				nsmode = val
			} else {
				glog.Warningf("Ignoring parameter %s:%s, reason: unknown namespace mode", pmemParameterKeyNamespaceMode, val)
			}
		}
		if val, ok := params["_id"]; ok {
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
	// FIXME(avalluri): This check should be handled by LvmDeviceManager??
	if asked <= 0 {
		asked = 4 * 1024 * 1024
	}

	if vol = cs.getVolumeByName(req.Name); vol != nil {
		// Check if the size of existing volume can cover the new request
		glog.V(4).Infof("CreateVolume: Vol %s exists, Size: %v", req.Name, vol.Size)
		if vol.Size < asked {
			return nil, status.Error(codes.AlreadyExists, fmt.Sprintf("Volume with the same name: %s but with different size already exist", req.Name))
		}
	} else {
		glog.V(4).Infof("CreateVolume: Name: %v req.Required: %v req.Limit: %v", req.Name, asked, req.GetCapacityRange().GetLimitBytes())

		if err := cs.dm.CreateDevice(volumeID, uint64(asked), nsmode); err != nil {
			return nil, status.Errorf(codes.Internal, "CreateVolume: failed to create volume: %s", err.Error())
		}
		vol = &nodeVolume{
			ID:     volumeID,
			Name:   req.GetName(),
			Size:   asked,
			Erase:  eraseafter,
			NsMode: nsmode,
		}
		cs.pmemVolumes[volumeID] = vol
		glog.V(3).Infof("CreateVolume: Record new volume as %v", *vol)
	}

	topology = append(topology, &csi.Topology{
		Segments: map[string]string{
			PmemDriverTopologyKey: cs.nodeID,
		},
	})

	return &csi.CreateVolumeResponse{
		Volume: &csi.Volume{
			VolumeId:           vol.ID,
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

	if err := cs.ValidateControllerServiceRequest(csi.ControllerServiceCapability_RPC_CREATE_DELETE_VOLUME); err != nil {
		glog.Errorf("invalid delete volume req: %v", req)
		return nil, err
	}

	// Serialize by VolumeId
	nodeVolumeMutex.LockKey(req.VolumeId)
	defer nodeVolumeMutex.UnlockKey(req.VolumeId) //nolint: errcheck

	glog.V(4).Infof("DeleteVolume: volumeID: %v", req.GetVolumeId())
	if vol := cs.getVolumeByID(req.GetVolumeId()); vol != nil {
		if err := cs.dm.DeleteDevice(req.VolumeId, vol.Erase); err != nil {
			return nil, status.Errorf(codes.Internal, "Failed to delete volume: %s", err.Error())
		}
		delete(cs.pmemVolumes, vol.ID)
		glog.V(4).Infof("DeleteVolume: volume %s deleted", req.GetVolumeId())
	} else {
		glog.V(3).Infof("Volume %s not created by this controller", req.GetVolumeId())
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
			return &csi.ValidateVolumeCapabilitiesResponse{
				Confirmed: nil,
				Message:   "Driver does not support '" + cap.AccessMode.Mode.String() + "' mode",
			}, nil
		}
	}
	return &csi.ValidateVolumeCapabilitiesResponse{
		Confirmed: &csi.ValidateVolumeCapabilitiesResponse_Confirmed{
			VolumeCapabilities: req.VolumeCapabilities,
			VolumeContext:      req.GetVolumeContext(),
		},
	}, nil
}

func (cs *nodeControllerServer) ListVolumes(ctx context.Context, req *csi.ListVolumesRequest) (*csi.ListVolumesResponse, error) {
	glog.V(5).Infof("ListVolumes")
	if err := cs.ValidateControllerServiceRequest(csi.ControllerServiceCapability_RPC_LIST_VOLUMES); err != nil {
		glog.Errorf("invalid list volumes req: %v", req)
		return nil, err
	}
	// List namespaces
	var entries []*csi.ListVolumesResponse_Entry
	for _, vol := range cs.pmemVolumes {
		entries = append(entries, &csi.ListVolumesResponse_Entry{
			Volume: &csi.Volume{
				VolumeId:      vol.ID,
				CapacityBytes: vol.Size,
			},
		})
	}

	return &csi.ListVolumesResponse{
		Entries: entries,
	}, nil
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
		if mode, ok := params[pmemParameterKeyNamespaceMode]; ok {
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

func (cs *nodeControllerServer) getVolumeByName(volumeName string) *nodeVolume {
	for _, pmemVol := range cs.pmemVolumes {
		if pmemVol.Name == volumeName {
			return pmemVol
		}
	}
	return nil
}

func (cs *nodeControllerServer) ControllerExpandVolume(context.Context, *csi.ControllerExpandVolumeRequest) (*csi.ControllerExpandVolumeResponse, error) {
	return nil, status.Error(codes.Unimplemented, "")
}
