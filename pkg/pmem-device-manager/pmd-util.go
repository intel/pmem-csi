package pmdmanager

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"time"

	"k8s.io/klog/v2"

	pmemerr "github.com/intel/pmem-csi/pkg/errors"
	pmemexec "github.com/intel/pmem-csi/pkg/exec"
	"golang.org/x/sys/unix"
)

const (
	retryStatTimeout time.Duration = 100 * time.Millisecond
)

func clearDevice(ctx context.Context, dev *PmemDeviceInfo, flush bool) error {
	logger := klog.FromContext(ctx).WithName("clearDevice").WithValues("device", dev.Path)
	ctx = klog.NewContext(ctx, logger)
	logger.V(4).Info("Starting", "flush", flush)

	// by default, clear 4 kbytes to avoid recognizing file system by next volume seeing data area
	var blocks uint64 = 4
	if flush {
		// clear all data if "erase all" asked specifically
		blocks = 0
	}

	// erase data on block device.
	// zero number of blocks causes overwriting whole device with random data.
	// nonzero number of blocks clears blocks*1024 bytes.
	// Before action, check that dev.Path exists and is device
	fileinfo, err := os.Stat(dev.Path)
	if err != nil {
		return fmt.Errorf("clear device: %v", err)
	}

	// Check if device
	if (fileinfo.Mode() & os.ModeDevice) == 0 {
		return fmt.Errorf("%s is not device", dev.Path)
	}

	fd, err := unix.Open(dev.Path, unix.O_RDONLY|unix.O_EXCL|unix.O_CLOEXEC, 0)
	defer unix.Close(fd)

	if err != nil {
		return fmt.Errorf("failed to clear device %q: %w", dev.Path, pmemerr.DeviceInUse)
	}

	if blocks == 0 {
		logger.V(5).Info("Wiping entire device")
		// shred would write n times using random data, followed by optional write of zeroes.
		// For faster operation, and because we consider zeroing enough for
		// reasonable clearing in case of a memory device, we force zero iterations
		// with random data, followed by one pass writing zeroes.
		if _, err := pmemexec.RunCommand(ctx, "shred", "-n", "0", "-z", dev.Path); err != nil {
			return fmt.Errorf("device shred failure: %v", err.Error())
		}
	} else {
		logger.V(5).Info("Zeroing blocks at start of device", "blocks", blocks, "dev-size", dev.Size)
		of := "of=" + dev.Path
		// guard against writing more than volume size
		if blocks*1024 > dev.Size {
			blocks = dev.Size / 1024
		}
		count := "count=" + strconv.FormatUint(blocks, 10)
		if _, err := pmemexec.RunCommand(ctx, "dd", "if=/dev/zero", of, "bs=1024", count); err != nil {
			return fmt.Errorf("device zeroing failure: %v", err.Error())
		}
	}
	return nil
}

func waitDeviceAppears(ctx context.Context, dev *PmemDeviceInfo) error {
	logger := klog.FromContext(ctx).WithName("waitDeviceAppears").WithValues("device", dev.Path)
	for i := 0; i < 10; i++ {
		if _, err := os.Stat(dev.Path); err == nil {
			return nil
		}

		logger.V(2).Info("Device does not exist, sleep and retry",
			"attempt", i,
			"timeout", retryStatTimeout,
		)
		time.Sleep(retryStatTimeout)
	}
	return fmt.Errorf("%s: device not ready", dev.Path)
}
