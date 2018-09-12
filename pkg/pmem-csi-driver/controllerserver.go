/*
Copyright 2017 The Kubernetes Authors.

SPDX-License-Identifier: Apache-2.0
*/

package pmemcsidriver

import (
	"fmt"

        "github.com/golang/glog"
	"github.com/pborman/uuid"
	"golang.org/x/net/context"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/container-storage-interface/spec/lib/go/csi/v0"

	"github.com/intel/pmem-csi/pkg/pmem-common"
	"github.com/intel/pmem-csi/pkg/ndctl"
)

const (
        deviceID           = "deviceID"
)

type controllerServer struct {
 	*DefaultControllerServer
}

func (cs *controllerServer) CreateVolume(ctx context.Context, req *csi.CreateVolumeRequest) (*csi.CreateVolumeResponse, error) {
	if err := cs.Driver.ValidateControllerServiceRequest(csi.ControllerServiceCapability_RPC_CREATE_DELETE_VOLUME); err != nil {
		pmemcommon.Infof(3, ctx, "invalid create volume req: %v", req)
		return nil, err
	}

        volName := req.GetName()
	glog.Infof("CreateVolume: Name: %v", volName)
	// Check arguments
	if len(volName) == 0 {
		return nil, status.Error(codes.InvalidArgument, "Name missing in request")
	}
	if req.GetVolumeCapabilities() == nil {
		return nil, status.Error(codes.InvalidArgument, "Volume Capabilities missing in request")
	} 
	// Check for existing volume. If found, check does this fit into already allocated capacity
        asked := uint64(req.GetCapacityRange().GetRequiredBytes())
	if exVol, VolSize, err := ndctl.GetBlockDevByName(volName); err == nil {
		// Check if the size of exisiting volume new can cover the new request
	        glog.Infof("CreateVolume: Vol %s exists, Size: %v", exVol, VolSize)
		if VolSize >= asked {
			// exisiting volume is compatible with new request and should be reused.
			return &csi.CreateVolumeResponse{
				Volume: &csi.Volume{
					Id:            volName,
					CapacityBytes: int64(VolSize),
					Attributes:    req.GetParameters(),
				},
			}, nil
		}
		return nil, status.Error(codes.AlreadyExists, fmt.Sprintf("Volume with the same name: %s but with different size already exist", volName))
	}
	// Check for available unallocated capacity
	available_size, err := ndctl.GetAvailableSize() 
	if err != nil {
		pmemcommon.Infof(3, ctx, "failed to get AvailSize: %v", err)
 		return nil, err
	}
	glog.Infof("CreateVolume: AvailableSize:  %v", available_size)
	glog.Infof("CreateVolume: Asked capacity: %v", asked)
	if asked > available_size {
		return nil, status.Errorf(codes.OutOfRange, "Requested capacity %d exceeds available capacity %d", asked, available_size)
	}
	// Create namespace
	err = ndctl.CreateNamespace(asked, volName)
	if err != nil {
		pmemcommon.Infof(3, ctx, "failed to create namespace: %v", err)
 		return nil, err
	}
        // TODO: do we need to create this uuid here, can we use something else as volumeID?
	// I think this volumeID has been inherited here from hostpath driver
        volumeID := uuid.NewUUID().String()
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
        if err := ndctl.DeleteNamespace(volumeID); err != nil {
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

        // List namespaces
	err, names, sizes := ndctl.ListNamespaces() 
	if err != nil {
		pmemcommon.Infof(3, ctx, "failed to list namespaces: %v", err)
 		return nil, err
	}

        var entries []*csi.ListVolumesResponse_Entry
        for i := range names {
                //glog.Infof("name[%d] is: %v", i, names[i])
                //glog.Infof("size[%d] is: %v", i, sizes[i])
                info := &csi.Volume{
			Id:            names[i],
			CapacityBytes: int64(sizes[i]),
			Attributes:    nil,
                }
                entry := &csi.ListVolumesResponse_Entry{info,*new(struct {}),nil,0}
                entries = append(entries, entry)
        }
        response := &csi.ListVolumesResponse{
                entries,
                "",
		*new(struct {}),
		nil,
		0,
        }
        return response, nil
}
