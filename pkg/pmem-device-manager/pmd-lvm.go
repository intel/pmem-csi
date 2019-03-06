package pmdmanager

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/intel/pmem-csi/pkg/ndctl"
	pmemexec "github.com/intel/pmem-csi/pkg/pmem-exec"
	"k8s.io/klog/glog"
)

type pmemLvm struct {
	volumeGroups []string
}

var _ PmemDeviceManager = &pmemLvm{}
var lvsArgs = []string{"--noheadings", "--nosuffix", "-o", "lv_name,lv_path,lv_size", "--units", "B"}
var vgsArgs = []string{"--noheadings", "--nosuffix", "-o", "vg_name,vg_size,vg_free,vg_tags", "--units", "B"}

// NewPmemDeviceManagerLVM Instantiates a new LVM based pmem device manager
// The pre-requisite for this manager is that all the pmem regions which should be managed by
// this LMV manager are devided into namespaces and grouped as volume groups.
func NewPmemDeviceManagerLVM() (PmemDeviceManager, error) {
	ctx, err := ndctl.NewContext()
	if err != nil {
		return nil, fmt.Errorf("Failed to initialize pmem context: %s", err.Error())
	}
	volumeGroups := []string{}
	for _, bus := range ctx.GetBuses() {
		for _, r := range bus.ActiveRegions() {
			nsmodes := []ndctl.NamespaceMode{ndctl.FsdaxMode, ndctl.SectorMode}
			for _, nsmod := range nsmodes {
				vgname := vgName(bus, r, nsmod)
				if _, err := pmemexec.RunCommand("vgs", vgname); err != nil {
					glog.V(5).Infof("NewPmemDeviceManagerLVM: VG %v non-existent, skip", vgname)
				} else {
					volumeGroups = append(volumeGroups, vgname)
				}
			}
		}
	}
	ctx.Free()

	return &pmemLvm{
		volumeGroups: volumeGroups,
	}, nil
}

type vgInfo struct {
	name string
	size uint64
	free uint64
	tag  string
}

func (lvm *pmemLvm) GetCapacity() (map[string]uint64, error) {
	capacity := map[string]uint64{}
	nsmodes := []ndctl.NamespaceMode{ndctl.FsdaxMode, ndctl.SectorMode}
	for _, nsmod := range nsmodes {
		vgs, err := getVolumeGroups(lvm.volumeGroups, string(nsmod))
		if err != nil {
			return nil, err
		}

		for _, vg := range vgs {
			if vg.free > capacity[string(nsmod)] {
				capacity[string(nsmod)] = vg.free
			}
		}
	}

	return capacity, nil
}

// nsmode is expected to be either "fsdax" or "sector"
func (lvm *pmemLvm) CreateDevice(name string, size uint64, nsmode string) error {
	if nsmode != string(ndctl.FsdaxMode) && nsmode != string(ndctl.SectorMode) {
		return fmt.Errorf("Unknown nsmode(%v)", nsmode)
	}
	// pick a region, few possible strategies:
	// 1. pick first with enough available space: simplest, regions get filled in order;
	// 2. pick first with largest available space: regions get used round-robin, i.e. load-balanced, but does not leave large unused;
	// 3. pick first with smallest available which satisfies the request: ordered initially, but later leaves bigger free available;
	// Let's implement strategy 1 for now, simplest to code as no need to compare sizes in all regions
	// NOTE: We walk buses and regions in ndctl context, but avail.size we check in LV context
	vgs, err := getVolumeGroups(lvm.volumeGroups, nsmode)
	if err != nil {
		return err
	}
	// lvcreate takes size in MBytes if no unit.
	// We use MBytes here to avoid problems with byte-granularity, as lvcreate
	// may refuse to create some arbitrary sizes.
	// Division by 1M should not result in smaller-than-asked here
	// as lvcreate will round up to next 4MB boundary.
	sizeM := int(size / (1024 * 1024))
	strSz := strconv.Itoa(sizeM)

	for _, vg := range vgs {
		if vg.free >= size {
			// lvcreate takes size in MBytes if no unit
			if _, err := pmemexec.RunCommand("lvcreate", "-L", strSz, "-n", name, vg.name); err != nil {
				glog.V(3).Infof("lvcreate failed with error: %v, trying for next free region", err)
			} else {
				// clear start of device to avoid old data being recognized as file system
				device, err := lvm.GetDevice(name)
				if err != nil {
					return err
				}
				err = ClearDevice(device, false)
				if err != nil {
					return err
				}
				return nil
			}
		}
	}
	return fmt.Errorf("No region is having enough space required(%v)", size)
}

func (lvm *pmemLvm) DeleteDevice(name string, flush bool) error {
	device, err := lvm.GetDevice(name)
	if err != nil {
		return err
	}
	err = ClearDevice(device, flush)
	if err != nil {
		return err
	}
	_, err = pmemexec.RunCommand("lvremove", "-fy", device.Path)
	return err
}

func (lvm *pmemLvm) FlushDeviceData(name string) error {
	device, err := lvm.GetDevice(name)
	if err != nil {
		return err
	}
	return ClearDevice(device, true)
}

func (lvm *pmemLvm) GetDevice(id string) (PmemDeviceInfo, error) {
	devices, err := lvm.ListDevices()
	if err != nil {
		return PmemDeviceInfo{}, err
	}
	for _, dev := range devices {
		if dev.Name == id {
			return dev, nil
		}
	}
	return PmemDeviceInfo{}, fmt.Errorf("Device not found with name %s", id)
}

func (lvm *pmemLvm) ListDevices() ([]PmemDeviceInfo, error) {
	args := append(lvsArgs, lvm.volumeGroups...)
	output, err := pmemexec.RunCommand("lvs", args...)
	if err != nil {
		return nil, fmt.Errorf("list volumes failed : %s(lvs output: %s)", err.Error(), output)
	}
	return parseLVSOuput(output)
}

func vgName(bus *ndctl.Bus, region *ndctl.Region, nsmode ndctl.NamespaceMode) string {
	return bus.DeviceName() + region.DeviceName() + string(nsmode)
}

//lvs options "lv_name,lv_path,lv_size,lv_free"
func parseLVSOuput(output string) ([]PmemDeviceInfo, error) {
	devices := []PmemDeviceInfo{}
	lines := strings.Split(string(output), "\n")
	for _, line := range lines {
		fields := strings.Fields(strings.TrimSpace(line))
		if len(fields) != 3 {
			continue
		}

		dev := PmemDeviceInfo{}
		dev.Name = fields[0]
		dev.Path = fields[1]
		dev.Size, _ = strconv.ParseUint(fields[2], 10, 64)

		devices = append(devices, dev)
	}

	return devices, nil
}

func getVolumeGroups(groups []string, wantedTag string) ([]vgInfo, error) {
	vgs := []vgInfo{}
	args := append(vgsArgs, groups...)
	output, err := pmemexec.RunCommand("vgs", args...)
	if err != nil {
		return vgs, fmt.Errorf("vgs failure: %s", err.Error())
	}
	for _, line := range strings.SplitN(output, "\n", len(groups)) {
		fields := strings.Fields(strings.TrimSpace(line))
		if len(fields) != 4 {
			return vgs, fmt.Errorf("Failed to parse vgs output line: %s", line)
		}
		tag := fields[3]
		if tag == wantedTag {
			vg := vgInfo{}
			vg.name = fields[0]
			vg.size, _ = strconv.ParseUint(fields[1], 10, 64)
			vg.free, _ = strconv.ParseUint(fields[2], 10, 64)
			vg.tag = tag
			vgs = append(vgs, vg)
		}
	}

	return vgs, nil
}
