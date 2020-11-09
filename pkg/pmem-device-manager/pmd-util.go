package pmdmanager

import (
	"fmt"
	"os"
	"strconv"
	"time"

	pmemerr "github.com/intel/pmem-csi/pkg/errors"
	pmemexec "github.com/intel/pmem-csi/pkg/exec"
	"golang.org/x/sys/unix"
	"k8s.io/klog/v2"
)

const (
	retryStatTimeout time.Duration = 100 * time.Millisecond
)

func clearDevice(dev *PmemDeviceInfo, flush bool) error {
	klog.V(4).Infof("ClearDevice: path: %v flush:%v", dev.Path, flush)
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
		klog.Errorf("clearDevice: %s does not exist", dev.Path)
		return err
	}

	// Check if device
	if (fileinfo.Mode() & os.ModeDevice) == 0 {
		klog.Errorf("clearDevice: %s is not device", dev.Path)
		return fmt.Errorf("%s is not device", dev.Path)
	}

	fd, err := unix.Open(dev.Path, unix.O_RDONLY|unix.O_EXCL|unix.O_CLOEXEC, 0)
	defer unix.Close(fd)

	if err != nil {
		return fmt.Errorf("failed to clear device %q: %w", dev.Path, pmemerr.DeviceInUse)
	}

	if blocks == 0 {
		klog.V(5).Infof("Wiping entire device: %s", dev.Path)
		// use one iteration instead of shred's default=3 for speed
		if _, err := pmemexec.RunCommand("shred", "-n", "1", dev.Path); err != nil {
			return fmt.Errorf("device shred failure: %v", err.Error())
		}
	} else {
		klog.V(5).Infof("Zeroing %d 1k blocks at start of device: %s Size %v", blocks, dev.Path, dev.Size)
		of := "of=" + dev.Path
		// guard against writing more than volume size
		if blocks*1024 > dev.Size {
			blocks = dev.Size / 1024
		}
		count := "count=" + strconv.FormatUint(blocks, 10)
		if _, err := pmemexec.RunCommand("dd", "if=/dev/zero", of, "bs=1024", count); err != nil {
			return fmt.Errorf("device zeroing failure: %v", err.Error())
		}
	}
	return nil
}

func waitDeviceAppears(dev *PmemDeviceInfo) error {
	for i := 0; i < 10; i++ {
		if _, err := os.Stat(dev.Path); err == nil {
			return nil
		}

		klog.Warningf("waitDeviceAppears[%d]: %s does not exist, sleep %v and retry",
			i, dev.Path, retryStatTimeout)
		time.Sleep(retryStatTimeout)
	}
	return fmt.Errorf("%s: device not ready", dev.Path)
}
