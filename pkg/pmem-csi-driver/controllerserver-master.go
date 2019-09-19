/*
Copyright 2017 The Kubernetes Authors.

SPDX-License-Identifier: Apache-2.0
*/

package pmemcsidriver

import (
	"crypto/sha1"
	"encoding/hex"
	"fmt"
	"math"
	"strconv"
	"sync"

	"github.com/container-storage-interface/spec/lib/go/csi"
	"golang.org/x/net/context"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"k8s.io/klog"
	"k8s.io/utils/keymutex"

	"github.com/intel/pmem-csi/pkg/registryserver"
)

//VolumeStatus type representation for volume status
type VolumeStatus int

const (
	//Created volume created
	Created VolumeStatus = iota + 1
	//Deleted volume deleted
	Deleted

	// FIXME(avalluri): Choose better naming
	pmemParameterKeyPersistencyModel = "persistencyModel"
	pmemParameterKeyCacheSize        = "cacheSize"

	pmemPersistencyModelNone  PmemPersistencyModel = "none"
	pmemPersistencyModelCache PmemPersistencyModel = "cache"
	//pmemPersistencyModelEphemeral PmemPersistencyModel = "ephemeral"
)

type PmemPersistencyModel string

type pmemVolume struct {
	// VolumeID published to outside world
	id string
	// Name of volume
	name string
	// Size of the volume
	size int64
	// ID of nodes where the volume provisioned/attached
	// It would be one if simple volume, else would be more than one for "cached" volume
	nodeIDs map[string]VolumeStatus
}

type masterController struct {
	*DefaultControllerServer
	rs          *registryserver.RegistryServer
	pmemVolumes map[string]*pmemVolume //map of reqID:pmemVolume
	mutex       sync.Mutex             // mutex for pmemVolumes
}

var _ csi.ControllerServer = &masterController{}
var _ PmemService = &masterController{}
var _ registryserver.RegistryListener = &masterController{}
var volumeMutex = keymutex.NewHashed(-1)

func NewMasterControllerServer(rs *registryserver.RegistryServer) *masterController {
	serverCaps := []csi.ControllerServiceCapability_RPC_Type{
		csi.ControllerServiceCapability_RPC_CREATE_DELETE_VOLUME,
		csi.ControllerServiceCapability_RPC_LIST_VOLUMES,
		csi.ControllerServiceCapability_RPC_GET_CAPACITY,
	}
	cs := &masterController{
		DefaultControllerServer: NewDefaultControllerServer(serverCaps),
		rs:                      rs,
		pmemVolumes:             map[string]*pmemVolume{},
	}

	rs.AddListener(cs)

	return cs
}

func (cs *masterController) RegisterService(rpcServer *grpc.Server) {
	csi.RegisterControllerServer(rpcServer, cs)
}

// OnNodeAdded retrieves the existing volumes at recently added Node.
// It uses ControllerServer.ListVolume() CSI call to retrieve volumes.
func (cs *masterController) OnNodeAdded(ctx context.Context, node *registryserver.NodeInfo) error {
	conn, err := cs.rs.ConnectToNodeController(node.NodeID)
	if err != nil {
		return fmt.Errorf("Connection failure on given endpoint %s : %s", node.Endpoint, err.Error())
	}

	csiClient := csi.NewControllerClient(conn)
	resp, err := csiClient.ListVolumes(ctx, &csi.ListVolumesRequest{})
	if err != nil {
		return fmt.Errorf("Node failed to report volumes: %s", err.Error())
	}

	klog.V(5).Infof("Found Volumes at %s: %v", node.NodeID, resp.Entries)

	cs.mutex.Lock()
	defer cs.mutex.Unlock()

	for _, entry := range resp.Entries {
		v := entry.GetVolume()
		if v == nil { /* this shouldn't happen */
			continue
		}
		if vol, ok := cs.pmemVolumes[v.VolumeId]; ok && vol != nil {
			// This is possibly Cache volume, so just add this node id.
			vol.nodeIDs[node.NodeID] = Created
		} else {
			cs.pmemVolumes[v.VolumeId] = &pmemVolume{
				id:   v.VolumeId,
				size: v.CapacityBytes,
				name: v.VolumeContext["Name"],
				nodeIDs: map[string]VolumeStatus{
					node.NodeID: Created,
				},
			}
		}
	}

	return nil
}

func (cs *masterController) OnNodeDeleted(ctx context.Context, node *registryserver.NodeInfo) {
}

func (cs *masterController) CreateVolume(ctx context.Context, req *csi.CreateVolumeRequest) (*csi.CreateVolumeResponse, error) {
	var vol *pmemVolume
	chosenNodes := map[string]VolumeStatus{}

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

	for _, cap := range req.VolumeCapabilities {
		if cap.GetBlock() != nil {
			return nil, status.Error(codes.Unimplemented, "VolumeCapability access_type:block unimplemented")
		}
	}

	asked := req.GetCapacityRange().GetRequiredBytes()

	outTopology := []*csi.Topology{}
	klog.V(3).Infof("Controller CreateVolume: Name:%v required_bytes:%v limit_bytes:%v", req.Name, asked, req.GetCapacityRange().GetLimitBytes())
	if vol = cs.getVolumeByName(req.Name); vol != nil {
		// Check if the size of existing volume can cover the new request
		klog.V(4).Infof("CreateVolume: Vol %s exists, Size: %v", req.Name, vol.size)
		if vol.size < asked {
			return nil, status.Error(codes.AlreadyExists, fmt.Sprintf("Smaller volume with the same name:%s already exists", req.Name))
		}

		chosenNodes = vol.nodeIDs
	} else {
		// VolumeID is hashed from Volume Name.
		// Hashing guarantees same ID for repeated requests.
		hasher := sha1.New()
		hasher.Write([]byte(req.Name))
		volumeID := hex.EncodeToString(hasher.Sum(nil))
		klog.V(4).Infof("Controller CreateVolume: Create SHA1 hash from name:%s to form id:%s", req.Name, volumeID)
		inTopology := []*csi.Topology{}
		cacheCount := uint64(1)

		if req.Parameters == nil {
			req.Parameters = map[string]string{}
		} else {
			if val, ok := req.Parameters[pmemParameterKeyPersistencyModel]; ok {
				volumeType := PmemPersistencyModel(val)
				if volumeType == pmemPersistencyModelCache {
					if val, ok := req.Parameters[pmemParameterKeyCacheSize]; ok {
						c, err := strconv.ParseUint(val, 10, 64)
						if err != nil {
							klog.Warning("failed to parse '" + pmemParameterKeyCacheSize + "' parameter")
						} else {
							cacheCount = c
						}
					}
				}
			}
		}

		if reqTop := req.GetAccessibilityRequirements(); reqTop != nil {
			inTopology = reqTop.Preferred
			if inTopology == nil {
				inTopology = reqTop.Requisite
			}
		}

		if len(inTopology) == 0 {
			// No topology provided, so we are free to choose from all available
			// nodes
			for node := range cs.rs.NodeClients() {
				inTopology = append(inTopology, &csi.Topology{
					Segments: map[string]string{
						PmemDriverTopologyKey: node,
					},
				})
			}
		}

		// Ask all nodes to use existing volume id
		req.Parameters["_id"] = volumeID
		for _, top := range inTopology {
			if cacheCount == 0 {
				break
			}
			node := top.Segments[PmemDriverTopologyKey]
			conn, err := cs.rs.ConnectToNodeController(node)
			if err != nil {
				klog.Warningf("failed to connect to %s: %s", node, err.Error())
				continue
			}

			defer conn.Close()

			csiClient := csi.NewControllerClient(conn)

			if _, err := csiClient.CreateVolume(ctx, req); err != nil {
				klog.Warningf("failed to create volume name:%s id:%s on %s: %s", node, req.Name, volumeID, err.Error())
				continue
			}
			cacheCount = cacheCount - 1
			chosenNodes[node] = Created
		}

		delete(req.Parameters, "_id")

		if len(chosenNodes) == 0 {
			return nil, status.Error(codes.Unavailable, fmt.Sprintf("No node found with %v capacity", asked))
		}

		klog.V(3).Infof("Chosen nodes: %v", chosenNodes)

		vol = &pmemVolume{
			id:      volumeID,
			name:    req.Name,
			size:    asked,
			nodeIDs: chosenNodes,
		}
		cs.mutex.Lock()
		defer cs.mutex.Unlock()
		cs.pmemVolumes[volumeID] = vol
		klog.V(3).Infof("Controller CreateVolume: Record new volume as %v", *vol)
	}

	for node := range chosenNodes {
		outTopology = append(outTopology, &csi.Topology{
			Segments: map[string]string{
				PmemDriverTopologyKey: node,
			},
		})
	}

	return &csi.CreateVolumeResponse{
		Volume: &csi.Volume{
			VolumeId:           vol.id,
			CapacityBytes:      asked,
			AccessibleTopology: outTopology,
			VolumeContext:      req.Parameters,
		},
	}, nil
}

func (cs *masterController) DeleteVolume(ctx context.Context, req *csi.DeleteVolumeRequest) (*csi.DeleteVolumeResponse, error) {
	if err := cs.ValidateControllerServiceRequest(csi.ControllerServiceCapability_RPC_CREATE_DELETE_VOLUME); err != nil {
		klog.Errorf("invalid delete volume req: %v", req)
		return nil, err
	}

	// Check arguments
	if len(req.GetVolumeId()) == 0 {
		return nil, status.Error(codes.InvalidArgument, "Volume ID missing in request")
	}

	// Serialize by VolumeId
	volumeMutex.LockKey(req.VolumeId)
	defer volumeMutex.UnlockKey(req.VolumeId) //nolint: errcheck

	klog.V(4).Infof("DeleteVolume: requested volumeID: %v", req.GetVolumeId())
	if vol := cs.getVolumeByID(req.GetVolumeId()); vol != nil {
		for node := range vol.nodeIDs {
			conn, err := cs.rs.ConnectToNodeController(node)
			if err != nil {
				return nil, status.Error(codes.Internal, "Failed to connect to node "+node+": "+err.Error())
			}
			defer conn.Close() // nolint:errcheck
			klog.V(4).Infof("Asking node %s to delete volume name:%s id:%s", node, vol.name, vol.id)
			if _, err := csi.NewControllerClient(conn).DeleteVolume(ctx, req); err != nil {
				return nil, status.Error(codes.Internal, "Failed to delete volume name:"+vol.name+" id:"+vol.id+" on "+node+": "+err.Error())
			}
		}
		cs.mutex.Lock()
		defer cs.mutex.Unlock()
		delete(cs.pmemVolumes, vol.id)
		klog.V(4).Infof("Controller DeleteVolume: volume name:%s id:%s deleted", vol.name, vol.id)
	} else {
		klog.Warningf("Volume %s not created by this controller", req.GetVolumeId())
	}

	return &csi.DeleteVolumeResponse{}, nil
}

func (cs *masterController) ValidateVolumeCapabilities(ctx context.Context, req *csi.ValidateVolumeCapabilitiesRequest) (*csi.ValidateVolumeCapabilitiesResponse, error) {

	// Check arguments
	if len(req.GetVolumeId()) == 0 {
		return nil, status.Error(codes.InvalidArgument, "Volume ID missing in request")
	}
	cs.mutex.Lock()
	defer cs.mutex.Unlock()

	_, found := cs.pmemVolumes[req.VolumeId]
	if !found {
		return nil, status.Error(codes.NotFound, "No volume found with id %s"+req.VolumeId)
	}

	if req.GetVolumeCapabilities() == nil {
		return nil, status.Error(codes.InvalidArgument, "Volume capabilities missing in request")
	}

	for _, cap := range req.VolumeCapabilities {
		if cap.GetAccessMode().GetMode() != csi.VolumeCapability_AccessMode_SINGLE_NODE_WRITER {
			return &csi.ValidateVolumeCapabilitiesResponse{
				Confirmed: nil,
				Message:   "Driver does not support '" + cap.AccessMode.Mode.String() + "' mode",
			}, nil
		}
		if cap.GetBlock() != nil {
			return &csi.ValidateVolumeCapabilitiesResponse{
				Confirmed: nil,
				Message:   "Driver does not support access type: block",
			}, nil
		}
	}

	/*
	 * FIXME(avalluri): Need to validate other capabilities against the existing volume
	 */
	return &csi.ValidateVolumeCapabilitiesResponse{
		Confirmed: &csi.ValidateVolumeCapabilitiesResponse_Confirmed{
			VolumeCapabilities: req.VolumeCapabilities,
			VolumeContext:      req.GetVolumeContext(),
		},
	}, nil
}

func (cs *masterController) ListVolumes(ctx context.Context, req *csi.ListVolumesRequest) (*csi.ListVolumesResponse, error) {
	klog.V(5).Infof("ListVolumes")
	if err := cs.ValidateControllerServiceRequest(csi.ControllerServiceCapability_RPC_LIST_VOLUMES); err != nil {
		klog.Errorf("invalid list volumes req: %v", req)
		return nil, err
	}

	cs.mutex.Lock()
	defer cs.mutex.Unlock()

	// Copy from map into array for pagination.
	vols := make([]*pmemVolume, 0, len(cs.pmemVolumes))
	for _, vol := range cs.pmemVolumes {
		vols = append(vols, vol)
	}

	// Code originally copied from https://github.com/kubernetes-csi/csi-test/blob/f14e3d32125274e0c3a3a5df380e1f89ff7c132b/mock/service/controller.go#L309-L365

	var (
		ulenVols      = int32(len(vols))
		maxEntries    = req.MaxEntries
		startingToken int32
	)

	if v := req.StartingToken; v != "" {
		i, err := strconv.ParseUint(v, 10, 32)
		if err != nil {
			return nil, status.Errorf(
				codes.Aborted,
				"startingToken=%d !< int32=%d",
				startingToken, math.MaxUint32)
		}
		startingToken = int32(i)
	}

	if startingToken > ulenVols {
		return nil, status.Errorf(
			codes.Aborted,
			"startingToken=%d > len(vols)=%d",
			startingToken, ulenVols)
	}

	// Discern the number of remaining entries.
	rem := ulenVols - startingToken

	// If maxEntries is 0 or greater than the number of remaining entries then
	// set maxEntries to the number of remaining entries.
	if maxEntries == 0 || maxEntries > rem {
		maxEntries = rem
	}

	var (
		i       int
		j       = startingToken
		entries = make(
			[]*csi.ListVolumesResponse_Entry,
			maxEntries)
	)

	for i = 0; i < len(entries); i++ {
		vol := vols[j]
		entries[i] = &csi.ListVolumesResponse_Entry{
			Volume: &csi.Volume{
				VolumeId:      vol.id,
				CapacityBytes: vol.size,
			},
		}
		j++
	}

	var nextToken string
	if n := startingToken + int32(i); n < ulenVols {
		nextToken = fmt.Sprintf("%d", n)
	}

	return &csi.ListVolumesResponse{
		Entries:   entries,
		NextToken: nextToken,
	}, nil
}

func (cs *masterController) GetCapacity(ctx context.Context, req *csi.GetCapacityRequest) (*csi.GetCapacityResponse, error) {
	var capacity int64
	if err := cs.ValidateControllerServiceRequest(csi.ControllerServiceCapability_RPC_GET_CAPACITY); err != nil {
		return nil, err
	}

	for _, node := range cs.rs.NodeClients() {
		cap, err := cs.getNodeCapacity(ctx, *node, req)
		if err != nil {
			klog.Warningf("Error while fetching '%s' node capacity: %s", node.NodeID, err.Error())
			continue
		}
		capacity += cap
	}

	return &csi.GetCapacityResponse{
		AvailableCapacity: capacity,
	}, nil
}

func (cs *masterController) getNodeCapacity(ctx context.Context, node registryserver.NodeInfo, req *csi.GetCapacityRequest) (int64, error) {
	conn, err := cs.rs.ConnectToNodeController(node.NodeID)
	if err != nil {
		return 0, fmt.Errorf("failed to connect to node %s: %s", node.NodeID, err.Error())
	}

	defer conn.Close()

	csiClient := csi.NewControllerClient(conn)
	resp, err := csiClient.GetCapacity(ctx, req)
	if err != nil {
		return 0, fmt.Errorf("Error while fetching '%s' node capacity: %s", node.NodeID, err.Error())
	}

	return resp.AvailableCapacity, nil
}

func (cs *masterController) getVolumeByID(volumeID string) *pmemVolume {
	cs.mutex.Lock()
	defer cs.mutex.Unlock()
	if pmemVol, ok := cs.pmemVolumes[volumeID]; ok {
		return pmemVol
	}
	return nil
}

func (cs *masterController) getVolumeByName(Name string) *pmemVolume {
	cs.mutex.Lock()
	defer cs.mutex.Unlock()
	for _, pmemVol := range cs.pmemVolumes {
		if pmemVol.name == Name {
			return pmemVol
		}
	}
	return nil
}

func (cs *masterController) ControllerExpandVolume(context.Context, *csi.ControllerExpandVolumeRequest) (*csi.ControllerExpandVolumeResponse, error) {
	return nil, status.Error(codes.Unimplemented, "")
}
