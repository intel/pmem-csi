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
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"k8s.io/klog/glog"

	"github.com/container-storage-interface/spec/lib/go/csi/v0"

	"github.com/intel/pmem-csi/pkg/pmem-common"
	pmdmanager "github.com/intel/pmem-csi/pkg/pmem-device-manager"
	"github.com/intel/pmem-csi/pkg/pmem-grpc"
	"k8s.io/kubernetes/pkg/util/keymutex" // TODO: move to k8s.io/utils (https://github.com/kubernetes/utils/issues/62)
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
	ID     string
	Name   string
	Size   uint64
	Status VolumeStatus
	NodeID string
	Erase  bool
	NsMode string
}

type controllerServer struct {
	*DefaultControllerServer
	mode              DriverMode
	rs                *registryServer
	dm                pmdmanager.PmemDeviceManager
	pmemVolumes       map[string]pmemVolume //Controller: map of reqID:VolumeInfo
	publishVolumeInfo map[string]string     //Node: map of reqID:VolumeName
}

var _ csi.ControllerServer = &controllerServer{}
var volumeMutex = keymutex.NewHashed(-1)

func NewControllerServer(driver *CSIDriver, mode DriverMode, rs *registryServer, dm pmdmanager.PmemDeviceManager) csi.ControllerServer {
	serverCaps := []csi.ControllerServiceCapability_RPC_Type{}
	switch mode {
	case Controller:
		serverCaps = append(serverCaps, csi.ControllerServiceCapability_RPC_CREATE_DELETE_VOLUME)
		serverCaps = append(serverCaps, csi.ControllerServiceCapability_RPC_PUBLISH_UNPUBLISH_VOLUME)
		serverCaps = append(serverCaps, csi.ControllerServiceCapability_RPC_LIST_VOLUMES)
	case Node:
		serverCaps = append(serverCaps, csi.ControllerServiceCapability_RPC_PUBLISH_UNPUBLISH_VOLUME)
	case Unified:
		serverCaps = append(serverCaps, csi.ControllerServiceCapability_RPC_CREATE_DELETE_VOLUME)
		serverCaps = append(serverCaps, csi.ControllerServiceCapability_RPC_LIST_VOLUMES)
	}
	driver.AddControllerServiceCapabilities(serverCaps)

	return &controllerServer{
		DefaultControllerServer: NewDefaultControllerServer(driver),
		mode:              mode,
		rs:                rs,
		dm:                dm,
		pmemVolumes:       map[string]pmemVolume{},
		publishVolumeInfo: map[string]string{},
	}
}

func (cs *controllerServer) GetVolumeByID(volumeID string) *pmemVolume {
	if pmemVol, ok := cs.pmemVolumes[volumeID]; ok {
		return &pmemVol
	}
	return nil
}

func (cs *controllerServer) GetVolumeByName(Name string) *pmemVolume {
	for _, pmemVol := range cs.pmemVolumes {
		if pmemVol.Name == Name {
			return &pmemVol
		}
	}
	return nil
}

func (cs *controllerServer) CreateVolume(ctx context.Context, req *csi.CreateVolumeRequest) (*csi.CreateVolumeResponse, error) {
	var vol *pmemVolume
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
	// Process plugin-specific parameters given as key=value pairs
	params := req.GetParameters()
	if params != nil {
		glog.Infof("CreateVolume: parameters detected")
		// We recognize eraseafter=false/true, defaulting to true
		for key, val := range params {
			glog.Infof("CreateVolume: seeing param: [%v] [%v]", key, val)
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
			}
		}
	}

	asked := uint64(req.GetCapacityRange().GetRequiredBytes())
	// Required==zero means unspecified by CSI spec, we create a small 4 Mbyte volume
	// as lvcreate does not allow zero size (csi-sanity creates zero-sized volumes)
	if asked == 0 {
		asked = 4 * 1024 * 1024
	}

	glog.Infof("CreateVolume: Name: %v, req.Required: %v req.Limit; %v", req.Name, asked, req.GetCapacityRange().GetLimitBytes())
	if vol = cs.GetVolumeByName(req.Name); vol != nil {
		// Check if the size of exisiting volume new can cover the new request
		glog.Infof("CreateVolume: Vol %s exists, Size: %v", vol.Name, vol.Size)
		if vol.Size < asked {
			return nil, status.Error(codes.AlreadyExists, fmt.Sprintf("Volume with the same name: %s but with different size already exist", vol.Name))
		}
	} else {
		id, _ := uuid.NewUUID() //nolint: gosec
		volumeID := id.String()
		if cs.mode == Unified || cs.mode == Node {
			glog.Infof("CreateVolume: Special create volume in Unified mode")
			if err := cs.dm.CreateDevice(volumeID, asked, nsmode); err != nil {
				return nil, status.Errorf(codes.Internal, "CreateVolume: failed to create volume: %s", err.Error())
			}
		} else /*if cs.mode == Unified */ {
			foundNode := false
			for _, nodeInfo := range cs.rs.nodeClients {
				if nodeInfo.Capacity[nsmode] >= asked {
					foundNode = true
					break
				}
			}

			if !foundNode {
				return nil, status.Error(codes.Unavailable, fmt.Sprintf("No node found with %v capacaity", asked))
			}
		}

		vol = &pmemVolume{
			ID:     volumeID,
			Name:   req.GetName(),
			Size:   asked,
			Status: Created,
			Erase:  eraseafter,
			NsMode: nsmode,
		}
		if cs.mode == Unified || cs.mode == Node {
			vol.Status = Attached
		}
		cs.pmemVolumes[volumeID] = *vol
		glog.Infof("CreateVolume: Record new volume as [%v]", *vol)
	}

	return &csi.CreateVolumeResponse{
		Volume: &csi.Volume{
			Id:            vol.ID,
			CapacityBytes: int64(vol.Size),
			Attributes: map[string]string{
				"name":   req.GetName(),
				"size":   strconv.FormatUint(asked, 10),
				"nsmode": nsmode,
			},
		},
	}, nil
}

func (cs *controllerServer) DeleteVolume(ctx context.Context, req *csi.DeleteVolumeRequest) (*csi.DeleteVolumeResponse, error) {

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
	vol := cs.GetVolumeByID(req.GetVolumeId())
	if vol == nil {
		pmemcommon.Infof(3, ctx, "Volume %s not created by this controller", req.GetVolumeId())
	} else {
		if vol.Status != Unattached {
			pmemcommon.Infof(3, ctx, "Volume %s is attached to %s but not dittached", vol.Name, vol.NodeID)
		}
		if cs.mode == Unified || cs.mode == Node {
			glog.Infof("DeleteVolume: Special Delete in Unified mode")
			if vol, ok := cs.pmemVolumes[req.GetVolumeId()]; ok {
				if err := cs.dm.DeleteDevice(req.VolumeId, vol.Erase); err != nil {
					return nil, status.Errorf(codes.Internal, "Failed to delete volume: %s", err.Error())
				}
			}
		}
		delete(cs.pmemVolumes, vol.ID)
		pmemcommon.Infof(4, ctx, "DeleteVolume: volume deleted %s", req.GetVolumeId())
	}
	return &csi.DeleteVolumeResponse{}, nil
}

func (cs *controllerServer) ValidateVolumeCapabilities(ctx context.Context, req *csi.ValidateVolumeCapabilitiesRequest) (*csi.ValidateVolumeCapabilitiesResponse, error) {

	// Check arguments
	if len(req.GetVolumeId()) == 0 {
		return nil, status.Error(codes.InvalidArgument, "Volume ID missing in request")
	}
	if req.GetVolumeCapabilities() == nil {
		return nil, status.Error(codes.InvalidArgument, "Volume capabilities missing in request")
	}

	vol := cs.GetVolumeByID(req.GetVolumeId())
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

func (cs *controllerServer) ListVolumes(ctx context.Context, req *csi.ListVolumesRequest) (*csi.ListVolumesResponse, error) {
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
			Attributes: map[string]string{
				"name": vol.Name,
				"size": strconv.FormatUint(vol.Size, 10),
			},
		}
		entries = append(entries, &csi.ListVolumesResponse_Entry{
			Volume:               info,
			XXX_NoUnkeyedLiteral: *new(struct{}),
			XXX_unrecognized:     nil,
			XXX_sizecache:        0,
		})
	}

	response := &csi.ListVolumesResponse{
		Entries:              entries,
		NextToken:            "",
		XXX_NoUnkeyedLiteral: *new(struct{}),
	}
	return response, nil
}

func (cs *controllerServer) ControllerPublishVolume(ctx context.Context, req *csi.ControllerPublishVolumeRequest) (*csi.ControllerPublishVolumeResponse, error) {
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

	if cs.mode == Controller {
		vol, ok := cs.pmemVolumes[req.VolumeId]
		if !ok {
			return nil, status.Error(codes.NotFound, fmt.Sprintf("No volume with id '%s' found", req.VolumeId))
		}
		node, err := cs.rs.GetNodeController(req.NodeId)
		if err != nil {
			return nil, status.Error(codes.NotFound, err.Error())
		}

		conn, err := pmemgrpc.Connect(node.Endpoint, connectionTimeout)
		if err != nil {
			return nil, status.Error(codes.Internal, err.Error())
		}
		defer conn.Close()

		glog.Infof("Getting New Controller Client ....")
		csiClient := csi.NewControllerClient(conn)
		glog.Infof("Initiating Publishing volume ....")

		req.VolumeAttributes["name"] = vol.Name
		req.VolumeAttributes["size"] = strconv.FormatUint(vol.Size, 10)
		req.VolumeAttributes["nsmode"] = vol.NsMode
		resp, err := csiClient.ControllerPublishVolume(ctx, req)
		glog.Infof("Got response")

		if err == nil {
			vol.Status = Attached
			vol.NodeID = req.NodeId
			// Update node capacity
			// FIXME: Does this update should done via Registry API?
			// like RegistryServer.UpdateCapacity(uint64 request)
			cs.rs.UpdateNodeCapacity(node.NodeID, vol.NsMode, node.Capacity[vol.NsMode]-vol.Size)
		}
		return resp, err
	}

	if req.GetNodeId() != cs.Driver.nodeID {
		// if asked nodeID does not match ours, return NotFound error
		return nil, status.Error(codes.NotFound, "Node not found")
	}
	var volumeName string
	var volumeSize uint64
	var nsmode string
	if cs.mode == Node {
		attrs := req.GetVolumeAttributes()
		if attrs == nil {
			return nil, status.Error(codes.InvalidArgument, "Volume attribultes must be provided")
		}
		volumeName = attrs["name"]
		volumeSize, _ = strconv.ParseUint(attrs["size"], 10, 64)
		nsmode = attrs["nsmode"]
	} else /* if cs.mode == Unified */ {
		vol, ok := cs.pmemVolumes[req.VolumeId]
		if !ok {
			return nil, status.Error(codes.NotFound, fmt.Sprintf("No volume with id '%s' found", req.VolumeId))
		}
		volumeName = vol.Name
		volumeSize = vol.Size
		nsmode = vol.NsMode
	}
	// Check have we already published this volume.
	// We may get called with same VolumeId repeatedly in short time
	vol, published := cs.publishVolumeInfo[req.VolumeId]
	if published {
		glog.Infof("ControllerPublishVolume: Name:%v Id:%v already published, skip creation", vol, req.VolumeId)
	} else {
		glog.Infof("ControllerPublishVolume: volumeName:%v volumeSize:%v nsmode:%v", volumeName, volumeSize, nsmode)
		/* Node/Unified */
		if err := cs.dm.CreateDevice(req.VolumeId, volumeSize, nsmode); err != nil {
			return nil, status.Errorf(codes.Internal, "Failed to create volume: %s", err.Error())
		}

		cs.publishVolumeInfo[req.VolumeId] = volumeName
	}

	return &csi.ControllerPublishVolumeResponse{
		PublishInfo: map[string]string{
			"name": volumeName,
			"size": strconv.FormatUint(volumeSize, 10),
		},
	}, nil
}

func (cs *controllerServer) ControllerUnpublishVolume(ctx context.Context, req *csi.ControllerUnpublishVolumeRequest) (*csi.ControllerUnpublishVolumeResponse, error) {
	if req.GetVolumeId() == "" {
		return nil, status.Error(codes.InvalidArgument, "ControllerPublishVolume Volume ID must be provided")
	}

	// Serialize by VolumeId
	volumeMutex.LockKey(req.VolumeId)
	defer volumeMutex.UnlockKey(req.VolumeId)

	glog.Infof("ControllerUnpublishVolume : volume_id: %s, node_id: %s ", req.VolumeId, req.NodeId)

	if cs.mode == Controller {
		node, err := cs.rs.GetNodeController(req.NodeId)
		if err != nil {
			return nil, status.Error(codes.NotFound, err.Error())
		}
		conn, errr := pmemgrpc.Connect(node.Endpoint, connectionTimeout)
		if errr != nil {
			return nil, status.Error(codes.Internal, errr.Error())
		}

		csiClient := csi.NewControllerClient(conn)
		resp, err := csiClient.ControllerUnpublishVolume(ctx, req)
		if err == nil {
			if vol, ok := cs.pmemVolumes[req.GetVolumeId()]; ok {
				vol.Status = Unattached
				// Update node capacity
				// FIXME: Does this should be invoked from node controller?
				// like RegistryServer.UpdateNodeCapacity(uint64 request)
				cs.rs.UpdateNodeCapacity(node.NodeID, vol.NsMode, node.Capacity[vol.NsMode]+vol.Size)
			}
		}

		return resp, err
	}

	/* Node */

	// eraseafter was possibly given as plugin-specific option,
	// it was stored in the pmemVolumes entry during Create
	erase := true
	if vol, ok := cs.pmemVolumes[req.GetVolumeId()]; ok {
		glog.Infof("ControllerUnpublish: use stored EraseAfter: %v", vol.Erase)
		erase = vol.Erase
	}
	if err := cs.dm.DeleteDevice(req.GetVolumeId(), erase); err != nil {
		return nil, status.Errorf(codes.Internal, "Failed to delete volume: %s", err.Error())
	}

	delete(cs.publishVolumeInfo, req.GetVolumeId())

	return &csi.ControllerUnpublishVolumeResponse{}, nil
}
