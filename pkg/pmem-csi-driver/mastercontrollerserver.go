/*
Copyright 2017 The Kubernetes Authors.

SPDX-License-Identifier: Apache-2.0
*/

package pmemcsidriver

import (
	"fmt"

	"github.com/google/uuid"
	"github.com/pkg/errors"
	"golang.org/x/net/context"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"k8s.io/klog/glog"

	"github.com/container-storage-interface/spec/lib/go/csi/v0"

	pmemcommon "github.com/intel/pmem-csi/pkg/pmem-common"
	"k8s.io/utils/keymutex"
)

//VolumeStatus type representation for volume status
type VolumeStatus int

const (
	//Created volume created
	Created VolumeStatus = iota + 1
	//Attached volume attached on a node
	Attached
	//Unattached volume dettached on a node
	Unattached
	//Deleted volume deleted
	Deleted
)

type pmemVolume struct {
	// VolumeID published to outside world
	id string
	// Cached PV creation request to be used in volume attach phase
	req *csi.CreateVolumeRequest
	// current volume status
	status VolumeStatus
	// node id where the volume provisioned/attached
	nodeID string
}

type masterController struct {
	*DefaultControllerServer
	rs          *registryServer
	pmemVolumes map[string]*pmemVolume //map of reqID:pmemVolume
}

var _ csi.ControllerServer = &masterController{}
var _ PmemService = &masterController{}
var volumeMutex = keymutex.NewHashed(-1)

func NewMasterControllerServer(driver *CSIDriver, rs *registryServer) *masterController {
	serverCaps := []csi.ControllerServiceCapability_RPC_Type{}
	serverCaps = append(serverCaps, csi.ControllerServiceCapability_RPC_CREATE_DELETE_VOLUME)
	serverCaps = append(serverCaps, csi.ControllerServiceCapability_RPC_PUBLISH_UNPUBLISH_VOLUME)
	serverCaps = append(serverCaps, csi.ControllerServiceCapability_RPC_LIST_VOLUMES)
	serverCaps = append(serverCaps, csi.ControllerServiceCapability_RPC_GET_CAPACITY)
	driver.AddControllerServiceCapabilities(serverCaps)

	return &masterController{
		DefaultControllerServer: NewDefaultControllerServer(driver),
		rs:                      rs,
		pmemVolumes:             map[string]*pmemVolume{},
	}
}

func (cs *masterController) RegisterService(rpcServer *grpc.Server) {
	csi.RegisterControllerServer(rpcServer, cs)
}

func (cs *masterController) CreateVolume(ctx context.Context, req *csi.CreateVolumeRequest) (*csi.CreateVolumeResponse, error) {
	var vol *pmemVolume

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

	asked := req.GetCapacityRange().GetRequiredBytes()

	topology := []*csi.Topology{}
	glog.Infof("CreateVolume: Name: %v req.Required: %v req.Limit: %v", req.Name, asked, req.GetCapacityRange().GetLimitBytes())
	if vol = cs.getVolumeByName(req.Name); vol != nil {
		// Check if the size of existing volume can cover the new request
		glog.Infof("CreateVolume: Vol %s exists, Size: %v", req.Name, vol.req.GetCapacityRange().GetRequiredBytes())
		if vol.req.GetCapacityRange().GetRequiredBytes() < asked {
			return nil, status.Error(codes.AlreadyExists, fmt.Sprintf("Volume with the same name: %s but with different size already exist", req.Name))
		}
	} else {
		id, _ := uuid.NewUUID() //nolint: gosec
		volumeID := id.String()
		capReq := &csi.GetCapacityRequest{
			Parameters:         req.GetParameters(),
			VolumeCapabilities: req.GetVolumeCapabilities(),
		}
		foundNodes := []string{}
		for _, node := range cs.rs.nodeClients {
			cap, err := cs.getNodeCapacity(ctx, node, capReq)
			if err != nil {
				glog.Warningf("Error while fetching '%s' node capacity: %s", node.NodeID, err.Error())
				continue
			}
			glog.Infof("Node: %s - Capacity: %v", node, cap)
			if cap >= asked {
				glog.Infof("node %s has requested capacity", node.NodeID)
				foundNodes = append(foundNodes, node.NodeID)
			}
		}

		if len(foundNodes) == 0 {
			return nil, status.Error(codes.Unavailable, fmt.Sprintf("No node found with %v capacity", asked))
		}

		glog.Infof("Found nodes: %v", foundNodes)

		for _, id := range foundNodes {
			topology = append(topology, &csi.Topology{
				Segments: map[string]string{
					"kubernetes.io/hostname": id,
				},
			})
		}

		vol = &pmemVolume{
			id:     volumeID,
			req:    req,
			status: Created,
		}
		cs.pmemVolumes[volumeID] = vol
		glog.Infof("CreateVolume: Record new volume as %v", *vol)
	}

	return &csi.CreateVolumeResponse{
		Volume: &csi.Volume{
			Id:                 vol.id,
			CapacityBytes:      asked,
			AccessibleTopology: topology,
		},
	}, nil
}

func (cs *masterController) DeleteVolume(ctx context.Context, req *csi.DeleteVolumeRequest) (*csi.DeleteVolumeResponse, error) {

	// Check arguments
	if len(req.GetVolumeId()) == 0 {
		return nil, status.Error(codes.InvalidArgument, "Volume ID missing in request")
	}

	if err := cs.Driver.ValidateControllerServiceRequest(csi.ControllerServiceCapability_RPC_CREATE_DELETE_VOLUME); err != nil {
		pmemcommon.Infof(3, ctx, "invalid delete volume req: %v", req)
		return nil, err
	}

	// Serialize by VolumeId
	volumeMutex.LockKey(req.VolumeId)
	defer volumeMutex.UnlockKey(req.VolumeId)

	pmemcommon.Infof(4, ctx, "DeleteVolume: volumeID: %v", req.GetVolumeId())
	if vol := cs.getVolumeByID(req.GetVolumeId()); vol != nil {
		if vol.status != Unattached {
			pmemcommon.Infof(3, ctx, "Volume %s is attached to %s but not detached", vol.req.Name, vol.nodeID)
		}

		conn, err := cs.rs.ConnectToNodeController(vol.nodeID, connectionTimeout)
		if err != nil {
			return nil, status.Error(codes.Internal, err.Error())
		}
		defer conn.Close()

		csiClient := csi.NewControllerClient(conn)
		clientReq := &csi.DeleteVolumeRequest{
			VolumeId: vol.id,
		}
		if res, err := csiClient.DeleteVolume(ctx, clientReq); err != nil {
			return res, err
		}
		delete(cs.pmemVolumes, vol.id)
		pmemcommon.Infof(4, ctx, "DeleteVolume: volume %s deleted", req.GetVolumeId())
	} else {
		pmemcommon.Infof(3, ctx, "Volume %s not created by this controller", req.GetVolumeId())
	}
	return &csi.DeleteVolumeResponse{}, nil
}

func (cs *masterController) ValidateVolumeCapabilities(ctx context.Context, req *csi.ValidateVolumeCapabilitiesRequest) (*csi.ValidateVolumeCapabilitiesResponse, error) {

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

func (cs *masterController) ListVolumes(ctx context.Context, req *csi.ListVolumesRequest) (*csi.ListVolumesResponse, error) {
	pmemcommon.Infof(3, ctx, "ListVolumes")
	if err := cs.Driver.ValidateControllerServiceRequest(csi.ControllerServiceCapability_RPC_LIST_VOLUMES); err != nil {
		pmemcommon.Infof(3, ctx, "invalid list volumes req: %v", req)
		return nil, err
	}
	// List namespaces
	var entries []*csi.ListVolumesResponse_Entry
	for _, vol := range cs.pmemVolumes {
		info := &csi.Volume{
			Id:            vol.id,
			CapacityBytes: int64(vol.req.CapacityRange.RequiredBytes),
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

func (cs *masterController) ControllerPublishVolume(ctx context.Context, req *csi.ControllerPublishVolumeRequest) (*csi.ControllerPublishVolumeResponse, error) {
	if req.GetVolumeId() == "" {
		return nil, status.Error(codes.InvalidArgument, "ControllerPublishVolume Volume ID must be provided")
	}

	if req.GetNodeId() == "" {
		return nil, status.Error(codes.InvalidArgument, "ControllerPublishVolume Node ID must be provided")
	}

	if req.GetVolumeCapability() == nil {
		return nil, status.Error(codes.InvalidArgument, "ControllerPublishVolume Volume capability must be provided")
	}

	// Serialize by VolumeId
	volumeMutex.LockKey(req.VolumeId)
	defer volumeMutex.UnlockKey(req.VolumeId)

	glog.Infof("ControllerPublishVolume: cs.Node: %s req.volume_id: %s, req.node_id: %s ", cs.Driver.nodeID, req.VolumeId, req.NodeId)

	vol := cs.getVolumeByID(req.VolumeId)
	if vol == nil {
		return nil, status.Error(codes.NotFound, fmt.Sprintf("No volume with id '%s' found", req.VolumeId))
	}

	glog.Infof("Current volume Info: %+v", vol)

	if vol.status == Attached {
		if req.NodeId == vol.nodeID {
			return &csi.ControllerPublishVolumeResponse{}, nil
		} else {
			return nil, status.Error(codes.FailedPrecondition, fmt.Sprintf("Volume already attached on %s", vol.nodeID))
		}
	} else if vol.status == Unattached {
		if req.NodeId == vol.nodeID {
			// Do we need to clear data on reattachment on same node??
			return &csi.ControllerPublishVolumeResponse{}, nil
		} else {
			// Delete stale volume on previously attached node
			conn, err := cs.rs.ConnectToNodeController(vol.nodeID, connectionTimeout)
			if err != nil {
				return nil, status.Error(codes.Internal, err.Error())
			}
			defer conn.Close()

			csiClient := csi.NewControllerClient(conn)
			clientReq := &csi.DeleteVolumeRequest{
				VolumeId: vol.id,
			}
			if _, err := csiClient.DeleteVolume(ctx, clientReq); err != nil {
				return nil, err
			}
		}
	}

	// Its new attachment, create pmem volume on node
	conn, err := cs.rs.ConnectToNodeController(req.NodeId, connectionTimeout)
	if err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}
	defer conn.Close()

	csiClient := csi.NewControllerClient(conn)

	if vol.req.Parameters == nil {
		vol.req.Parameters = map[string]string{}
	}
	// Ask node to use existing volume id
	vol.req.Parameters["_id"] = vol.id
	if _, err = csiClient.CreateVolume(ctx, vol.req); err != nil {
		return nil, status.Error(codes.Internal, errors.Wrap(err, "Attach volume failure").Error())
	}

	vol.status = Attached
	vol.nodeID = req.NodeId

	return &csi.ControllerPublishVolumeResponse{}, nil
}

func (cs *masterController) ControllerUnpublishVolume(ctx context.Context, req *csi.ControllerUnpublishVolumeRequest) (*csi.ControllerUnpublishVolumeResponse, error) {
	if req.GetVolumeId() == "" {
		return nil, status.Error(codes.InvalidArgument, "ControllerPublishVolume Volume ID must be provided")
	}

	// Serialize by VolumeId
	volumeMutex.LockKey(req.VolumeId)
	defer volumeMutex.UnlockKey(req.VolumeId)

	glog.Infof("ControllerUnpublishVolume : volume_id: %s, node_id: %s ", req.VolumeId, req.NodeId)

	if vol := cs.getVolumeByID(req.VolumeId); vol != nil {
		vol.status = Unattached
	}

	return &csi.ControllerUnpublishVolumeResponse{}, nil
}

func (cs *masterController) GetCapacity(ctx context.Context, req *csi.GetCapacityRequest) (*csi.GetCapacityResponse, error) {
	var capacity int64

	for _, node := range cs.rs.nodeClients {
		cap, err := cs.getNodeCapacity(ctx, node, req)
		if err != nil {
			glog.Warningf("Error while fetching '%s' node capacity: %s", node.NodeID, err.Error())
			continue
		}
		capacity += cap
	}

	return &csi.GetCapacityResponse{
		AvailableCapacity: capacity,
	}, nil
}

func (cs *masterController) getNodeCapacity(ctx context.Context, node NodeInfo, req *csi.GetCapacityRequest) (int64, error) {
	conn, err := cs.rs.ConnectToNodeController(node.NodeID, connectionTimeout)
	if err != nil {
		return 0, fmt.Errorf("failed to connect to node %s: %s", node.NodeID, err.Error())
	}

	csiClient := csi.NewControllerClient(conn)
	resp, err := csiClient.GetCapacity(ctx, req)
	if err != nil {
		return 0, fmt.Errorf("Error while fetching '%s' node capacity: %s", node.NodeID, err.Error())
	}

	return resp.AvailableCapacity, nil
}

func (cs *masterController) getVolumeByID(volumeID string) *pmemVolume {
	if pmemVol, ok := cs.pmemVolumes[volumeID]; ok {
		return pmemVol
	}
	return nil
}

func (cs *masterController) getVolumeByName(Name string) *pmemVolume {
	for _, pmemVol := range cs.pmemVolumes {
		if pmemVol.req.Name == Name {
			return pmemVol
		}
	}
	return nil
}
