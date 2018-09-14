/*
Copyright 2017 The Kubernetes Authors.

SPDX-License-Identifier: Apache-2.0
*/

package pmemcsidriver

import (
	"os"
	"os/exec"

	"golang.org/x/net/context"

	"github.com/container-storage-interface/spec/lib/go/csi/v0"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"k8s.io/kubernetes/pkg/util/mount"

	"github.com/golang/glog"
	"github.com/intel/pmem-csi/pkg/ndctl"
	"github.com/intel/pmem-csi/pkg/pmem-common"
)

type nodeServer struct {
	*DefaultNodeServer
	ctx *ndctl.Context
}

func (ns *nodeServer) NodePublishVolume(ctx context.Context, req *csi.NodePublishVolumeRequest) (*csi.NodePublishVolumeResponse, error) {

	// Check arguments
	if req.GetVolumeCapability() == nil {
		return nil, status.Error(codes.InvalidArgument, "Volume capability missing in request")
	}
	if len(req.GetVolumeId()) == 0 {
		return nil, status.Error(codes.InvalidArgument, "Volume ID missing in request")
	}
	if len(req.GetTargetPath()) == 0 {
		return nil, status.Error(codes.InvalidArgument, "Target path missing in request")
	}

	targetPath := req.GetTargetPath()
	stagingtargetPath := req.GetStagingTargetPath()
	// TODO: check is bind-mount already made
	// (happens when publish is asked repeatedly for already published namespace)
	// Repeated bind-mount does not seem to cause OS level error though, likely just No-op
	notMnt, err := mount.New("").IsLikelyNotMountPoint(targetPath)
	if err != nil {
		if os.IsNotExist(err) {
			if err = os.MkdirAll(targetPath, 0750); err != nil {
				return nil, status.Error(codes.Internal, err.Error())
			}
			notMnt = true
		} else {
			return nil, status.Error(codes.Internal, err.Error())
		}
	}

	if !notMnt {
		return &csi.NodePublishVolumeResponse{}, nil
	}

	fsType := req.GetVolumeCapability().GetMount().GetFsType()

	// TODO: check and clean this, deviceId empty and not used here?
	deviceId := ""
	if req.GetPublishInfo() != nil {
		deviceId = req.GetPublishInfo()[deviceID]
	}

	readOnly := req.GetReadonly()
	volumeId := req.GetVolumeId()
	attrib := req.GetVolumeAttributes()
	mountFlags := req.GetVolumeCapability().GetMount().GetMountFlags()

	glog.Infof("NodePublishVolume: targetpath %v\nStagingtargetpath %v\nfstype %v\ndevice %v\nreadonly %v\nattributes %v\n mountflags %v\n",
		targetPath, stagingtargetPath, fsType, deviceId, readOnly, volumeId, attrib, mountFlags)

	options := []string{"bind"}
	if readOnly {
		options = append(options, "ro")
	}
	mounter := mount.New("")
	glog.Infof("NodePublishVolume: bind-mount %s %s", stagingtargetPath, targetPath)
	if err := mounter.Mount(stagingtargetPath, targetPath, "", options); err != nil {
		return nil, err
	}

	return &csi.NodePublishVolumeResponse{}, nil
}

func (ns *nodeServer) NodeUnpublishVolume(ctx context.Context, req *csi.NodeUnpublishVolumeRequest) (*csi.NodeUnpublishVolumeResponse, error) {

	// Check arguments
	if len(req.GetVolumeId()) == 0 {
		return nil, status.Error(codes.InvalidArgument, "Volume ID missing in request")
	}
	targetPath := req.GetTargetPath()
	if len(targetPath) == 0 {
		return nil, status.Error(codes.InvalidArgument, "Target path missing in request")
	}
	volumeID := req.GetVolumeId()

	// Unmounting the image
	glog.Infof("NodeUnpublishVolume: unmount %s", targetPath)
	err := mount.New("").Unmount(targetPath)
	if err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}
	pmemcommon.Infof(4, ctx, "volume %s/%s has been unmounted.", targetPath, volumeID)

	RemoveDir(ctx, targetPath)

	return &csi.NodeUnpublishVolumeResponse{}, nil
}

func (ns *nodeServer) NodeStageVolume(ctx context.Context, req *csi.NodeStageVolumeRequest) (*csi.NodeStageVolumeResponse, error) {

	// Check arguments
	if len(req.GetVolumeId()) == 0 {
		return nil, status.Error(codes.InvalidArgument, "Volume ID missing in request")
	}
	stagingtargetPath := req.GetStagingTargetPath()
	if len(stagingtargetPath) == 0 {
		return nil, status.Error(codes.InvalidArgument, "Target path missing in request")
	}

	//volumeId := req.GetVolumeId()
	fsType := req.GetVolumeCapability().GetMount().GetFsType()
	// showing for debug:
	glog.Infof("NodeStageVolume: VolumeID is %v", req.GetVolumeId())
	glog.Infof("NodeStageVolume: Staging target path is %v", stagingtargetPath)
	glog.Infof("NodeStageVolume: fsType is %v", fsType)

	namespace, err := ns.ctx.GetNamespaceByName(req.GetVolumeId())
	if err != nil {
		pmemcommon.Infof(3, ctx, "NodeStageVolume: did not find volume %s", req.GetVolumeId())
		return nil, err
	}
	glog.Infof("NodeStageVolume: blockdev is %v with size %v", namespace.BlockDeviceName(), namespace.Size())
	devicepath := "/dev/" + namespace.BlockDeviceName()

	// TODO: check is devicepath already mounted, return from here if mounted
	// (happens when stage is asked repeatedly for already staged namespace)
	var output []byte
	if fsType == "ext4" {
		glog.Infof("NodeStageVolume: mkfs.ext4 -F %s", devicepath)
		output, err = exec.Command("mkfs.ext4", "-F", devicepath).CombinedOutput()
	} else if fsType == "xfs" {
		glog.Infof("NodeStageVolume: mkfs.xfs -f %s", devicepath)
		output, err = exec.Command("mkfs.xfs", "-f", devicepath).CombinedOutput()
	} else {
		return nil, status.Error(codes.InvalidArgument, "xfs, ext4 are supported as file system types")
	}
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, "mkfs failed"+string(output))
	}

	glog.Infof("NodeStageVolume: mkdir %s", stagingtargetPath)
	err = os.MkdirAll(stagingtargetPath, 0777)
	if err != nil {
		pmemcommon.Infof(3, ctx, "failed to create volume: %v", err)
		return nil, err
	}
	glog.Infof("NodeStageVolume: mount %s %s", devicepath, stagingtargetPath)
	options := []string{""}
	mounter := mount.New("")
	if err := mounter.Mount(devicepath, stagingtargetPath, "", options); err != nil {
		return nil, err
	}
	return &csi.NodeStageVolumeResponse{}, nil
}

func (ns *nodeServer) NodeUnstageVolume(ctx context.Context, req *csi.NodeUnstageVolumeRequest) (*csi.NodeUnstageVolumeResponse, error) {

	// Check arguments
	// TODO: volumeID is not really used in that function, can we stop its print and leave it out from checking here?
	if len(req.GetVolumeId()) == 0 {
		return nil, status.Error(codes.InvalidArgument, "Volume ID missing in request")
	}
	stagingtargetPath := req.GetStagingTargetPath()
	if len(stagingtargetPath) == 0 {
		return nil, status.Error(codes.InvalidArgument, "Target path missing in request")
	}

	// showing for debug:
	glog.Infof("NodeUnStageVolume: VolumeID is %v", req.GetVolumeId())
	glog.Infof("NodeUnStageVolume: Staging target path is %v", stagingtargetPath)

	glog.Infof("NodeUnStageVolume: umount %s", stagingtargetPath)
	mounter := mount.New("")
	if err := mounter.Unmount(stagingtargetPath); err != nil {
		return nil, err
	}
	RemoveDir(ctx, stagingtargetPath)
	return &csi.NodeUnstageVolumeResponse{}, nil
}

// common handler called from few places above
func RemoveDir(ctx context.Context, Path string) error {
	glog.Infof("RemoveDir: remove dir %s", Path)
	err := os.Remove(Path)
	if err != nil {
		pmemcommon.Infof(3, ctx, "failed to remove directory %v: %v", Path, err)
		return err
	}
	return nil
}
