/*
Copyright 2017 The Kubernetes Authors.

SPDX-License-Identifier: Apache-2.0
*/

package pmemcsidriver

import (
	"errors"
	"fmt"
	"sync"

	"golang.org/x/net/context"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"k8s.io/klog"

	"github.com/container-storage-interface/spec/lib/go/csi"

	grpcserver "github.com/intel/pmem-csi/pkg/grpc-server"
	"github.com/intel/pmem-csi/pkg/pmem-csi-driver/parameters"
	pmdmanager "github.com/intel/pmem-csi/pkg/pmem-device-manager"
	pmemstate "github.com/intel/pmem-csi/pkg/pmem-state"
	"k8s.io/utils/keymutex"
)

type nodeVolume struct {
	ID     string            `json:"id"`
	Size   int64             `json:"size"`
	Params map[string]string `json:"parameters"`
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
var _ grpcserver.Service = &nodeControllerServer{}

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
			klog.Warningf("Failed to get volumes: %v", err)
		}
		cleanupList := []string{}
		ids, err := sm.GetAll()
		if err != nil {
			klog.Warningf("Failed to load state: %v", err)
		}

		for _, id := range ids {
			// retrieve volume info
			vol := &nodeVolume{}
			if err := sm.Get(id, vol); err != nil {
				klog.Warningf("Failed to retrieve volume info for id %q from state: %v", id, err)
				continue
			}
			v, err := parameters.Parse(parameters.NodeVolumeOrigin, vol.Params)
			if err != nil {
				klog.Warningf("Failed to parse volume parameters for volume %q: %v", id, err)
				continue
			}

			found := false
			if v.GetDeviceMode() != dm.GetMode() {
				dm, err := newDeviceManager(v.GetDeviceMode())
				if err != nil {
					klog.Warningf("Failed to initialize device manager for state volume '%s'(volume mode: '%s'): %v", id, v.GetDeviceMode(), err)
					continue
				}

				if _, err := dm.GetDevice(id); err == nil {
					found = true
				} else if !errors.Is(err, pmdmanager.ErrDeviceNotFound) {
					klog.Warningf("Failed to fetch device for state volume '%s'(volume mode: '%s'): %v", id, v.GetDeviceMode(), err)
					// Let's ignore this volume
					continue
				}
			} else {
				// See if the device data stored at StateManager is still valid
				for _, devInfo := range devices {
					if devInfo.VolumeId == id {
						found = true
						break
					}
				}
			}

			if found {
				ncs.pmemVolumes[id] = vol
			} else {
				// if not found in DeviceManager's list, add to cleanupList
				cleanupList = append(cleanupList, id)
			}
		}

		for _, id := range cleanupList {
			if err := sm.Delete(id); err != nil {
				klog.Warningf("Failed to delete stale volume %s from state: %s", id, err.Error())
			}
		}
	}

	return ncs
}

func (cs *nodeControllerServer) RegisterService(rpcServer *grpc.Server) {
	csi.RegisterControllerServer(rpcServer, cs)
}

func (cs *nodeControllerServer) CreateVolume(ctx context.Context, req *csi.CreateVolumeRequest) (*csi.CreateVolumeResponse, error) {
	topology := []*csi.Topology{}

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

	p, err := parameters.Parse(parameters.CreateVolumeOrigin, req.GetParameters())
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, "persistent volume: "+err.Error())
	}

	nodeVolumeMutex.LockKey(req.Name)
	defer nodeVolumeMutex.UnlockKey(req.Name)

	volumeID, size, err := cs.createVolumeInternal(ctx,
		p,
		req.Name,
		req.GetVolumeCapabilities(),
		req.GetCapacityRange(),
	)
	if err != nil {
		// This is already a status error.
		return nil, err
	}

	topology = append(topology, &csi.Topology{
		Segments: map[string]string{
			DriverTopologyKey: cs.nodeID,
		},
	})

	resp = &csi.CreateVolumeResponse{
		Volume: &csi.Volume{
			VolumeId:           volumeID,
			CapacityBytes:      size,
			AccessibleTopology: topology,
		},
	}

	return resp, nil
}

func (cs *nodeControllerServer) createVolumeInternal(ctx context.Context,
	p parameters.Volume,
	volumeName string,
	volumeCapabilities []*csi.VolumeCapability,
	capacity *csi.CapacityRange,
) (volumeID string, actual int64, statusErr error) {
	// Keep volume name as part of volume parameters for use in
	// getVolumeByName.
	p.Name = &volumeName

	asked := capacity.GetRequiredBytes()
	if vol := cs.getVolumeByName(volumeName); vol != nil {
		// Check if the size of existing volume can cover the new request
		klog.V(4).Infof("Node CreateVolume: volume exists, name:%q id:%s size:%v", volumeName, vol.ID, vol.Size)
		if vol.Size < asked {
			statusErr = status.Error(codes.AlreadyExists, fmt.Sprintf("smaller volume with the same name %q already exists", volumeName))
			return
		}
		// Use existing volume, it's the one the caller asked
		// for earlier (idempotent call):
		volumeID = vol.ID
		actual = vol.Size
		return
	}

	klog.V(4).Infof("Node CreateVolume: Name:%q req.Required:%v req.Limit:%v", volumeName, asked, capacity.GetLimitBytes())
	volumeID = p.GetVolumeID()
	if volumeID == "" {
		volumeID = GenerateVolumeID("Node CreateVolume", volumeName)
		// Check do we have entry with newly generated VolumeID already
		if vol := cs.getVolumeByID(volumeID); vol != nil {
			// if we have, that has to be VolumeID collision, because above we checked
			// that we don't have entry with such Name. VolumeID collision is very-very
			// unlikely so we should not get here in any near future, if otherwise state is good.
			klog.V(3).Infof("Controller CreateVolume: VolumeID:%s collision: existing name:%s new name:%s",
				volumeID, vol.Params[parameters.Name], volumeName)
			statusErr = status.Error(codes.Internal, "VolumeID/hash collision, can not create unique Volume")
			return
		}
	}

	// Set which device manager was used to create the volume
	mode := cs.dm.GetMode()
	p.DeviceMode = &mode

	vol := &nodeVolume{
		ID:     volumeID,
		Size:   asked,
		Params: p.ToContext(),
	}
	if cs.sm != nil {
		// Persist new volume state *before* actually creating the volume.
		// Writing this state after creating the volume has the risk that
		// we leak the volume if we don't get around to storing the state.
		if err := cs.sm.Create(volumeID, vol); err != nil {
			statusErr = status.Error(codes.Internal, "Node CreateVolume: "+err.Error())
			return
		}
		defer func() {
			// In case of failure, remove volume from state again because it wasn't created.
			// This is allowed to fail because orphaned entries will be detected eventually.
			if statusErr != nil {
				if err := cs.sm.Delete(volumeID); err != nil {
					klog.Warningf("Delete volume state for id '%s' failed with error: %v", volumeID, err)
				}
			}
		}()
	}
	if asked <= 0 {
		// Allocating volumes of zero size isn't supported.
		// We use some arbitrary small minimum size instead.
		// It will get rounded up by below layer to meet the alignment.
		asked = 1
	}
	if err := cs.dm.CreateDevice(volumeID, uint64(asked)); err != nil {
		statusErr = status.Errorf(codes.Internal, "Node CreateVolume: device creation failed: %v", err)
		return
	}
	// TODO(?): determine and return actual size here?
	actual = asked

	cs.mutex.Lock()
	defer cs.mutex.Unlock()
	cs.pmemVolumes[volumeID] = vol
	klog.V(3).Infof("Node CreateVolume: Record new volume as %v", *vol)

	return
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
	vol := cs.getVolumeByID(req.VolumeId)
	if vol == nil {
		// Already deleted.
		return &csi.DeleteVolumeResponse{}, nil
	}
	p, err := parameters.Parse(parameters.NodeVolumeOrigin, vol.Params)
	if err != nil {
		// This should never happen because PMEM-CSI itself created these parameters.
		// But if it happens, better fail and force an admin to recover instead of
		// potentially destroying data.
		return nil, status.Errorf(codes.Internal, "previously stored volume parameters for volume with ID %q: %v", req.VolumeId, err)
	}

	dm := cs.dm
	if dm.GetMode() != p.GetDeviceMode() {
		dm, err = newDeviceManager(p.GetDeviceMode())
		if err != nil {
			return nil, status.Errorf(codes.Internal, "failed to initialize device manager for volume(ID %q, mode: %s): %v", req.VolumeId, p.GetDeviceMode(), err)
		}
	}

	if err := dm.DeleteDevice(req.VolumeId, p.GetEraseAfter()); err != nil {
		if errors.Is(err, pmdmanager.ErrDeviceInUse) {
			return nil, status.Errorf(codes.FailedPrecondition, err.Error())
		}
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
	klog.V(5).Info("ListVolumes")
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

	capacity = int64(cap)

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
		if pmemVol.Params[parameters.Name] == volumeName {
			return pmemVol
		}
	}
	return nil
}

func (cs *nodeControllerServer) ControllerExpandVolume(context.Context, *csi.ControllerExpandVolumeRequest) (*csi.ControllerExpandVolumeResponse, error) {
	return nil, status.Error(codes.Unimplemented, "")
}
