package pmemvgm

import (
	"flag"
	"fmt"
	"strings"

	"k8s.io/klog"
	"k8s.io/klog/glog"

	"github.com/intel/pmem-csi/pkg/ndctl"
	"github.com/intel/pmem-csi/pkg/pmem-common"
	pmemexec "github.com/intel/pmem-csi/pkg/pmem-exec"
)

var (
	showVersion = flag.Bool("version", false, "Show release version and exit")

	version = "unknown"
)

func init() {
	klog.InitFlags(nil)
	flag.Set("logtostderr", "true")
	flag.Parse()
}

func Main() int {
	if *showVersion {
		fmt.Println(version)
		return 0
	}

	glog.V(3).Info("Version: ", version)

	ctx, err := ndctl.NewContext()
	if err != nil {
		pmemcommon.ExitError("failed to initialize pmem context", err)
		return 1
	}

	prepareVolumeGroups(ctx)

	return 0
}

// for all regions:
// - Check that VG exists for this region. Create if does not exist
// - For all namespaces in region:
//   - check that PVol exists provided by that namespace, is in current VG, add if does not exist
// Edge cases are when no PVol or VG structures (or partially) dont exist yet
func prepareVolumeGroups(ctx *ndctl.Context) {
	for _, bus := range ctx.GetBuses() {
		glog.V(5).Infof("CheckVG: Bus: %v", bus.DeviceName())
		for _, r := range bus.ActiveRegions() {
			glog.V(5).Infof("Region: %v", r.DeviceName())
			nsmodes := []ndctl.NamespaceMode{ndctl.FsdaxMode, ndctl.SectorMode}
			for _, nsmod := range nsmodes {
				glog.V(5).Infof("NsMode: %v", nsmod)
				vgName := vgName(bus, r, nsmod)
				if err := createVolumesForRegion(r, vgName, nsmod); err != nil {
					glog.Errorf("Failed volumegroup creation: %s", err.Error())
				}
			}
		}
	}
}

func vgName(bus *ndctl.Bus, region *ndctl.Region, nsmode ndctl.NamespaceMode) string {
	return bus.DeviceName() + region.DeviceName() + string(nsmode)
}

func createVolumesForRegion(r *ndctl.Region, vgName string, nsmode ndctl.NamespaceMode) error {
	cmd := ""
	cmdArgs := []string{"--force", vgName}
	nsArray := r.ActiveNamespaces()
	if len(nsArray) == 0 {
		glog.V(3).Infof("No active namespaces in region %s", r.DeviceName())
		return nil
	}
	for _, ns := range nsArray {
		// consider only namespaces in asked namespacemode,
		// and having name given by this driver, to exclude foreign ones
		if ns.Mode() == ndctl.NamespaceMode(nsmode) && ns.Name() == "pmem-csi" {
			devName := "/dev/" + ns.BlockDeviceName()
			glog.V(4).Infof("createVolumesForRegion: %s has nsmode %s", ns.BlockDeviceName(), nsmode)
			/* check if this pv is already part of a group, if yes ignore this pv
			if not add to arg list */
			output, err := pmemexec.RunCommand("pvs", "--noheadings", "-o", "vg_name", devName)
			if err != nil || len(strings.TrimSpace(output)) == 0 {
				cmdArgs = append(cmdArgs, devName)
			}
		}
	}
	if len(cmdArgs) == 2 {
		glog.V(3).Infof("no new namespace found to add to this group: %s", vgName)
		return nil
	}
	if _, err := pmemexec.RunCommand("vgdisplay", vgName); err != nil {
		glog.V(3).Infof("No Vgroup with name %v, mark for creation", vgName)
		cmd = "vgcreate"
	} else {
		glog.V(3).Infof("VolGroup '%v' exists", vgName)
		cmd = "vgextend"
	}

	_, err := pmemexec.RunCommand(cmd, cmdArgs...) //nolint gosec
	if err != nil {
		return err
	}
	// Tag add works without error if repeated, so it is safe to run without checking for existing
	_, err = pmemexec.RunCommand("vgchange", "--addtag", string(nsmode), vgName)
	return err
}
