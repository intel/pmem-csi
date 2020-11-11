/*
Copyright 2020 Intel Corporation.

SPDX-License-Identifier: Apache-2.0
*/

package pmdmanager

import (
	"fmt"
	"sync"

	api "github.com/intel/pmem-csi/pkg/apis/pmemcsi/v1alpha1"

	pmemerr "github.com/intel/pmem-csi/pkg/errors"
)

type fakeDM struct {
	capacity uint64
	mutex    sync.Mutex

	devices map[string]*PmemDeviceInfo
}

var _ PmemDeviceManager = &fakeDM{}

const totalCapacity uint64 = 1024 * 1024 * 1024 * 1024

// NewFake instantiates a fake PMEM device manager. The overall capacity
// is hard-coded as 1TB. Usable capacity can be configured via the
// percentage. Space is assumed to be contiguous with no fragmentation
// issues.
func newFake(pmemPercentage uint) (PmemDeviceManager, error) {
	if pmemPercentage > 100 {
		return nil, fmt.Errorf("invalid pmemPercentage '%d'. Value must be 0..100", pmemPercentage)
	}

	return &fakeDM{
		capacity: uint64(pmemPercentage) * totalCapacity / 100,
		devices:  map[string]*PmemDeviceInfo{},
	}, nil
}

func (dm *fakeDM) GetMode() api.DeviceMode {
	return api.DeviceModeFake
}

func (dm *fakeDM) GetCapacity() (capacity Capacity, err error) {
	dm.mutex.Lock()
	defer dm.mutex.Unlock()

	return dm.getCapacity(), nil
}

func (dm *fakeDM) getCapacity() Capacity {
	remaining := dm.capacity
	for _, dev := range dm.devices {
		remaining -= dev.Size
	}
	return Capacity{
		Available:     remaining,
		MaxVolumeSize: remaining,
		Managed:       dm.capacity,
		Total:         totalCapacity,
	}
}

func (dm *fakeDM) CreateDevice(volumeId string, size uint64) error {
	dm.mutex.Lock()
	defer dm.mutex.Unlock()

	_, ok := dm.devices[volumeId]
	if ok {
		return pmemerr.DeviceExists
	}

	if size > dm.getCapacity().MaxVolumeSize {
		return pmemerr.NotEnoughSpace
	}

	dm.devices[volumeId] = &PmemDeviceInfo{
		VolumeId: volumeId,
		Size:     size,
		Path:     FakeDevicePathPrefix + volumeId,
	}
	return nil
}

func (dm *fakeDM) DeleteDevice(volumeId string, flush bool) error {
	dm.mutex.Lock()
	defer dm.mutex.Unlock()

	// Remove device, whether it exists or not.
	delete(dm.devices, volumeId)

	return nil
}

func (dm *fakeDM) ListDevices() ([]*PmemDeviceInfo, error) {
	dm.mutex.Lock()
	defer dm.mutex.Unlock()

	devices := []*PmemDeviceInfo{}
	for _, dev := range dm.devices {
		devices = append(devices, dev)
	}

	return devices, nil
}

func (dm *fakeDM) GetDevice(volumeId string) (*PmemDeviceInfo, error) {
	dm.mutex.Lock()
	defer dm.mutex.Unlock()

	dev, ok := dm.devices[volumeId]
	if !ok {
		return nil, pmemerr.DeviceNotFound
	}
	return dev, nil
}
