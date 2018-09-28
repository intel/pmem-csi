/*
Copyright 2017 The Kubernetes Authors.

SPDX-License-Identifier: Apache-2.0
*/

package pmemcsidriver

import (
	"golang.org/x/net/context"
	"os"
	"os/exec"
	"strings"

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
	requestedFsType := req.GetVolumeCapability().GetMount().GetFsType()
	// showing for debug:
	glog.Infof("NodeStageVolume: VolumeID is %v", req.GetVolumeId())
	glog.Infof("NodeStageVolume: Staging target path is %v", stagingtargetPath)
	glog.Infof("NodeStageVolume: Requested fsType is %v", requestedFsType)

	var devicepath string
	if lvmode() == true {
		// using shortcut: don't look it up, just compose LV device path is
		devicepath = "/dev/" + lvgroup + "/" + req.GetVolumeId()
		glog.Infof("NodeStageVolume: devicepath: %v", devicepath)
	} else {
		namespace, err := ns.ctx.GetNamespaceByName(req.GetVolumeId())
		if err != nil {
			pmemcommon.Infof(3, ctx, "NodeStageVolume: did not find volume %s", req.GetVolumeId())
			return nil, err
		}
		glog.Infof("NodeStageVolume: Existing namespace: blockdev is %v with size %v", namespace.BlockDeviceName(), namespace.Size())
		devicepath = "/dev/" + namespace.BlockDeviceName()
	}


	// Check does devicepath already contain a filesystem?
	existingFsType, err := determineFilesystemType(devicepath)
	if err != nil {
		return nil, err
	}

	// what to do if existing file system is detected and is different from request;
	// TODO: Is the current decision to error-out here OK?
	// forced re-format would lead to loss of previous data, so we refuse.
	if existingFsType != "" {
		glog.Infof("NodeStageVolume: Found existing %v filesystem", existingFsType)
		// Is existing filesystem type same as requested?
		if existingFsType == requestedFsType {
			glog.Infof("Skip mkfs as %v file system already exists on %v", existingFsType, devicepath)
		} else {
			pmemcommon.Infof(3, ctx, "NodeStageVolume: File system with different type %v exist on %v",
				existingFsType, devicepath)
			return nil, status.Error(codes.InvalidArgument, "File system with different type exists")
		}
	} else {
		// no existing file system, make fs
		var output []byte
		if requestedFsType == "ext4" {
			glog.Infof("NodeStageVolume: mkfs.ext4 -F %s", devicepath)
			output, err = exec.Command("mkfs.ext4", "-F", devicepath).CombinedOutput()
		} else if requestedFsType == "xfs" {
			glog.Infof("NodeStageVolume: mkfs.xfs -f %s", devicepath)
			output, err = exec.Command("mkfs.xfs", "-f", devicepath).CombinedOutput()
		} else {
			return nil, status.Error(codes.InvalidArgument, "xfs, ext4 are supported as file system types")
		}
		if err != nil {
			return nil, status.Error(codes.InvalidArgument, "mkfs failed"+string(output))
		}
	}

	// MkdirAll is equal to mkdir -p i.e. it creates parent dirs if needed, and is no-op if dir exists
	glog.Infof("NodeStageVolume: mkdir -p %s", stagingtargetPath)
	err = os.MkdirAll(stagingtargetPath, 0777)
	if err != nil {
		pmemcommon.Infof(3, ctx, "failed to create volume: %v", err)
		return nil, err
	}
	// If file system is already mounted, can happen if out-of-sync "stage" is asked again without unstage
	// then the mount here will fail. I guess it's ok to not check explicitly for existing mount,
	// as end result after mount attempt will be same: no new mount and existing mount remains.
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

	// by spec, we have to return OK if asked volume is not mounted on asked path,
	// so we look up the current device by volumeID and see is that device
	// mounted on staging target path
	var devicepath string
	if lvmode() == true {
		// use shortcut: dont look it up, compose as we know what LV device path is.
		// Note different form than in NodeStageVolume
		devicepath = "/dev/mapper/" + lvgroup + "-" + req.GetVolumeId()
		glog.Infof("NodeUnstageVolume: devicepath: %v", devicepath)
	} else {
		namespace, err := ns.ctx.GetNamespaceByName(req.GetVolumeId())
		if err != nil {
			pmemcommon.Infof(3, ctx, "NodeUnstageVolume: did not find volume %s", req.GetVolumeId())
			return nil, err
		}
		glog.Infof("NodeUnstageVolume: Existing namespace: blockdev: %v with size %v", namespace.BlockDeviceName(), namespace.Size())
		devicepath = "/dev/" + namespace.BlockDeviceName()
	}

	// check all mountpoints
	mounter := mount.New("")
	mountPoints, mountPointsErr := mounter.List()
	if mountPointsErr != nil {
		return nil, mountPointsErr
	}
	for _, mp := range mountPoints {
		if mp.Device == devicepath && mp.Path == stagingtargetPath {
			glog.Infof("NodeUnstageVolume: Found matching mount: dev: %v path: %v", mp.Device, mp.Path)
			umounter := mount.New("")
			if err := umounter.Unmount(stagingtargetPath); err != nil {
				glog.Infof("NodeUnstageVolume: Umount failed: %v", err)
				return nil, err
			}
			RemoveDir(ctx, stagingtargetPath)
		}
	}
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

// This is based on function used in LV-CSI driver
func determineFilesystemType(devicePath string) (string, error) {
	// Use `file -bsL` to determine whether any filesystem type is detected.
	// If a filesystem is detected (ie., the output is not "data", we use
	// `blkid` to determine what the filesystem is. We use `blkid` as `file`
	// has inconvenient output.
	// We do *not* use `lsblk` as that requires udev to be up-to-date which
	// is often not the case when a device is erased using `dd`.
	output, err := exec.Command("file", "-bsL", devicePath).CombinedOutput()
	if err != nil {
		return "", err
	}
	if strings.TrimSpace(string(output)) == "data" {
		// No filesystem detected.
		return "", nil
	}
	// Some filesystem was detected, use blkid to figure out what it is.
	output, err = exec.Command("blkid", "-c", "/dev/null", "-o", "export", devicePath).CombinedOutput()
	if err != nil {
		return "", err
	}
	parseErr := status.Error(codes.InvalidArgument, "Can not parse blkid output")
	lines := strings.Split(string(output), "\n")
	for _, line := range lines {
		fields := strings.Split(strings.TrimSpace(line), "=")
		if len(fields) != 2 {
			return "", parseErr
		}
		if fields[0] == "TYPE" {
			return fields[1], nil
		}
	}
	return "", parseErr
}

var lvMode bool = false
var lvModeSet bool = false
func lvmode() (bool) {
	if lvModeSet == false {
		lvModeSet = true
		glog.Infof("LVmode not set, try to determine...")
		_, err := exec.Command("vgdisplay", lvgroup).CombinedOutput()
		if err != nil {
			lvMode = false
			glog.Infof("No LV group: %v found, LV mode false", lvgroup)
		} else {
			lvMode = true
			glog.Infof("LV group: %v is found, LV mode is true", lvgroup)
		}
	}
	return lvMode
}
