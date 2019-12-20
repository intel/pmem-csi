package pmemvgm

import (
	"flag"
	"fmt"
	"strings"

	"k8s.io/klog"

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
}

func Main() int {
	if *showVersion {
		fmt.Println(version)
		return 0
	}

	klog.V(3).Info("Version: ", version)

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
		klog.V(5).Infof("CheckVG: Bus: %v", bus.DeviceName())
		for _, r := range bus.ActiveRegions() {
			klog.V(5).Infof("Region: %v", r.DeviceName())
			vgName := vgName(bus, r)
			if err := createVolumesForRegion(r, vgName); err != nil {
				klog.Errorf("Failed volumegroup creation: %s", err.Error())
			}
		}
	}
}

func vgName(bus *ndctl.Bus, region *ndctl.Region) string {
	return bus.DeviceName() + region.DeviceName()
}

func createVolumesForRegion(r *ndctl.Region, vgName string) error {
	cmd := ""
	cmdArgs := []string{"--force", vgName}
	nsArray := r.ActiveNamespaces()
	if len(nsArray) == 0 {
		klog.V(3).Infof("No active namespaces in region %s", r.DeviceName())
		return nil
	}
	for _, ns := range nsArray {
		// consider only namespaces having name given by this driver, to exclude foreign ones
		if ns.Name() == "pmem-csi" {
			devName := "/dev/" + ns.BlockDeviceName()
			/* check if this pv is already part of a group, if yes ignore this pv
			if not add to arg list */
			output, err := pmemexec.RunCommand("pvs", "--noheadings", "-o", "vg_name", devName)
			if err != nil || len(strings.TrimSpace(output)) == 0 {
				cmdArgs = append(cmdArgs, devName)
			}
		}
	}
	if len(cmdArgs) == 2 {
		klog.V(3).Infof("no new namespace found to add to this group: %s", vgName)
		return nil
	}
	if _, err := pmemexec.RunCommand("vgdisplay", vgName); err != nil {
		klog.V(3).Infof("No Vgroup with name %v, mark for creation", vgName)
		cmd = "vgcreate"
	} else {
		klog.V(3).Infof("VolGroup '%v' exists", vgName)
		cmd = "vgextend"
	}

	_, err := pmemexec.RunCommand(cmd, cmdArgs...) //nolint gosec
	if err != nil {
		return err
	}
	return err
}
