/*
Copyright 2017 The Kubernetes Authors.

SPDX-License-Identifier: Apache-2.0
*/

package pmemcsidriver

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/container-storage-interface/spec/lib/go/csi"
	"golang.org/x/net/context"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"k8s.io/klog"
	"k8s.io/kubernetes/pkg/util/mount"

	units "github.com/docker/go-units"
	pmdmanager "github.com/intel/pmem-csi/pkg/pmem-device-manager"
	pmemexec "github.com/intel/pmem-csi/pkg/pmem-exec"
)

const (
	// Kubernetes v1.16+ would add this option to NodePublishRequest.VolumeContext
	// while provisioning ephemeral volume
	volumeContextEphemeral = "csi.storage.k8s.io/ephemeral"
	// ProvisionerIdentity is supposed to pass to NodePublish
	// only in case of Persistent volumes that were provisioned by
	// the driver
	volumeProvisionerIdentity = "storage.kubernetes.io/csiProvisionerIdentity"
	volumeContextSize         = "size"
	defaultFilesystem         = "ext4"
)

type nodeServer struct {
	nodeCaps []*csi.NodeServiceCapability
	cs       *nodeControllerServer
	// Driver deployed to provision only ephemeral volumes(only for Kubernetes v1.15)
	mounter       mount.Interface
	volRO         map[string]bool
	volTargetPath map[string]string
	volFstype     map[string]string
	volMountflags map[string][]string
}

var _ csi.NodeServer = &nodeServer{}
var _ PmemService = &nodeServer{}

func NewNodeServer(cs *nodeControllerServer) *nodeServer {
	return &nodeServer{
		nodeCaps: []*csi.NodeServiceCapability{
			{
				Type: &csi.NodeServiceCapability_Rpc{
					Rpc: &csi.NodeServiceCapability_RPC{
						Type: csi.NodeServiceCapability_RPC_STAGE_UNSTAGE_VOLUME,
					},
				},
			},
		},
		cs:            cs,
		mounter:       mount.New(""),
		volRO:         map[string]bool{},
		volTargetPath: map[string]string{},
		volFstype:     map[string]string{},
		volMountflags: map[string][]string{},
	}
}

func (ns *nodeServer) RegisterService(rpcServer *grpc.Server) {
	csi.RegisterNodeServer(rpcServer, ns)
}

func (ns *nodeServer) NodeGetInfo(ctx context.Context, req *csi.NodeGetInfoRequest) (*csi.NodeGetInfoResponse, error) {
	return &csi.NodeGetInfoResponse{
		NodeId: ns.cs.nodeID,
		AccessibleTopology: &csi.Topology{
			Segments: map[string]string{
				PmemDriverTopologyKey: ns.cs.nodeID,
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

	// Serialize by VolumeId
	volumeMutex.LockKey(req.GetVolumeId())
	defer volumeMutex.UnlockKey(req.GetVolumeId())

	var ephemeral bool
	var device *pmdmanager.PmemDeviceInfo
	var mountOptions []string
	var err error
	srcPath := req.GetStagingTargetPath()
	targetPath := req.GetTargetPath()
	readOnly := req.GetReadonly()
	mountFlags := req.GetVolumeCapability().GetMount().GetMountFlags()
	fsType := req.GetVolumeCapability().GetMount().GetFsType()
	klog.V(3).Infof("NodePublishVolume request: VolID:%s targetpath:%v sourcepath:%v readonly:%v Capab/mountflags:%v Capab/fsType:%v",
		req.GetVolumeId(), targetPath, srcPath, readOnly, mountFlags, fsType)

	// Kubernetes v1.16+ would request ephemeral volumes via VolumeContext
	val, ok := req.GetVolumeContext()[volumeContextEphemeral]
	if ok {
		b, err := strconv.ParseBool(val)
		if err != nil {
			return nil, status.Error(codes.InvalidArgument, err.Error())
		}
		ephemeral = b
	} else {
		// If met all below conditions we still treat as a ephemeral volume request:
		// 1) No Volume found with given volume id
		// 2) No provisioner info found in VolumeContext "storage.kubernetes.io/csiProvisionerIdentity"
		// 3) No StagingPath in the request
		// TODO(avalluri): Inspect the error returned by GetDevice() once we
		// implement the error codes in DeviceManager
		device, _ = ns.cs.dm.GetDevice(req.VolumeId)
		_, ok := req.GetVolumeContext()[volumeProvisionerIdentity]
		ephemeral = device == nil && !ok && len(srcPath) == 0
	}

	if ephemeral {
		device, err := ns.createEphemeralDevice(ctx, req)
		if err != nil {
			// createEphemeralDevice() returns status.Error, so safe to return
			return nil, err
		}
		srcPath = device.Path
		params := req.GetVolumeContext()
		if nsmode, ok := params[pmemParameterKeyNamespaceMode]; !ok || nsmode == pmemNamespaceModeFsdax {
			mountOptions = []string{"dax"}
		}
	} else {
		device, err = ns.cs.dm.GetDevice(req.VolumeId)
		// TODO(avalluri): Inspect the error returned by GetDevice() once we
		// implement the error codes in DeviceManager
		if err != nil {
			return nil, status.Error(codes.NotFound, "No device found with volume id "+req.VolumeId+": "+err.Error())
		}

		mountOptions = []string{"bind"}
		// TODO: check is bind-mount already made
		// (happens when publish is asked repeatedly for already published
		// namespace)
	}

	switch req.VolumeCapability.GetAccessType().(type) {
	case *csi.VolumeCapability_Block:
		// For block volumes, source path is the actual Device path
		srcPath = device.Path
		targetDir := filepath.Dir(targetPath)
		// Make sure that parent directory of target path is existing, otherwise create it
		targetPreExisting, err := ensureDirectory(ns.mounter, targetDir)
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
		if !ephemeral && len(srcPath) == 0 {
			return nil, status.Error(codes.InvalidArgument, "Staging target path missing in request")
		}

		// Check is bind-mount already made,
		// happens when publish is asked repeatedly for already published volume.
		notMnt, err := mount.IsNotMountPoint(ns.mounter, targetPath)
		if err != nil && !os.IsNotExist(err) {
			return nil, status.Error(codes.Internal, "validate target path: "+err.Error())
		}
		if !notMnt {
			// TODO(https://github.com/kubernetes-sigs/gcp-compute-persistent-disk-csi-driver/issues/95):
			// Check if mount is compatible. Return OK if these match:
			// 1) Requested target path MUST match the published path of that volume ID
			// 2) VolumeCapability MUST match
			//   Note that we do not check VolumeCapability/fsType as fsType may not be present (is used in NodeStageVolume scope).
			//   Means, we check only mountflags part of Capability.
			//   Mount flags are coded as []string, so instead of creating a loop, we join mount flags to single string
			//   for easier comparing. Note that if these appear in different order, comparison may fail to find match.
			// 3) Readonly MUST match
			// If there is mismatch of any of above, we return ALREADY_EXISTS error.
			klog.V(5).Infof("NodePublishVolume[%s]: existing RO=%v TargetPath=%v, mountFlags=[%v]",
				req.VolumeId, ns.volRO[req.VolumeId], ns.volTargetPath[req.VolumeId], ns.volMountflags[req.VolumeId])
			storedMountFlags := strings.Join(ns.volMountflags[req.VolumeId][:], ",")
			askedMountFlags := strings.Join(mountFlags[:], ",")
			if readOnly == ns.volRO[req.VolumeId] && targetPath == ns.volTargetPath[req.VolumeId] && storedMountFlags == askedMountFlags {
				klog.V(5).Infof("NodePublishVolume[%s]: paremeters match existing, return OK", req.VolumeId)
				return &csi.NodePublishVolumeResponse{}, nil
			} else {
				klog.V(5).Infof("NodePublishVolume[%s]: paremeters do not match existing, return ALREADY_EXISTS", req.VolumeId)
				return nil, status.Error(codes.AlreadyExists, "Volume published but is incompatible")
			}
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

	if req.GetReadonly() {
		mountOptions = append(mountOptions, "ro")
	}

	if err := ns.mount(srcPath, targetPath, mountOptions); err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}

	ns.volRO[req.VolumeId] = readOnly
	ns.volTargetPath[req.VolumeId] = targetPath
	ns.volMountflags[req.VolumeId] = mountFlags
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

	// Check if the target path is really a mount point. If its not a mount point do nothing
	if notMnt, err := ns.mounter.IsLikelyNotMountPoint(targetPath); notMnt || err != nil && !os.IsNotExist(err) {
		klog.V(5).Infof("NodeUnpublishVolume: %s is not mount point, skip", targetPath)
		return &csi.NodeUnpublishVolumeResponse{}, nil
	}

	// Unmounting the image
	klog.V(3).Infof("NodeUnpublishVolume: unmount %s", targetPath)
	err := ns.mounter.Unmount(targetPath)
	if err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}
	klog.V(5).Infof("NodeUnpublishVolume: volume id:%s targetpath:%s has been unmounted", volumeID, targetPath)

	os.Remove(targetPath) // nolint: gosec, errorchk

	var vol *nodeVolume
	if vol = ns.cs.getVolumeByID(req.VolumeId); vol == nil {
		// For ephemeral volumes we use req.VolumeId as volume name.
		vol = ns.cs.getVolumeByName(req.VolumeId)
	}

	if vol == nil {
		return nil, status.Error(codes.InvalidArgument, fmt.Sprintf("No volume found with volume id '%s'", req.VolumeId))
	}

	if vol.Params[pmemParameterKeyPersistencyModel] == string(pmemPersistencyModelEphemeral) {
		if _, err := ns.cs.DeleteVolume(ctx, &csi.DeleteVolumeRequest{VolumeId: vol.ID}); err != nil {
			return nil, status.Error(codes.Internal, fmt.Sprintf("Failed to delete ephemeral volume %s: %s", req.VolumeId, err.Error()))
		}
	}
	ns.volRO[req.VolumeId] = false
	ns.volTargetPath[req.VolumeId] = ""
	ns.volMountflags[req.VolumeId] = []string{}

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

	device, err := ns.cs.dm.GetDevice(req.VolumeId)
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
	if existingFsType != "" && existingFsType != requestedFsType {
		klog.Errorf("NodeStageVolume: File system with different type %v exists on %s",
			existingFsType, device.Path)
		return nil, status.Error(codes.AlreadyExists, "File system with different type exists")
	}
	// Check is something already mounted to stagingtargetPath
	mounter := mount.New("")
	mountedDev, _, err := mount.GetDeviceNameFromMount(mounter, stagingtargetPath)
	if err != nil {
		klog.Errorf("NodeStageVolume: Error getting device name for mount")
		return nil, err
	}
	if mountedDev == device.Path {
		klog.V(4).Infof("NodeStageVolume: Device %s already mounted to path %s", mountedDev, stagingtargetPath)
		// Check are the capabilities compatible.
		if existingFsType == requestedFsType {
			// If fstype compatible, return OK.
			klog.V(4).Infof("NodeStageVolume: Device:%s fsType:%s matches", mountedDev, requestedFsType)
			return &csi.NodeStageVolumeResponse{}, nil
		}
	}
	if err = ns.provisionDevice(device, req.GetVolumeCapability().GetMount().GetFsType()); err != nil {
		return nil, err
	}

	mountOptions := []string{}
	// FIXME(avalluri): we shouldn't depend on volumecontext to determine the device mode,
	// instead PmemDeviceInfo should hold the device mode in which it was created.
	if params := req.GetVolumeContext(); params != nil {
		if nsmode, ok := params[pmemParameterKeyNamespaceMode]; !ok || nsmode == pmemNamespaceModeFsdax {
			// Add dax option if namespacemode == fsdax
			mountOptions = append(mountOptions, "dax")
		}
	}

	if err = ns.mount(device.Path, stagingtargetPath, mountOptions); err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}

	// We store fsType in volume registry, but currently it is not used for anything.
	ns.volFstype[req.VolumeId] = requestedFsType
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

	// Serialize by VolumeId
	volumeMutex.LockKey(req.GetVolumeId())
	defer volumeMutex.UnlockKey(req.GetVolumeId())

	klog.V(4).Infof("NodeUnStageVolume: id:%v Staging target path:%s",
		req.GetVolumeId(), stagingtargetPath)

	// by spec, we have to return OK if asked volume is not mounted on asked path,
	// so we look up the current device by volumeID and see is that device
	// mounted on staging target path
	_, err := ns.cs.dm.GetDevice(req.VolumeId)
	if err != nil {
		klog.Errorf("NodeUnstageVolume: did not find volume %s", req.GetVolumeId())
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}

	// Find out device name for mounted path
	mountedDev, _, err := mount.GetDeviceNameFromMount(ns.mounter, stagingtargetPath)
	if err != nil {
		return nil, err
	}
	if mountedDev == "" {
		klog.Warningf("NodeUnstageVolume: No device name for mount point '%v'", stagingtargetPath)
		return &csi.NodeUnstageVolumeResponse{}, nil
	}
	klog.V(4).Infof("NodeUnstageVolume: detected mountedDev: %v", mountedDev)
	klog.V(3).Infof("NodeUnStageVolume: umount %s", stagingtargetPath)
	if err := ns.mounter.Unmount(stagingtargetPath); err != nil {
		klog.Errorf("NodeUnstageVolume: Umount failed: %v", err)
		return nil, err
	}

	ns.volFstype[req.VolumeId] = ""
	return &csi.NodeUnstageVolumeResponse{}, nil
}

func (ns *nodeServer) NodeExpandVolume(context.Context, *csi.NodeExpandVolumeRequest) (*csi.NodeExpandVolumeResponse, error) {
	return nil, status.Error(codes.Unimplemented, "")
}

// createEphemeralDevice creates new pmem device for given req.
// On failure it returns one of status errors.
func (ns *nodeServer) createEphemeralDevice(ctx context.Context, req *csi.NodePublishVolumeRequest) (*pmdmanager.PmemDeviceInfo, error) {
	var size *int64

	for key, val := range req.GetVolumeContext() {
		switch key {
		case volumeContextSize:
			s, err := units.FromHumanSize(val)
			if err != nil {
				return nil, status.Error(codes.InvalidArgument, fmt.Sprintf("failed to parse size(%s) of ephemeral inline volume: %s", val, err.Error()))
			}
			size = &s
		case pmemParameterKeyNamespaceMode:
			if val != pmemNamespaceModeFsdax && val != pmemNamespaceModeSector {
				return nil, status.Error(codes.InvalidArgument, fmt.Sprintf("invalid namespace mode(nsmode) '%s' provided for ephemeral inline volume", val))
			}
		case pmemParameterKeyEraseAfter:
			if _, err := strconv.ParseBool(val); err != nil {
				return nil, status.Error(codes.InvalidArgument, fmt.Sprintf("failed to parse volume attribute(%s) value(%s) of ephemeral inline volume: %s", key, val, err.Error()))
			}
		default:
			if !strings.HasPrefix(key, "csi.storage.k8s.io/") { // System reserved attributes
				return nil, status.Error(codes.InvalidArgument, fmt.Sprintf("unknown volume attribute provided '%s' for ephemeral inline volume", key))
			}
		}
	}

	if size == nil {
		return nil, status.Error(codes.InvalidArgument, "size not specified for ephemeral inline volume")
	}

	// Create new device
	params := req.GetVolumeContext()
	params[pmemParameterKeyPersistencyModel] = string(pmemPersistencyModelEphemeral)
	createReq := &csi.CreateVolumeRequest{
		VolumeCapabilities: []*csi.VolumeCapability{req.VolumeCapability},
		Name:               req.VolumeId,
		CapacityRange:      &csi.CapacityRange{RequiredBytes: *size},
		Parameters:         params,
	}

	createResp, err := ns.cs.CreateVolume(ctx, createReq)
	if err != nil {
		return nil, status.Error(codes.Internal, fmt.Sprintf("failed to create new ephemeral inline volume: %s", err.Error()))
	}

	device, err := ns.cs.dm.GetDevice(createResp.Volume.VolumeId)
	if err != nil {
		return nil, status.Error(codes.Internal, fmt.Sprintf("could not retrieve newly created volume with id '%s': %s", createResp.Volume.VolumeId, err.Error()))
	}

	// Create filesystem
	if err := ns.provisionDevice(device, req.GetVolumeCapability().GetMount().GetFsType()); err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}

	return device, nil
}

// provisionDevice initializes the device with requested filesystem
// and mounts at given targetPath.
func (ns *nodeServer) provisionDevice(device *pmdmanager.PmemDeviceInfo, fsType string) error {
	// Empty FsType means "unspecified" and we pick default
	if fsType == "" {
		fsType = defaultFilesystem
	}

	cmd := ""
	var args []string
	// hard-code block size to 4k to avoid smaller values and trouble to dax mount option
	if fsType == "ext4" {
		cmd = "mkfs.ext4"
		args = []string{"-b 4096", "-F", device.Path}
	} else if fsType == "xfs" {
		cmd = "mkfs.xfs"
		args = []string{"-b", "size=4096", "-f", device.Path}
	} else {
		return status.Error(codes.Unimplemented, fmt.Sprintf("Unsupported filesystem '%s'. Supported filesystems types: 'xfs', 'ext4'", fsType))
	}
	output, err := pmemexec.RunCommand(cmd, args...)
	if err != nil {
		return status.Error(codes.Unimplemented, fmt.Sprintf("mkfs failed: %s", output))
	}

	return nil
}

func (ns *nodeServer) mount(sourcePath, targetPath string, mountOptions []string) error {
	notMnt, err := ns.mounter.IsLikelyNotMountPoint(targetPath)
	if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("failed to determain if '%s' is a valid mount point: %s", targetPath, err.Error())
	}
	if !notMnt {
		// TODO(https://github.com/kubernetes-sigs/gcp-compute-persistent-disk-csi-driver/issues/95): check if mount is compatible. Return OK if it is, or appropriate error.
		/*
			1) Target Path MUST be the vol referenced by vol ID
			2) VolumeCapability MUST match
			3) Readonly MUST match
		*/
		return nil
	}

	if err := os.Mkdir(targetPath, os.FileMode(0755)); err != nil {
		// Kubernetes is violating the CSI spec and creates the
		// directory for us
		// (https://github.com/kubernetes/kubernetes/issues/75535). We
		// allow that by ignoring the "already exists" error.
		if !os.IsExist(err) {
			return fmt.Errorf("failed to create '%s': %s", targetPath, err.Error())
		}
	}

	// If file system is already mounted, can happen if out-of-sync "stage" is asked again without unstage
	// then the mount here will fail. I guess it's ok to not check explicitly for existing mount,
	// as end result after mount attempt will be same: no new mount and existing mount remains.
	// TODO: cleaner is to explicitly check (although CSI spec may tell that out-of-order call is illegal (check it))

	// We supposed to use "mount" package - ns.mounter.Mount()
	// but it seems not supporting -c "canonical" option, so do it with exec()
	// added -c makes canonical mount, resulting in mounted path matching what LV thinks is lvpath.
	args := []string{"-c"}
	if len(mountOptions) != 0 {
		args = append(args, "-o", strings.Join(mountOptions, ","))
	}

	args = append(args, sourcePath, targetPath)
	klog.V(4).Infof("mount args: [%v]", args)
	if _, err := pmemexec.RunCommand("mount", args...); err != nil {
		return fmt.Errorf("mount filesystem failed: %s", err.Error())
	}

	return nil
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
