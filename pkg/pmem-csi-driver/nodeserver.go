/*
Copyright 2017 The Kubernetes Authors.

SPDX-License-Identifier: Apache-2.0
*/

package pmemcsidriver

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/net/context"

	"github.com/container-storage-interface/spec/lib/go/csi"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"k8s.io/klog"
	"k8s.io/kubernetes/pkg/util/mount"

	pmdmanager "github.com/intel/pmem-csi/pkg/pmem-device-manager"
	pmemexec "github.com/intel/pmem-csi/pkg/pmem-exec"
)

type nodeServer struct {
	nodeCaps []*csi.NodeServiceCapability
	nodeID   string
	dm       pmdmanager.PmemDeviceManager
	volInfo  map[string]string
}

var _ csi.NodeServer = &nodeServer{}
var _ PmemService = &nodeServer{}

func NewNodeServer(nodeId string, dm pmdmanager.PmemDeviceManager) *nodeServer {
	return &nodeServer{
		nodeID: nodeId,
		nodeCaps: []*csi.NodeServiceCapability{
			{
				Type: &csi.NodeServiceCapability_Rpc{
					Rpc: &csi.NodeServiceCapability_RPC{
						Type: csi.NodeServiceCapability_RPC_STAGE_UNSTAGE_VOLUME,
					},
				},
			},
		},
		dm:      dm,
		volInfo: map[string]string{},
	}
}

func (ns *nodeServer) RegisterService(rpcServer *grpc.Server) {
	csi.RegisterNodeServer(rpcServer, ns)
}

func (ns *nodeServer) NodeGetInfo(ctx context.Context, req *csi.NodeGetInfoRequest) (*csi.NodeGetInfoResponse, error) {
	return &csi.NodeGetInfoResponse{
		NodeId: ns.nodeID,
		AccessibleTopology: &csi.Topology{
			Segments: map[string]string{
				PmemDriverTopologyKey: ns.nodeID,
			},
		},
	}, nil
}

func (ns *nodeServer) NodeGetCapabilities(ctx context.Context, req *csi.NodeGetCapabilitiesRequest) (*csi.NodeGetCapabilitiesResponse, error) {
	return &csi.NodeGetCapabilitiesResponse{
		Capabilities: ns.nodeCaps,
	}, nil
}

func (ns *nodeServer) NodeGetVolumeStats(ctx context.Context, req *csi.NodeGetVolumeStatsRequest) (*csi.NodeGetVolumeStatsResponse, error) {
	return nil, status.Error(codes.Unimplemented, "")
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

	// Serialize by VolumeId
	volumeMutex.LockKey(req.GetVolumeId())
	defer volumeMutex.UnlockKey(req.GetVolumeId())

	targetPath := req.TargetPath
	sourcePath := req.StagingTargetPath
	mounter := mount.New("")

	switch req.VolumeCapability.GetAccessType().(type) {
	case *csi.VolumeCapability_Block:
		dev, err := ns.dm.GetDevice(req.VolumeId)
		if err != nil {
			return nil, status.Error(codes.NotFound, "No device found with volume id "+req.VolumeId+": "+err.Error())
		}
		// For block volumes, source path is the actual Device path
		sourcePath = dev.Path
		targetDir := filepath.Dir(targetPath)
		// Make sure that parent directory of target path is existing, otherwise create it
		targetPreExisting, err := ensureDirectory(mounter, targetDir)
		if err != nil {
			return nil, err
		}
		f, err := os.OpenFile(targetPath, os.O_CREATE, os.FileMode(0644))
		defer f.Close()
		if err != nil && !os.IsExist(err) {
			if !targetPreExisting {
				if rerr := os.Remove(targetDir); rerr != nil {
					klog.Warningf("Could not remove created mount target %q: %v", targetDir, rerr)
				}
			}
			return nil, status.Errorf(codes.Internal, "Could not create target device file %q: %v", targetPath, err)
		}
	case *csi.VolumeCapability_Mount:
		notMnt, err := mount.IsNotMountPoint(mounter, targetPath)
		if err != nil && !os.IsNotExist(err) {
			return nil, status.Error(codes.Internal, "validate target path: "+err.Error())
		}
		if !notMnt {
			// TODO(https://github.com/kubernetes-sigs/gcp-compute-persistent-disk-csi-driver/issues/95): check if mount is compatible. Return OK if it is, or appropriate error.
			/*
				1) Target Path MUST be the vol referenced by vol ID
				2) VolumeCapability MUST match
				3) Readonly MUST match
			*/
			return &csi.NodePublishVolumeResponse{}, nil
		}

		if err := os.Mkdir(targetPath, os.FileMode(0755)); err != nil {
			// Kubernetes is violating the CSI spec and creates the
			// directory for us
			// (https://github.com/kubernetes/kubernetes/issues/75535). We
			// allow that by ignoring the "already exists" error.
			if !os.IsExist(err) {
				return nil, status.Error(codes.Internal, "make target dir: "+err.Error())
			}
		}
	}

	readOnly := req.GetReadonly()
	attrib := req.GetVolumeContext()
	mountFlags := req.GetVolumeCapability().GetMount().GetMountFlags()

	klog.V(3).Infof("NodePublishVolume: \nsourcepath: %v\ntargetpath: %v\nreadonly: %v\nattributes: %v\n mountflags: %v\n",
		sourcePath, targetPath, readOnly, attrib, mountFlags)

	options := []string{"bind"}
	if readOnly {
		options = append(options, "ro")
	}
	klog.V(5).Infof("NodePublishVolume: bind-mount %s %s", sourcePath, targetPath)

	if err := mounter.Mount(sourcePath, targetPath, "", options); err != nil {
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

	// Serialize by VolumeId
	volumeMutex.LockKey(volumeID)
	defer volumeMutex.UnlockKey(volumeID)

	_, err := ns.dm.GetDevice(volumeID)
	if err != nil {
		klog.Errorf("NodeUnpublishVolume: did not find volume %s", volumeID)
		return nil, status.Error(codes.NotFound, "Volume not found")
	}
	// Check if the target path is really a mount point. If its not a mount point do nothing
	if notMnt, err := mount.New("").IsLikelyNotMountPoint(targetPath); notMnt || err != nil && !os.IsNotExist(err) {
		klog.V(5).Infof("NodeUnpublishVolume: %s is not mount point, skip", targetPath)
		return &csi.NodeUnpublishVolumeResponse{}, nil
	}

	// Unmounting the image
	klog.V(3).Infof("NodeUnpublishVolume: unmount %s", targetPath)
	err = mount.New("").Unmount(targetPath)
	if err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}
	klog.V(5).Infof("NodeUnpublishVolume: volume id:%s targetpath:%s has been unmounted", volumeID, targetPath)

	os.Remove(targetPath) // nolint: gosec

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
	if req.GetVolumeCapability() == nil {
		return nil, status.Error(codes.InvalidArgument, "Volume capability missing in request")
	}

	// We should do nothing for block device usage
	switch req.VolumeCapability.GetAccessType().(type) {
	case *csi.VolumeCapability_Block:
		return &csi.NodeStageVolumeResponse{}, nil
	}

	requestedFsType := req.GetVolumeCapability().GetMount().GetFsType()
	if requestedFsType == "" {
		// Default to ext4 filesystem
		requestedFsType = "ext4"
	}

	// Serialize by VolumeId
	volumeMutex.LockKey(req.GetVolumeId())
	defer volumeMutex.UnlockKey(req.GetVolumeId())

	// showing for debug:
	klog.V(4).Infof("NodeStageVolume: VolumeID:%v Staging target path:%v Requested fsType:%v",
		req.GetVolumeId(), stagingtargetPath, requestedFsType)

	device, err := ns.dm.GetDevice(req.VolumeId)
	if err != nil {
		klog.Errorf("NodeStageVolume: did not find volume %s", req.VolumeId)
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}

	// Check does devicepath already contain a filesystem?
	existingFsType, err := determineFilesystemType(device.Path)
	if err != nil {
		klog.Errorf("NodeStageVolume: determineFilesystemType failed: %v", err)
		return nil, err
	}

	// what to do if existing file system is detected and is different from request;
	// forced re-format would lead to loss of previous data, so we refuse.
	if existingFsType != "" {
		klog.V(4).Infof("NodeStageVolume: Found existing %v filesystem", existingFsType)
		// Is existing filesystem type same as requested?
		if existingFsType == requestedFsType {
			klog.V(4).Infof("Skip mkfs as %v file system already exists on %v", existingFsType, device.Path)
		} else {
			klog.Errorf("NodeStageVolume: File system with different type %v exist on %v",
				existingFsType, device.Path)
			return nil, status.Error(codes.InvalidArgument, "File system with different type exists")
		}
	} else {
		// no existing file system, make fs
		// Empty FsType means "unspecified" and we pick default, currently hard-codes to ext4
		cmd := ""
		args := []string{}
		// hard-code block size to 4k to avoid smaller values and trouble to dax mount option
		if requestedFsType == "ext4" || requestedFsType == "" {
			cmd = "mkfs.ext4"
			args = []string{"-b 4096", "-F", device.Path}
		} else if requestedFsType == "xfs" {
			cmd = "mkfs.xfs"
			args = []string{"-b", "size=4096", "-f", device.Path}
		} else {
			klog.Errorf("NodeStageVolume: Unsupported fstype: %v", requestedFsType)
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
	klog.V(5).Infof("NodeStageVolume: mount %s %s", device.Path, stagingtargetPath)

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

	nsmode := pmemNamespaceModeFsdax //default volume namespace mode to fsdax
	if params := req.GetVolumeContext(); params != nil {
		if v, ok := params[pmemParameterKeyNamespaceMode]; ok {
			nsmode = v
		}
	}

	if nsmode == pmemNamespaceModeFsdax {
		klog.V(4).Infof("NodeStageVolume: namespacemode FSDAX, add dax mount option")
		// Add dax option if namespacemode == fsdax
		args = append(args, "-o", "dax")
	}

	args = append(args, device.Path, stagingtargetPath)
	klog.V(4).Infof("NodeStageVolume: mount args: [%v]", args)
	if _, err := pmemexec.RunCommand("mount", args...); err != nil {
		return nil, status.Error(codes.InvalidArgument, "mount filesystem failed"+err.Error())
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

	volName := ns.volInfo[req.VolumeId]

	// Serialize by VolumeId
	volumeMutex.LockKey(req.GetVolumeId())
	defer volumeMutex.UnlockKey(req.GetVolumeId())

	// showing for debug:
	klog.V(4).Infof("NodeUnStageVolume: name:%s id:%v Staging target path:%s",
		volName, req.GetVolumeId(), stagingtargetPath)

	// by spec, we have to return OK if asked volume is not mounted on asked path,
	// so we look up the current device by volumeID and see is that device
	// mounted on staging target path
	_, err := ns.dm.GetDevice(req.VolumeId)
	if err != nil {
		klog.Errorf("NodeUnstageVolume: did not find volume %s", req.GetVolumeId())
		return nil, err
	}

	// Find out device name for mounted path
	mounter := mount.New("")
	mountedDev, _, err := mount.GetDeviceNameFromMount(mounter, stagingtargetPath)
	if err != nil {
		klog.Errorf("NodeUnstageVolume: Error getting device name for mount")
		return nil, err
	}
	if mountedDev == "" {
		klog.Warningf("NodeUnstageVolume: No device name for mount point '%v'", stagingtargetPath)
		return &csi.NodeUnstageVolumeResponse{}, nil
	}
	klog.V(4).Infof("NodeUnstageVolume: detected mountedDev: %v", mountedDev)
	klog.V(3).Infof("NodeUnStageVolume: umount %s", stagingtargetPath)
	if err := mounter.Unmount(stagingtargetPath); err != nil {
		klog.Errorf("NodeUnstageVolume: Umount failed: %v", err)
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

// ensureDirectory checks if the given directory is existing, if not attempts to create it.
// retruns true, if direcotry pre-exists, otherwise false.
func ensureDirectory(mounter mount.Interface, dir string) (bool, error) {
	if _, err := os.Stat(dir); err != nil {
		if os.IsNotExist(err) {
			return true, nil
		}
		return false, status.Errorf(codes.Internal, "Failed to check existence of directory %q: %s", dir, err.Error())
	}

	if err := os.MkdirAll(dir, os.FileMode(0755)); err != nil && !os.IsExist(err) {
		return false, status.Errorf(codes.Internal, "Could not create dir %q: %v", dir, err)
	}

	return false, nil
}

func (ns *nodeServer) NodeExpandVolume(context.Context, *csi.NodeExpandVolumeRequest) (*csi.NodeExpandVolumeResponse, error) {
	return nil, status.Error(codes.Unimplemented, "")
}
