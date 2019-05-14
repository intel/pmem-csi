/*
Copyright 2017 The Kubernetes Authors.

SPDX-License-Identifier: Apache-2.0
*/

package pmemcsidriver

import (
	"fmt"
	"math"
	"strconv"

	"github.com/google/uuid"
	"golang.org/x/net/context"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"k8s.io/klog/glog"

	"github.com/container-storage-interface/spec/lib/go/csi"

	"k8s.io/utils/keymutex"
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
	// VolumeType
	volumeType PmemPersistencyModel
}

type masterController struct {
	*DefaultControllerServer
	rs          *registryServer
	pmemVolumes map[string]*pmemVolume //map of reqID:pmemVolume
}

var _ csi.ControllerServer = &masterController{}
var _ PmemService = &masterController{}
var volumeMutex = keymutex.NewHashed(-1)

func NewMasterControllerServer(rs *registryServer) *masterController {
	serverCaps := []csi.ControllerServiceCapability_RPC_Type{
		csi.ControllerServiceCapability_RPC_CREATE_DELETE_VOLUME,
		csi.ControllerServiceCapability_RPC_LIST_VOLUMES,
		csi.ControllerServiceCapability_RPC_GET_CAPACITY,
	}
	return &masterController{
		DefaultControllerServer: NewDefaultControllerServer(serverCaps),
		rs:                      rs,
		pmemVolumes:             map[string]*pmemVolume{},
	}
}

func (cs *masterController) RegisterService(rpcServer *grpc.Server) {
	csi.RegisterControllerServer(rpcServer, cs)
}

func (cs *masterController) CreateVolume(ctx context.Context, req *csi.CreateVolumeRequest) (*csi.CreateVolumeResponse, error) {
	var vol *pmemVolume
	chosenNodes := map[string]VolumeStatus{}

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

	asked := req.GetCapacityRange().GetRequiredBytes()

	outTopology := []*csi.Topology{}
	glog.V(3).Infof("CreateVolume: Name: %v req.Required: %v req.Limit: %v", req.Name, asked, req.GetCapacityRange().GetLimitBytes())
	if vol = cs.getVolumeByName(req.Name); vol != nil {
		// Check if the size of existing volume can cover the new request
		glog.V(4).Infof("CreateVolume: Vol %s exists, Size: %v", req.Name, vol.size)
		if vol.size < asked {
			return nil, status.Error(codes.AlreadyExists, fmt.Sprintf("Volume with the same name: %s but with different size already exist", req.Name))
		}

		chosenNodes = vol.nodeIDs
	} else {
		id, _ := uuid.NewUUID() //nolint: gosec
		volumeID := id.String()
		inTopology := []*csi.Topology{}
		volumeType := pmemPersistencyModelNone
		cacheCount := uint64(1)

		if req.Parameters == nil {
			req.Parameters = map[string]string{}
		} else {
			if val, ok := req.Parameters[pmemParameterKeyPersistencyModel]; ok {
				volumeType = PmemPersistencyModel(val)
				if volumeType == pmemPersistencyModelCache {
					if val, ok := req.Parameters[pmemParameterKeyCacheSize]; ok {
						c, err := strconv.ParseUint(val, 10, 64)
						if err != nil {
							glog.Warning("failed to parse '" + pmemParameterKeyCacheSize + "' parameter")
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
			for node := range cs.rs.nodeClients {
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
			conn, err := cs.rs.ConnectToNodeController(node, connectionTimeout)
			if err != nil {
				glog.Warningf("failed to connect to %s: %s", node, err.Error())
				continue
			}

			defer conn.Close()

			csiClient := csi.NewControllerClient(conn)

			if _, err := csiClient.CreateVolume(ctx, req); err != nil {
				glog.Warningf("failed to create volume on %s: %s", node, err.Error())
				continue
			}
			cacheCount = cacheCount - 1
			chosenNodes[node] = Created
		}

		delete(req.Parameters, "_id")

		if len(chosenNodes) == 0 {
			return nil, status.Error(codes.Unavailable, fmt.Sprintf("No node found with %v capacity", asked))
		}

		glog.V(3).Infof("Chosen nodes: %v", chosenNodes)

		vol = &pmemVolume{
			id:         volumeID,
			name:       req.Name,
			size:       asked,
			nodeIDs:    chosenNodes,
			volumeType: volumeType,
		}
		cs.pmemVolumes[volumeID] = vol
		glog.V(3).Infof("CreateVolume: Record new volume as %v", *vol)
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
		glog.Errorf("invalid delete volume req: %v", req)
		return nil, err
	}

	// Check arguments
	if len(req.GetVolumeId()) == 0 {
		return nil, status.Error(codes.InvalidArgument, "Volume ID missing in request")
	}

	// Serialize by VolumeId
	volumeMutex.LockKey(req.VolumeId)
	defer volumeMutex.UnlockKey(req.VolumeId) //nolint: errcheck

	glog.V(4).Infof("DeleteVolume: volumeID: %v", req.GetVolumeId())
	if vol := cs.getVolumeByID(req.GetVolumeId()); vol != nil {
		for node := range vol.nodeIDs {
			conn, err := cs.rs.ConnectToNodeController(node, connectionTimeout)
			if err != nil {
				glog.Warningf("Failed to connect to node controller:%s, stale volume(%s) on %s should be cleaned manually", err.Error(), vol.id, node)
			}

			if _, err := csi.NewControllerClient(conn).DeleteVolume(ctx, req); err != nil {
				glog.Warningf("Failed to delete volume %s on %s: %s", vol.id, node, err.Error())
			}
			conn.Close() // nolint:gosec
		}
		delete(cs.pmemVolumes, vol.id)
		glog.V(4).Infof("DeleteVolume: volume %s deleted", req.GetVolumeId())
	} else {
		glog.Warningf("Volume %s not created by this controller", req.GetVolumeId())
	}

	return &csi.DeleteVolumeResponse{}, nil
}

func (cs *masterController) ValidateVolumeCapabilities(ctx context.Context, req *csi.ValidateVolumeCapabilitiesRequest) (*csi.ValidateVolumeCapabilitiesResponse, error) {

	// Check arguments
	if len(req.GetVolumeId()) == 0 {
		return nil, status.Error(codes.InvalidArgument, "Volume ID missing in request")
	}

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
	glog.V(5).Infof("ListVolumes")
	if err := cs.ValidateControllerServiceRequest(csi.ControllerServiceCapability_RPC_LIST_VOLUMES); err != nil {
		glog.Errorf("invalid list volumes req: %v", req)
		return nil, err
	}

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

	defer conn.Close()

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
		if pmemVol.name == Name {
			return pmemVol
		}
	}
	return nil
}

func (cs *masterController) ControllerExpandVolume(context.Context, *csi.ControllerExpandVolumeRequest) (*csi.ControllerExpandVolumeResponse, error) {
	return nil, status.Error(codes.Unimplemented, "")
}
