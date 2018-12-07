package pmdmanager

import (
	"fmt"

	"github.com/intel/pmem-csi/pkg/ndctl"
	"k8s.io/klog/glog"
)

type pmemNdctl struct {
	ctx *ndctl.Context
}

var _ PmemDeviceManager = &pmemNdctl{}

//NewPmemDeviceManagerNdctl Instantiates a new ndctl based pmem device manager
func NewPmemDeviceManagerNdctl() (PmemDeviceManager, error) {
	ctx, err := ndctl.NewContext()
	if err != nil {
		return nil, fmt.Errorf("Failed to initialize pmem context: %s", err.Error())
	}
	return &pmemNdctl{
		ctx: ctx,
	}, nil
}

func (pmem *pmemNdctl) GetCapacity() (uint64, error) {
	var capacity uint64
	for _, bus := range pmem.ctx.GetBuses() {
		for _, r := range bus.ActiveRegions() {
			available := r.MaxAvailableExtent()
			if available > capacity {
				capacity = available
			}
		}
	}
	return capacity, nil
}

func (pmem *pmemNdctl) CreateDevice(name string, size uint64, nsmode string) error {
	ns, err := pmem.ctx.CreateNamespace(ndctl.CreateNamespaceOpts{
		Name: name,
		Size: size,
		Mode: ndctl.NamespaceMode(nsmode),
	})
	if err != nil {
		return err
	}
	data, _ := ns.MarshalJSON() //nolint: gosec
	glog.Infof("Namespace crated: %v", data)

	return nil
}

func (pmem *pmemNdctl) DeleteDevice(name string, flush bool) error {
	if flush {
		if err := pmem.FlushDeviceData(name); err != nil {
			glog.Warningf("DeleteDevice: %s\n", err.Error())
		}
	}
	return pmem.ctx.DestroyNamespaceByName(name)
}

func (pmem *pmemNdctl) FlushDeviceData(name string) error {
	return fmt.Errorf("Unsupported for pmem devices")
}

func (pmem *pmemNdctl) GetDevice(name string) (PmemDeviceInfo, error) {
	ns, err := pmem.ctx.GetNamespaceByName(name)
	if err != nil {
		return PmemDeviceInfo{}, err
	}

	return namespaceToPmemInfo(ns), nil
}

func (pmem *pmemNdctl) ListDevices() ([]PmemDeviceInfo, error) {
	devices := []PmemDeviceInfo{}
	for _, ns := range pmem.ctx.GetActiveNamespaces() {
		devices = append(devices, namespaceToPmemInfo(ns))
	}
	return devices, nil
}

func namespaceToPmemInfo(ns *ndctl.Namespace) PmemDeviceInfo {
	return PmemDeviceInfo{
		Name: ns.Name(),
		Path: "/dev/" + ns.BlockDeviceName(),
		Size: ns.Size(),
	}
}
