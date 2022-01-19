//go:build linux
// +build linux

/*
Copyright 2018 The Kubernetes Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

// Forked from https://github.com/kubernetes/kubernetes/raw/8a09460c2f7ba8f6acd8a6fb7603ed3ac4805eb6/pkg/volume/util/volumepathhandler/volume_path_handler_linux.go
// to add file offset to AttachFileDevice.

package volumepathhandler

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"

	pmemlog "github.com/intel/pmem-csi/pkg/logger"
	"golang.org/x/sys/unix"

	"k8s.io/apimachinery/pkg/types"
)

// AttachFileDevice takes a path to a regular file and makes it available as an
// attached block device.
func (v VolumePathHandler) AttachFileDevice(ctx context.Context, path string) (string, error) {
	return v.AttachFileDeviceWithOffset(ctx, path, 0)
}

// AttachFileDevice takes a path to a regular file and makes its content starting
// at the given offset available as an attached block device.
func (v VolumePathHandler) AttachFileDeviceWithOffset(ctx context.Context, path string, offset int64) (string, error) {
	ctx, logger := pmemlog.WithName(ctx, "AttachFileDevice")
	blockDevicePath, err := v.GetLoopDevice(ctx, path)
	if err != nil && err.Error() != ErrDeviceNotFound {
		return "", fmt.Errorf("GetLoopDevice failed for path %s: %v", path, err)
	}

	// If no existing loop device for the path, create one
	if blockDevicePath == "" {
		logger.V(4).Info("Creating device", "path", path)
		blockDevicePath, err = makeLoopDevice(ctx, path, offset)
		if err != nil {
			return "", fmt.Errorf("makeLoopDevice failed for path %s: %v", path, err)
		}
	}
	return blockDevicePath, nil
}

// DetachFileDevice takes a path to the attached block device and
// detach it from block device.
func (v VolumePathHandler) DetachFileDevice(ctx context.Context, path string) error {
	ctx, logger := pmemlog.WithName(ctx, "DetachFileDevice")
	loopPath, err := v.GetLoopDevice(ctx, path)
	if err != nil {
		if err.Error() == ErrDeviceNotFound {
			logger.V(2).Info("couldn't find loopback device which takes file descriptor lock. Skip detaching device.", "path", path)
		} else {
			return fmt.Errorf("GetLoopDevice failed for path %s: %v", path, err)
		}
	} else {
		if len(loopPath) != 0 {
			err = removeLoopDevice(ctx, loopPath)
			if err != nil {
				return fmt.Errorf("removeLoopDevice failed for path %s: %v", path, err)
			}
		}
	}
	return nil
}

// GetLoopDevice returns the full path to the loop device associated with the given path.
func (v VolumePathHandler) GetLoopDevice(ctx context.Context, path string) (string, error) {
	ctx, logger := pmemlog.WithName(ctx, "GetLoopDevice")
	_, err := os.Stat(path)
	if os.IsNotExist(err) {
		logger.V(2).Info("Loop device backing file not found, assuming loop device does not exist either", "path", path)
		return "", errors.New(ErrDeviceNotFound)
	}
	if err != nil {
		return "", fmt.Errorf("not attachable: %v", err)
	}

	args := []string{"-j", path}
	cmd := exec.Command(losetupPath, args...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		logger.V(2).Info("Failed device discover command", "path", path, "error", err, "stdout", string(out))
		return "", fmt.Errorf("losetup -j %s failed: %v", path, err)
	}
	logger.V(5).Info("losetup -j", "path", path, "stdout", string(out))
	return parseLosetupOutputForDevice(out, path)
}

func makeLoopDevice(ctx context.Context, path string, offset int64) (string, error) {
	ctx, logger := pmemlog.WithName(ctx, "makeLoopDevice")
	args := []string{"-f", "--show"}
	if offset != 0 {
		args = append(args, "-o", fmt.Sprintf("%d", offset))
	}
	args = append(args, path)
	cmd := exec.Command(losetupPath, args...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		logger.V(2).Info("Failed device create command", "path", path, "error", err, "stdout", out)
		return "", fmt.Errorf("losetup -f --show %s failed: %v", path, err)
	}

	// losetup -f --show {path} returns device in the format:
	// /dev/loop1
	if len(out) == 0 {
		return "", errors.New(ErrDeviceNotFound)
	}

	return strings.TrimSpace(string(out)), nil
}

// removeLoopDevice removes specified loopback device
func removeLoopDevice(ctx context.Context, device string) error {
	ctx, logger := pmemlog.WithName(ctx, "removeLoopDevice")
	args := []string{"-d", device}
	cmd := exec.Command(losetupPath, args...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		if _, err := os.Stat(device); os.IsNotExist(err) {
			return nil
		}
		logger.V(2).Info("Failed to remove loopback device", "device", device, "error", err, "stdout", string(out))
		return fmt.Errorf("losetup -d %s failed: %v", device, err)
	}
	return nil
}

var offsetSuffix = regexp.MustCompile(`, offset \d+$`)

func parseLosetupOutputForDevice(output []byte, path string) (string, error) {
	if len(output) == 0 {
		return "", errors.New(ErrDeviceNotFound)
	}

	// losetup -j {path} returns device in the format:
	// /dev/loop1: [0073]:148662 ({path})
	// /dev/loop2: [0073]:148662 (/dev/sdX)
	//
	// losetup -j shows all the loop device for the same device that has the same
	// major/minor number, by resolving symlink and matching major/minor number.
	// Therefore, there will be other path than {path} in output, as shown in above output.
	//
	// Optionally the lines have ", offset {offset}" at the end. We strip those
	// and return all loop devices that refer to the same file.
	s := string(output)
	// Find the line that exact matches to the path, or "({path})"
	var matched string
	scanner := bufio.NewScanner(strings.NewReader(s))
	for scanner.Scan() {
		// Remove the ", offset ..." suffix?
		line := scanner.Text()
		index := offsetSuffix.FindStringSubmatchIndex(line)
		if index != nil {
			line = line[0:index[0]]
		}

		// Does it now match the file path?
		if strings.HasSuffix(line, "("+path+")") {
			matched = line
			break
		}
	}
	if len(matched) == 0 {
		return "", errors.New(ErrDeviceNotFound)
	}
	s = matched

	// Get device name, or the 0th field of the output separated with ":".
	// We don't need 1st field or later to be splitted, so passing 2 to SplitN.
	device := strings.TrimSpace(strings.SplitN(s, ":", 2)[0])
	if len(device) == 0 {
		return "", errors.New(ErrDeviceNotFound)
	}
	return device, nil
}

// FindGlobalMapPathUUIDFromPod finds {pod uuid} bind mount under globalMapPath
// corresponding to map path symlink, and then return global map path with pod uuid.
// (See pkg/volume/volume.go for details on a global map path and a pod device map path.)
// ex. mapPath symlink: pods/{podUid}}/{DefaultKubeletVolumeDevicesDirName}/{escapeQualifiedPluginName}/{volumeName} -> /dev/sdX
//     globalMapPath/{pod uuid} bind mount: plugins/kubernetes.io/{PluginName}/{DefaultKubeletVolumeDevicesDirName}/{volumePluginDependentPath}/{pod uuid} -> /dev/sdX
func (v VolumePathHandler) FindGlobalMapPathUUIDFromPod(ctx context.Context, pluginDir, mapPath string, podUID types.UID) (string, error) {
	ctx, logger := pmemlog.WithName(ctx, "FindGlobalMapPathUUIDFromPod")
	var globalMapPathUUID string
	// Find symbolic link named pod uuid under plugin dir
	err := filepath.Walk(pluginDir, func(path string, fi os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if (fi.Mode()&os.ModeDevice == os.ModeDevice) && (fi.Name() == string(podUID)) {
			logger.V(5).Info("Checking", "path", path, "map-path", mapPath)
			if res, err := compareBindMountAndSymlinks(ctx, path, mapPath); err == nil && res {
				globalMapPathUUID = path
			}
		}
		return nil
	})
	if err != nil {
		return "", fmt.Errorf("FindGlobalMapPathUUIDFromPod failed: %v", err)
	}
	logger.V(5).Info("Found UUID", "global-map-path-uuid", globalMapPathUUID)
	// Return path contains global map path + {pod uuid}
	return globalMapPathUUID, nil
}

// compareBindMountAndSymlinks returns if global path (bind mount) and
// pod path (symlink) are pointing to the same device.
// If there is an error in checking it returns error.
func compareBindMountAndSymlinks(ctx context.Context, global, pod string) (bool, error) {
	ctx, logger := pmemlog.WithName(ctx, "compareBindMountAndSymlinks")

	// To check if bind mount and symlink are pointing to the same device,
	// we need to check if they are pointing to the devices that have same major/minor number.

	// Get the major/minor number for global path
	devNumGlobal, err := getDeviceMajorMinor(global)
	if err != nil {
		return false, fmt.Errorf("getDeviceMajorMinor failed for path %s: %v", global, err)
	}

	// Get the symlinked device from the pod path
	devPod, err := os.Readlink(pod)
	if err != nil {
		return false, fmt.Errorf("failed to readlink path %s: %v", pod, err)
	}
	// Get the major/minor number for the symlinked device from the pod path
	devNumPod, err := getDeviceMajorMinor(devPod)
	if err != nil {
		return false, fmt.Errorf("getDeviceMajorMinor failed for path %s: %v", devPod, err)
	}
	logger.V(5).Info("Checking", "dev-num-global", devNumGlobal, "dev-num-pod", devNumPod)

	// Check if the major/minor number are the same
	if devNumGlobal == devNumPod {
		return true, nil
	}
	return false, nil
}

// getDeviceMajorMinor returns major/minor number for the path with below format:
// major:minor (in hex)
// ex)
//     fc:10
func getDeviceMajorMinor(path string) (string, error) {
	var stat unix.Stat_t

	if err := unix.Stat(path, &stat); err != nil {
		return "", fmt.Errorf("failed to stat path %s: %v", path, err)
	}

	devNumber := uint64(stat.Rdev)
	major := unix.Major(devNumber)
	minor := unix.Minor(devNumber)

	return fmt.Sprintf("%x:%x", major, minor), nil
}
