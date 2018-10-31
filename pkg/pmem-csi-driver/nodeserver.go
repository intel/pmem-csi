/*
Copyright 2017 The Kubernetes Authors.

SPDX-License-Identifier: Apache-2.0
*/

package pmemcsidriver

import (
	"fmt"
	"os"
	"strings"

	"golang.org/x/net/context"

	"github.com/container-storage-interface/spec/lib/go/csi/v0"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"k8s.io/kubernetes/pkg/util/mount"

	"github.com/golang/glog"
	"github.com/intel/csi-pmem/pkg/pmem-common"
	pmdmanager "github.com/intel/csi-pmem/pkg/pmem-device-manager"
	pmemexec "github.com/intel/csi-pmem/pkg/pmem-exec"
)

type nodeServer struct {
	*DefaultNodeServer
	dm      pmdmanager.PmemDeviceManager
	volInfo map[string]string
}

var _ csi.NodeServer = &nodeServer{}

func NewNodeServer(driver *CSIDriver, dm pmdmanager.PmemDeviceManager) csi.NodeServer {
	driver.AddNodeServiceCapabilities([]csi.NodeServiceCapability_RPC_Type{
		csi.NodeServiceCapability_RPC_STAGE_UNSTAGE_VOLUME,
	})
	return &nodeServer{
		DefaultNodeServer: NewDefaultNodeServer(driver),
		dm:                dm,
		volInfo:           map[string]string{},
	}
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
	if len(req.GetStagingTargetPath()) == 0 {
		return nil, status.Error(codes.InvalidArgument, "Staging target path missing in request")
	}

	targetPath := req.TargetPath
	stagingtargetPath := req.StagingTargetPath
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

	readOnly := req.GetReadonly()
	attrib := req.GetVolumeAttributes()
	mountFlags := req.GetVolumeCapability().GetMount().GetMountFlags()

	glog.Infof("NodePublishVolume: targetpath %v\nStagingtargetpath %v\nfstype %v\nreadonly %v\nattributes %v\n mountflags %v\n",
		targetPath, stagingtargetPath, fsType, readOnly, attrib, mountFlags)

	options := []string{"bind"}
	if readOnly {
		options = append(options, "ro")
	}
	glog.Infof("NodePublishVolume: bind-mount %s %s", stagingtargetPath, targetPath)
	mounter := mount.New("")
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

	glog.Infof("NodeUnpublishVolume: removing mount target directory: %s", targetPath)
	if err := os.Remove(targetPath); err != nil {
		pmemcommon.Infof(3, ctx, "failed to remove directory %v: %v", targetPath, err)
	}

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

	attrs := req.GetVolumeAttributes()

	requestedFsType := req.GetVolumeCapability().GetMount().GetFsType()
	// showing for debug:
	glog.Infof("NodeStageVolume: VolumeID is %v", req.GetVolumeId())
	glog.Infof("NodeStageVolume: VolumeName is %v", attrs["name"])
	glog.Infof("NodeStageVolume: VolumeSize is %v", attrs["size"])
	glog.Infof("NodeStageVolume: Namespacemode is %v", attrs["nsmode"])
	glog.Infof("NodeStageVolume: Staging target path is %v", stagingtargetPath)
	glog.Infof("NodeStageVolume: Requested fsType is %v", requestedFsType)

	device, err := ns.dm.GetDevice(req.VolumeId)
	if err != nil {
		pmemcommon.Infof(3, ctx, "NodeStageVolume: did not find volume %s", attrs["name"])
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}

	// Check does devicepath already contain a filesystem?
	existingFsType, err := determineFilesystemType(device.Path)
	if err != nil {
		glog.Infof("NodeStageVolume: determine failed: %v", err)
		return nil, err
	}

	// what to do if existing file system is detected and is different from request;
	// forced re-format would lead to loss of previous data, so we refuse.
	if existingFsType != "" {
		glog.Infof("NodeStageVolume: Found existing %v filesystem", existingFsType)
		// Is existing filesystem type same as requested?
		if existingFsType == requestedFsType {
			glog.Infof("Skip mkfs as %v file system already exists on %v", existingFsType, device.Path)
		} else {
			pmemcommon.Infof(3, ctx, "NodeStageVolume: File system with different type %v exist on %v",
				existingFsType, device.Path)
			return nil, status.Error(codes.InvalidArgument, "File system with different type exists")
		}
	} else {
		// no existing file system, make fs
		// Empty FsType means "unspecified" and we pick default, currently hard-codes to ext4
		cmd := ""
		args := []string{}
		if requestedFsType == "ext4" || requestedFsType == "" {
			cmd = "mkfs.ext4"
			args = []string{"-F", device.Path}
		} else if requestedFsType == "xfs" {
			cmd = "mkfs.xfs"
			args = []string{"-f", device.Path}
		} else {
			glog.Infof("NodeStageVolume: Unsupported fstype: [%v]", requestedFsType)
			return nil, status.Error(codes.InvalidArgument, "xfs, ext4 are supported as file system types")
		}
		output, err := pmemexec.RunCommand(cmd, args...)
		if err != nil {
			return nil, status.Error(codes.InvalidArgument, "mkfs failed: "+output)
		}
	}

	// If file system is already mounted, can happen if out-of-sync "stage" is asked again without unstage
	// then the mount here will fail. I guess it's ok to not check explicitly for existing mount,
	// as end result after mount attempt will be same: no new mount and existing mount remains.
	// TODO: cleaner is to explicitly check (although CSI spec may tell that out-of-order call is illegal (check it))
	glog.Infof("NodeStageVolume: mount %s %s", device.Path, stagingtargetPath)

	/* THIS is how it could go with using "mount" package
	        options := []string{""}
		mounter := mount.New("")
		if err := mounter.Mount(devicepath, stagingtargetPath, "", options); err != nil {
			return nil, err
		}*/
	// ... but it seems not supporting -c "canonical" option, so do it with exec
	// added -c makes canonical mount, resulting in mounted path matching what LV thinks is lvpath.
	args := []string{"-c"}
	// Without -c mounted path will look like /dev/mapper/... and its more difficult to match it to lvpath when unmounting
	// TODO: perhaps what's explained above can be revisited-cleaned somehow

	// Add dax option if namespacemode == fsdax
	if attrs["nsmode"] == "fsdax" {
		glog.Infof("NodeStageVolume: namespacemode FSDAX, add dax mount option")
		args = append(args, "-o", "dax")
	}
	args = append(args, device.Path, stagingtargetPath)
	glog.Infof("NodeStageVolume: mount args: [%v]", args)
	if _, err := pmemexec.RunCommand("mount", args...); err != nil {
		return nil, status.Error(codes.InvalidArgument, "mount filesystem failed"+err.Error())
	}

	ns.volInfo[req.VolumeId] = attrs["name"]
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

	volName := ns.volInfo[req.VolumeId]
	// showing for debug:
	glog.Infof("NodeUnStageVolume: VolumeID is %v", req.GetVolumeId())
	glog.Infof("NodeUnStageVolume: VolumeName is %v", volName)
	glog.Infof("NodeUnStageVolume: Staging target path is %v", stagingtargetPath)
	glog.Infof("NodeUnStageVolume: umount %s", stagingtargetPath)

	// by spec, we have to return OK if asked volume is not mounted on asked path,
	// so we look up the current device by volumeID and see is that device
	// mounted on staging target path
	_, err := ns.dm.GetDevice(req.VolumeId)
	if err != nil {
		pmemcommon.Infof(3, ctx, "NodeUnstageVolume: did not find volume %s", req.GetVolumeId())
		return nil, err
	}

	// Find out device name for mounted path
	mounter := mount.New("")
	mountedDev, _, err := mount.GetDeviceNameFromMount(mounter, stagingtargetPath)
	if err != nil {
		pmemcommon.Infof(3, ctx, "NodeUnstageVolume: Error getting device name for mount")
		return nil, err
	}
	if mountedDev == "" {
		pmemcommon.Infof(3, ctx, "NodeUnstageVolume: No device name for mount point")
		return nil, status.Error(codes.InvalidArgument, "No device found for mount point")
	}
	glog.Infof("NodeUnstageVolume: detected mountedDev: [%v]", mountedDev)
	if err := mounter.Unmount(stagingtargetPath); err != nil {
		glog.Infof("NodeUnstageVolume: Umount failed: %v", err)
		return nil, err
	}

	return &csi.NodeUnstageVolumeResponse{}, nil
}

// This is based on function used in LV-CSI driver
func determineFilesystemType(devicePath string) (string, error) {
	if devicePath == "" {
		return "", fmt.Errorf("null device path")
	}
	// Use `file -bsL` to determine whether any filesystem type is detected.
	// If a filesystem is detected (ie., the output is not "data", we use
	// `blkid` to determine what the filesystem is. We use `blkid` as `file`
	// has inconvenient output.
	// We do *not* use `lsblk` as that requires udev to be up-to-date which
	// is often not the case when a device is erased using `dd`.
	output, err := pmemexec.RunCommand("file", "-bsL", devicePath)
	if err != nil {
		return "", err
	}
	if strings.TrimSpace(output) == "data" {
		// No filesystem detected.
		return "", nil
	}
	// Some filesystem was detected, use blkid to figure out what it is.
	output, err = pmemexec.RunCommand("blkid", "-c", "/dev/null", "-o", "full", devicePath)
	if err != nil {
		return "", err
	}
	if len(output) == 0 {
		return "", fmt.Errorf("no device information for %s", devicePath)
	}

	// exptected output format from blkid:
	// devicepath: UUID="<uuid>" TYPE="<filesystem type>"
	attrs := strings.Split(string(output), ":")
	if len(attrs) != 2 {
		return "", fmt.Errorf("Can not parse blkid output: %s", output)
	}
	for _, field := range strings.Fields(attrs[1]) {
		attr := strings.Split(field, "=")
		if len(attr) == 2 && attr[0] == "TYPE" {
			return strings.Trim(attr[1], "\""), nil
		}
	}
	return "", fmt.Errorf("no filesystem type detected for %s", devicePath)
}
