/*
Copyright 2017 The Kubernetes Authors.

SPDX-License-Identifier: Apache-2.0
*/

package pmemcsidriver

import (
	"crypto/sha1"
	"encoding/hex"
	"fmt"
	"strconv"
	"sync"

	"golang.org/x/net/context"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"k8s.io/klog"

	"github.com/container-storage-interface/spec/lib/go/csi"

	pmdmanager "github.com/intel/pmem-csi/pkg/pmem-device-manager"
	pmemstate "github.com/intel/pmem-csi/pkg/pmem-state"
	"k8s.io/utils/keymutex"
)

const (
	pmemParameterKeyNamespaceMode = "nsmode"
	pmemParameterKeyEraseAfter    = "eraseafter"

	pmemNamespaceModeFsdax  = "fsdax"
	pmemNamespaceModeSector = "sector"
)

type nodeVolume struct {
	ID     string            `json:"id"`
	Size   int64             `json:"size"`
	Params map[string]string `json:"parameters"`
}

func (nv *nodeVolume) Copy() *nodeVolume {
	nvCopy := &nodeVolume{
		ID:     nv.ID,
		Size:   nv.Size,
		Params: map[string]string{},
	}

	for k, v := range nv.Params {
		nvCopy.Params[k] = v
	}

	return nvCopy
}

type nodeControllerServer struct {
	*DefaultControllerServer
	nodeID      string
	dm          pmdmanager.PmemDeviceManager
	sm          pmemstate.StateManager
	pmemVolumes map[string]*nodeVolume // map of reqID:nodeVolume
	mutex       sync.Mutex             // lock for pmemVolumes
}

var _ csi.ControllerServer = &nodeControllerServer{}
var _ PmemService = &nodeControllerServer{}

var nodeVolumeMutex = keymutex.NewHashed(-1)

func NewNodeControllerServer(nodeID string, dm pmdmanager.PmemDeviceManager, sm pmemstate.StateManager) *nodeControllerServer {
	serverCaps := []csi.ControllerServiceCapability_RPC_Type{
		csi.ControllerServiceCapability_RPC_CREATE_DELETE_VOLUME,
		csi.ControllerServiceCapability_RPC_LIST_VOLUMES,
		csi.ControllerServiceCapability_RPC_GET_CAPACITY,
	}

	ncs := &nodeControllerServer{
		DefaultControllerServer: NewDefaultControllerServer(serverCaps),
		nodeID:                  nodeID,
		dm:                      dm,
		sm:                      sm,
		pmemVolumes:             map[string]*nodeVolume{},
	}

	// Restore provisioned volumes from state.
	if sm != nil {
		// Get actual devices at DeviceManager
		devices, err := dm.ListDevices()
		if err != nil {
			klog.Warningf("Failed to get volumes: %s", err.Error())
		}
		cleanupList := []string{}
		v := &nodeVolume{}
		err = sm.GetAll(v, func(id string) bool {
			// See if the device data stored at StateManager is still valid
			for _, devInfo := range devices {
				if devInfo.Name == id {
					ncs.pmemVolumes[id] = v.Copy()
					return true
				}
			}
			// if not found in DeviceManager's list, add to cleanupList
			cleanupList = append(cleanupList, id)

			return true
		})
		if err != nil {
			klog.Warningf("Failed to load state on node %s: %s", nodeID, err.Error())
		}

		for _, id := range cleanupList {
			if err := sm.Delete(id); err != nil {
				klog.Warningf("Failed to delete stale volume %s from state : %s", id, err.Error())
			}
		}
	}

	return ncs
}

func (cs *nodeControllerServer) RegisterService(rpcServer *grpc.Server) {
	csi.RegisterControllerServer(rpcServer, cs)
}

func (cs *nodeControllerServer) CreateVolume(ctx context.Context, req *csi.CreateVolumeRequest) (*csi.CreateVolumeResponse, error) {
	var vol *nodeVolume
	topology := []*csi.Topology{}
	volumeID := ""
	nsmode := pmemNamespaceModeFsdax

	var resp *csi.CreateVolumeResponse

	if err := cs.ValidateControllerServiceRequest(csi.ControllerServiceCapability_RPC_CREATE_DELETE_VOLUME); err != nil {
		klog.Errorf("invalid create volume req: %v", req)
		return nil, err
	}

	if req.GetVolumeCapabilities() == nil {
		return nil, status.Error(codes.InvalidArgument, "Volume Capabilities missing in request")
	}

	if len(req.GetName()) == 0 {
		return nil, status.Error(codes.InvalidArgument, "Name missing in request")
	}

	nodeVolumeMutex.LockKey(req.Name)
	defer nodeVolumeMutex.UnlockKey(req.Name)

	params := req.GetParameters()
	if params != nil {
		if val, ok := params[pmemParameterKeyNamespaceMode]; ok {
			if val == pmemNamespaceModeFsdax || val == pmemNamespaceModeSector {
				nsmode = val
			}
		}
		if val, ok := params["_id"]; ok {
			/* use master controller provided volume uid */
			volumeID = val

			delete(params, "_id")
		}
	} else {
		params = map[string]string{}
	}

	// Keeping volume name as part of volume parameters, this helps to
	// persist volume name and to pass to Master via ListVolumes.
	params["Name"] = req.Name

	// VolumeID is hashed from Volume Name if not provided by master controller.
	// Hashing guarantees same ID for repeated requests.
	if volumeID == "" {
		hasher := sha1.New()
		hasher.Write([]byte(req.Name))
		volumeID = hex.EncodeToString(hasher.Sum(nil))
		klog.V(4).Infof("Node CreateVolume: Create SHA1 hash from name:%s to form id:%s", req.Name, volumeID)
	}

	asked := req.GetCapacityRange().GetRequiredBytes()

	if vol = cs.getVolumeByName(req.Name); vol != nil {
		// Check if the size of existing volume can cover the new request
		klog.V(4).Infof("Node CreateVolume: Volume exists, name:%s id:%s size:%v", req.Name, vol.ID, vol.Size)
		if vol.Size < asked {
			return nil, status.Error(codes.AlreadyExists, fmt.Sprintf("Smaller volume with the same name:%s already exists", req.Name))
		}
	} else {
		klog.V(4).Infof("CreateVolume: Name: %v req.Required: %v req.Limit: %v", req.Name, asked, req.GetCapacityRange().GetLimitBytes())
		vol = &nodeVolume{
			ID:     volumeID,
			Size:   asked,
			Params: params,
		}
		if cs.sm != nil {
			// Persist new volume state
			if err := cs.sm.Create(volumeID, vol); err != nil {
				return nil, status.Error(codes.Internal, err.Error())
			}
			defer func(id string) {
				// Incase of failure, remove volume from state
				if resp == nil {
					if err := cs.sm.Delete(id); err != nil {
						klog.Warningf("Delete volume state for id '%s' failed with error: %s", id, err.Error())
					}
				}
			}(volumeID)
		}

		if err := cs.dm.CreateDevice(volumeID, uint64(asked), nsmode); err != nil {
			return nil, status.Errorf(codes.Internal, "Node CreateVolume: failed to create volume: %s", err.Error())
		}

		cs.mutex.Lock()
		defer cs.mutex.Unlock()
		cs.pmemVolumes[volumeID] = vol
		klog.V(3).Infof("Node CreateVolume: Record new volume as %v", *vol)
	}

	topology = append(topology, &csi.Topology{
		Segments: map[string]string{
			PmemDriverTopologyKey: cs.nodeID,
		},
	})

	resp = &csi.CreateVolumeResponse{
		Volume: &csi.Volume{
			VolumeId:           vol.ID,
			CapacityBytes:      vol.Size,
			AccessibleTopology: topology,
		},
	}

	return resp, nil
}

func (cs *nodeControllerServer) DeleteVolume(ctx context.Context, req *csi.DeleteVolumeRequest) (*csi.DeleteVolumeResponse, error) {

	// Check arguments
	if len(req.GetVolumeId()) == 0 {
		return nil, status.Error(codes.InvalidArgument, "Volume ID missing in request")
	}

	if err := cs.ValidateControllerServiceRequest(csi.ControllerServiceCapability_RPC_CREATE_DELETE_VOLUME); err != nil {
		klog.Errorf("invalid delete volume req: %v", req)
		return nil, err
	}

	// Serialize by VolumeId
	nodeVolumeMutex.LockKey(req.VolumeId)
	defer nodeVolumeMutex.UnlockKey(req.VolumeId) //nolint: errcheck

	klog.V(4).Infof("Node DeleteVolume: volumeID: %v", req.GetVolumeId())
	eraseafter := true
	if vol := cs.getVolumeByID(req.VolumeId); vol != nil {
		// We recognize eraseafter=false/true, defaulting to true
		if val, ok := vol.Params[pmemParameterKeyEraseAfter]; ok {
			bVal, err := strconv.ParseBool(val)
			if err != nil {
				klog.Warningf("Ignoring invalid parameter %s:%s, reason: %s, falling back to default: %v",
					pmemParameterKeyEraseAfter, val, err.Error(), eraseafter)
			} else {
				eraseafter = bVal
			}
		}
	} else {
		return &csi.DeleteVolumeResponse{}, nil
	}

	if err := cs.dm.DeleteDevice(req.VolumeId, eraseafter); err != nil {
		return nil, status.Errorf(codes.Internal, "Failed to delete volume: %s", err.Error())
	}
	if cs.sm != nil {
		if err := cs.sm.Delete(req.VolumeId); err != nil {
			klog.Warning("Failed to remove volume from state: ", err)
		}
	}

	cs.mutex.Lock()
	defer cs.mutex.Unlock()
	delete(cs.pmemVolumes, req.VolumeId)

	klog.V(4).Infof("Node DeleteVolume: volume %s deleted", req.GetVolumeId())
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
	klog.V(5).Infof("ListVolumes")
	if err := cs.ValidateControllerServiceRequest(csi.ControllerServiceCapability_RPC_LIST_VOLUMES); err != nil {
		klog.Errorf("invalid list volumes req: %v", req)
		return nil, err
	}
	cs.mutex.Lock()
	defer cs.mutex.Unlock()
	// List namespaces
	var entries []*csi.ListVolumesResponse_Entry
	for _, vol := range cs.pmemVolumes {
		entries = append(entries, &csi.ListVolumesResponse_Entry{
			Volume: &csi.Volume{
				VolumeId:      vol.ID,
				CapacityBytes: vol.Size,
				VolumeContext: vol.Params,
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
	cs.mutex.Lock()
	defer cs.mutex.Unlock()
	if pmemVol, ok := cs.pmemVolumes[volumeID]; ok {
		return pmemVol
	}
	return nil
}

func (cs *nodeControllerServer) getVolumeByName(volumeName string) *nodeVolume {
	cs.mutex.Lock()
	defer cs.mutex.Unlock()
	for _, pmemVol := range cs.pmemVolumes {
		if pmemVol.Params["Name"] == volumeName {
			return pmemVol
		}
	}
	return nil
}

func (cs *nodeControllerServer) ControllerExpandVolume(context.Context, *csi.ControllerExpandVolumeRequest) (*csi.ControllerExpandVolumeResponse, error) {
	return nil, status.Error(codes.Unimplemented, "")
}
