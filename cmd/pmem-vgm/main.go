package main

import (
	"flag"
	"os/exec"

	"github.com/golang/glog"
	"github.com/intel/pmem-csi/pkg/ndctl"
)

func init() {
	flag.Set("logtostderr", "true")
}

func main() {
	flag.Parse()
	ctx, err := ndctl.NewContext()
	if err != nil {
		glog.Fatalf("Failed to initialize pmem context: %s", err.Error())
	}

	prepareVolumeGroups(ctx)
}

// TODO: could combine loops in above and below function, as loops traverse
// same buses, regions, namespaces. But readability,clarity will suffer, so I prefer not to combine right now

// for all regions:
// - Check that VG exists for this region. Create if does not exist
// - For all namespaces in region:
//   - check that PVol exists provided by that namespace, is in current VG, add if does not exist
// Edge cases are when no PVol or VG structures (or partially) dont exist yet
func prepareVolumeGroups(ctx *ndctl.Context) {
	for _, bus := range ctx.GetBuses() {
		glog.Infof("CheckVG: Bus: %v", bus.DeviceName())
		for _, r := range bus.ActiveRegions() {
			glog.Infof("Region: %v", r.DeviceName())
			vgName := vgName(bus, r)
			if err := createVolumesForRegion(r, vgName); err != nil {
				glog.Errorf("Failed volumegroup creation: %s", err.Error())
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
		glog.Infof("No active namespaces in region %s", r.DeviceName())
		return nil
	}
	for _, ns := range nsArray {
		devName := "/dev/" + ns.BlockDeviceName()
		/* check if this pv is already part of a group, if yes ignore this pv
		   if not add to arg list */
		output, err := exec.Command("pvdisplay", devName).CombinedOutput()
		if err != nil {
			cmdArgs = append(cmdArgs, devName)
		}
		glog.Infof("pvdisplay output: %s", string(output[:]))
	}
	if len(cmdArgs) == 2 {
		glog.Infof("no new namespace found to add to this group: %s", vgName)
		return nil
	}
	if _, err := exec.Command("vgdisplay", vgName).CombinedOutput(); err != nil {
		glog.Infof("No Vgroup with name %v, mark for creation", vgName)
		cmd = "vgcreate"
	} else {
		glog.Infof("VolGroup '%v' exists", vgName)
		cmd = "vgextend"
	}

	glog.Infof("Running: %s %v", cmd, append([]string{cmd}, cmdArgs...))

	output, err := exec.Command(cmd, cmdArgs...).CombinedOutput() //nolint gosec
	if err != nil {
		glog.Warningf(cmd, " failed with output: ", string(output[:]))
	} else {
		glog.Infof("%s output: %s", cmd, string(output[:]))
	}

	return err
}
