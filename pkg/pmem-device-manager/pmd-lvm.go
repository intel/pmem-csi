package pmdmanager

import (
	"fmt"
	"strconv"
	"strings"
	"sync"

	"github.com/intel/pmem-csi/pkg/ndctl"
	pmemexec "github.com/intel/pmem-csi/pkg/pmem-exec"
	"k8s.io/klog/glog"
)

type pmemLvm struct {
	volumeGroups []string
	devices      map[string]PmemDeviceInfo
}

var _ PmemDeviceManager = &pmemLvm{}
var lvsArgs = []string{"--noheadings", "--nosuffix", "-o", "lv_name,lv_path,lv_size,vg_tags", "--units", "B"}
var vgsArgs = []string{"--noheadings", "--nosuffix", "-o", "vg_name,vg_size,vg_free,vg_tags", "--units", "B"}

// mutex to synchronize all LVM calls
// The reason we chose not to support concurrent LVM calls was
// due to LVM's inconsistent behavior when made concurrent calls
// in our stress tests. One should revisit this and choose better
// suitable synchronization policy.
var lvmMutex = &sync.Mutex{}

// NewPmemDeviceManagerLVM Instantiates a new LVM based pmem device manager
// The pre-requisite for this manager is that all the pmem regions which should be managed by
// this LMV manager are devided into namespaces and grouped as volume groups.
func NewPmemDeviceManagerLVM() (PmemDeviceManager, error) {
	lvmMutex.Lock()
	defer lvmMutex.Unlock()

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

	devices, err := listDevices(volumeGroups...)
	if err != nil {
		return nil, err
	}

	return &pmemLvm{
		volumeGroups: volumeGroups,
		devices:      devices,
	}, nil
}

type vgInfo struct {
	name   string
	size   uint64
	free   uint64
	nsmode string
}

func (lvm *pmemLvm) GetCapacity() (map[string]uint64, error) {
	lvmMutex.Lock()
	defer lvmMutex.Unlock()
	return lvm.getCapacity()
}

// nsmode is expected to be either "fsdax" or "sector"
func (lvm *pmemLvm) CreateDevice(name string, size uint64, nsmode string) error {
	if nsmode != string(ndctl.FsdaxMode) && nsmode != string(ndctl.SectorMode) {
		return fmt.Errorf("Unknown nsmode(%v)", nsmode)
	}
	lvmMutex.Lock()
	defer lvmMutex.Unlock()
	// Check that such name does not exist. In certain error states, for example when
	// namespace creation works but device zeroing fails (missing /dev/pmemX.Y in container),
	// this function is asked to create new devices repeatedly, forcing running out of space.
	// Avoid device filling with garbage entries by returning error.
	// Overall, no point having more than one namespace with same name.
	_, err := lvm.getDevice(name)
	if err == nil {
		return fmt.Errorf("CreateDevice: Failed: namespace with that name '%s' exists", name)
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
	// Asked==zero means unspecified by CSI spec, we create a small 4 Mbyte volume
	// as lvcreate does not allow zero size (csi-sanity creates zero-sized volumes)
	if sizeM <= 0 {
		sizeM = 4
	}
	strSz := strconv.Itoa(sizeM)

	for _, vg := range vgs {
		if vg.free >= size {
			// In some container environments clearing device fails with race condition.
			// So, we ask lvm not to clear(-Zn) the newly created device, instead we do ourself in later stage.
			// lvcreate takes size in MBytes if no unit
			if _, err := pmemexec.RunCommand("lvcreate", "-Zn", "-L", strSz, "-n", name, vg.name); err != nil {
				glog.V(3).Infof("lvcreate failed with error: %v, trying for next free region", err)
			} else {
				// clear start of device to avoid old data being recognized as file system
				device, err := getUncachedDevice(name, vg.name)
				if err != nil {
					return err
				}
				err = WaitDeviceAppears(device)
				if err != nil {
					return err
				}
				err = ClearDevice(device, false)
				if err != nil {
					return err
				}

				lvm.devices[device.Name] = device

				return nil
			}
		}
	}
	return fmt.Errorf("No region is having enough space required(%v)", size)
}

func (lvm *pmemLvm) DeleteDevice(name string, flush bool) error {
	lvmMutex.Lock()
	defer lvmMutex.Unlock()

	device, err := lvm.getDevice(name)
	if err != nil {
		return err
	}
	if err := ClearDevice(device, flush); err != nil {
		return err
	}

	_, err = pmemexec.RunCommand("lvremove", "-fy", device.Path)
	return err
}

func (lvm *pmemLvm) FlushDeviceData(name string) error {
	lvmMutex.Lock()
	defer lvmMutex.Unlock()

	device, err := lvm.getDevice(name)
	if err != nil {
		return err
	}

	return ClearDevice(device, true)
}

func (lvm *pmemLvm) ListDevices() ([]PmemDeviceInfo, error) {
	lvmMutex.Lock()
	defer lvmMutex.Unlock()

	devices := []PmemDeviceInfo{}
	for _, dev := range lvm.devices {
		devices = append(devices, dev)
	}

	return devices, nil
}

func (lvm *pmemLvm) GetDevice(id string) (PmemDeviceInfo, error) {
	lvmMutex.Lock()
	defer lvmMutex.Unlock()

	return lvm.getDevice(id)
}

func (lvm *pmemLvm) getDevice(id string) (PmemDeviceInfo, error) {
	if dev, ok := lvm.devices[id]; ok {
		return dev, nil
	}

	return PmemDeviceInfo{}, fmt.Errorf("Device not found with name %s", id)
}

func getUncachedDevice(id string, volumeGroup string) (PmemDeviceInfo, error) {
	devices, err := listDevices(volumeGroup)
	if err != nil {
		return PmemDeviceInfo{}, err
	}

	if dev, ok := devices[id]; ok {
		return dev, nil
	}
	return PmemDeviceInfo{}, fmt.Errorf("Device not found with name %s", id)
}

// listDevices Lists available logical devices in given volume groups
func listDevices(volumeGroups ...string) (map[string]PmemDeviceInfo, error) {
	args := append(lvsArgs, volumeGroups...)
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
func parseLVSOuput(output string) (map[string]PmemDeviceInfo, error) {
	devices := map[string]PmemDeviceInfo{}
	lines := strings.Split(output, "\n")
	for _, line := range lines {
		fields := strings.Fields(strings.TrimSpace(line))
		if len(fields) != 4 {
			continue
		}

		dev := PmemDeviceInfo{}
		dev.Name = fields[0]
		dev.Path = fields[1]
		dev.Size, _ = strconv.ParseUint(fields[2], 10, 64)
		tags := parseTags(fields[3]) //vg_tags
		dev.Mode = tags["nsmode"]
		if nn, ok := tags["numanode"]; ok {
			n, _ := strconv.ParseInt(nn, 10, 32)
			dev.NumaNode = int(n)
		}

		devices[dev.Name] = dev
	}

	return devices, nil
}

func (lvm *pmemLvm) getCapacity() (map[string]uint64, error) {
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

func getVolumeGroups(groups []string, wantedMode string) ([]vgInfo, error) {
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
		tags := parseTags(fields[3]) //vg_tags

		if mode := tags["nsmode"]; wantedMode != "" && mode == wantedMode {
			vg := vgInfo{}
			vg.name = fields[0]
			vg.size, _ = strconv.ParseUint(fields[1], 10, 64)
			vg.free, _ = strconv.ParseUint(fields[2], 10, 64)
			vg.nsmode = mode
			vgs = append(vgs, vg)
		}
	}

	return vgs, nil
}

// parseTags parse vg_tag string and returns map[string]string
func parseTags(vgTags string) map[string]string {
	tags := map[string]string{}

	entries := strings.Split(vgTags, ",")
	for _, tag := range entries {
		if keyval := strings.SplitN(tag, "=", 2); len(keyval) == 2 {
			tags[keyval[0]] = keyval[1]
		} else {
			// fallback to single tag value
			tags["nsmode"] = keyval[0]
			break
		}
	}

	return tags
}
