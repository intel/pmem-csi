/*
Copyright 2017 The Kubernetes Authors.

SPDX-License-Identifier: Apache-2.0
*/

package pmemcsidriver

import (
	"encoding/json"
	"fmt"

	"github.com/golang/glog"
	"golang.org/x/net/context"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/container-storage-interface/spec/lib/go/csi/v0"

	"github.com/intel/pmem-csi/pkg/ndctl"
	"github.com/intel/pmem-csi/pkg/pmem-common"
)

const (
	deviceID = "deviceID"
)

type controllerServer struct {
	*DefaultControllerServer
	ctx *ndctl.Context
}

func (cs *controllerServer) CreateVolume(ctx context.Context, req *csi.CreateVolumeRequest) (*csi.CreateVolumeResponse, error) {
	if err := cs.Driver.ValidateControllerServiceRequest(csi.ControllerServiceCapability_RPC_CREATE_DELETE_VOLUME); err != nil {
		pmemcommon.Infof(3, ctx, "invalid create volume req: %v", req)
		return nil, err
	}

	volName := req.GetName()
	asked := uint64(req.GetCapacityRange().GetRequiredBytes())
	glog.Infof("CreateVolume: Name: %v, Size: %v", volName, asked)
	// Check arguments
	if len(volName) == 0 {
		return nil, status.Error(codes.InvalidArgument, "Name missing in request")
	}
	if req.GetVolumeCapabilities() == nil {
		return nil, status.Error(codes.InvalidArgument, "Volume Capabilities missing in request")
	}
	// Check for existing volume. If found, check does this fit into already allocated capacity

	if ns, err := cs.ctx.GetNamespaceByName(volName); err == nil {
		// Check if the size of exisiting volume new can cover the new request
		glog.Infof("CreateVolume: Vol %s exists, Size: %v", ns.Name(), ns.Size())
		if ns.Size() >= asked {
			// exisiting volume is compatible with new request and should be reused.
			return &csi.CreateVolumeResponse{
				Volume: &csi.Volume{
					Id:            volName,
					CapacityBytes: int64(ns.Size()),
					Attributes:    req.GetParameters(),
				},
			}, nil
		}
		return nil, status.Error(codes.AlreadyExists, fmt.Sprintf("Volume with the same name: %s but with different size already exist", volName))
	}
	// Check for available unallocated capacity
	// available_size, err := ndctl.GetAvailableSize()
	// if err != nil {
	// 	pmemcommon.Infof(3, ctx, "failed to get AvailSize: %v", err)
	// 	return nil, err
	// }
	//glog.Infof("CreateVolume: AvailableSize:  %v", available_size)
	//glog.Infof("CreateVolume: Asked capacity: %v", asked)
	// if asked > available_size {
	// 	return nil, status.Errorf(codes.OutOfRange, "Requested capacity %d exceeds available capacity %d", asked, available_size)
	// }
	// Create namespace
	ns, err := cs.ctx.CreateNamespace(ndctl.CreateNamespaceOpts{
		Size: asked,
		Name: volName,
	})
	if err != nil {
		pmemcommon.Infof(3, ctx, "failed to create namespace: %v", err)
		return nil, err
	}
	data, _ := ns.MarshalJSON()
	glog.Infof("Namespace crated: %v", data)
	// TODO: do we need to create this uuid here, can we use something else as volumeID?
	// I think this volumeID has been inherited here from hostpath driver
	// volumeID := ns.UUID().String()
	volumeID := ns.Name()
	return &csi.CreateVolumeResponse{
		Volume: &csi.Volume{
			Id:            volumeID,
			CapacityBytes: int64(asked),
			Attributes:    req.GetParameters(),
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
	volumeID := req.VolumeId
	glog.Infof("DeleteVolume: volumeID: %v", volumeID)
	pmemcommon.Infof(4, ctx, "deleting volume %s", volumeID)
	// TODO: should we wipe the space here+?
	// for privacy, somewhere we need to wipe. But where, and what about performance hit.
	if err := cs.ctx.DestroyNamespaceByName(volumeID); err != nil {
		return nil, err
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

	for _, cap := range req.VolumeCapabilities {
		if cap.GetAccessMode().GetMode() != csi.VolumeCapability_AccessMode_SINGLE_NODE_WRITER {
			return &csi.ValidateVolumeCapabilitiesResponse{Supported: false, Message: ""}, nil
		}
	}
	return &csi.ValidateVolumeCapabilitiesResponse{Supported: true, Message: ""}, nil
}

func (cs *controllerServer) ListVolumes(ctx context.Context, req *csi.ListVolumesRequest) (*csi.ListVolumesResponse, error) {

	if err := cs.Driver.ValidateControllerServiceRequest(csi.ControllerServiceCapability_RPC_LIST_VOLUMES); err != nil {
		pmemcommon.Infof(3, ctx, "invalid list volumes req: %v", req)
		return nil, err
	}
	// List namespaces
	nss := cs.ctx.GetActiveNamespaces()

	var entries []*csi.ListVolumesResponse_Entry
	for _, ns := range nss {
		data, _ := json.MarshalIndent(ns, "", " ")
		glog.Info("Namespace:", string(data[:]))
		//glog.Infof("namespace BlockDevName: %v, Size: %v", ns.BlockDeviceName(), ns.Size())
		info := &csi.Volume{
			Id:            ns.Name(),
			CapacityBytes: int64(ns.Size()),
			Attributes:    nil,
		}
		entry := &csi.ListVolumesResponse_Entry{
			Volume:               info,
			XXX_NoUnkeyedLiteral: *new(struct{}),
			XXX_unrecognized:     nil,
			XXX_sizecache:        0,
		}
		entries = append(entries, entry)
	}
	response := &csi.ListVolumesResponse{
		Entries:              entries,
		NextToken:            "",
		XXX_NoUnkeyedLiteral: *new(struct{}),
	}
	return response, nil
}
