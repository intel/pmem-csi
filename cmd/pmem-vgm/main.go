package main

import (
	"flag"
	"strings"

	"k8s.io/klog"
	"k8s.io/klog/glog"

	"github.com/intel/pmem-csi/pkg/ndctl"
	pmemexec "github.com/intel/pmem-csi/pkg/pmem-exec"
)

func main() {
	klog.InitFlags(nil)
	flag.Set("logtostderr", "true")
	flag.Parse()
	ctx, err := ndctl.NewContext()
	if err != nil {
		klog.Fatalf("Failed to initialize pmem context: %s", err.Error())
	}

	prepareVolumeGroups(ctx)
}

// Prepare volume groups, one per Namespace mode
func prepareVolumeGroups(ctx *ndctl.Context) {
	nsmodes := []ndctl.NamespaceMode{ndctl.FsdaxMode, ndctl.SectorMode}
	for _, nsmod := range nsmodes {
		vgName := vgName(nsmod)
		if err := createVolumeGroup(ctx, vgName, nsmod); err != nil {
			glog.Errorf("Failed volumegroup creation: %s", err.Error())
		}
	}
}

func vgName(nsmode ndctl.NamespaceMode) string {
	return "pmemcsi" + string(nsmode)
}

// - For all namespaces in all buses and all regions:
//   - check that PVol exists provided by that namespace and belongs to correct VG.
//   - Create Volume group if does not exist
func createVolumeGroup(ctx *ndctl.Context, vgName string, nsmode ndctl.NamespaceMode) error {
	cmd := ""
	cmdArgs := []string{"--force", vgName}
	for _, bus := range ctx.GetBuses() {
		glog.Infof("createVolumeGroup: Bus: %s", bus.DeviceName())
		for _, r := range bus.ActiveRegions() {
			glog.Infof("createVolumeGroup: Region: %s", r.DeviceName())
			nsArray := r.ActiveNamespaces()
			for _, ns := range nsArray {
				// consider only namespaces in asked namespacemode,
				// and having name given by this driver, to exclude foreign ones
				if ns.Mode() == ndctl.NamespaceMode(nsmode) && ns.Name() == "pmem-csi" {
					devName := "/dev/" + ns.BlockDeviceName()
					glog.Infof("createVolumeGroup: %s has nsmode %s", ns.BlockDeviceName(), nsmode)
					// check if this pv is already part of a group.
					// if yes ignore this pv, if not add to arg list
					output, err := pmemexec.RunCommand("pvs", "--noheadings", "-o", "vg_name", devName)
					if err != nil || len(strings.TrimSpace(output)) == 0 {
						cmdArgs = append(cmdArgs, devName)
					}
				}
			}
		}
	}
	if len(cmdArgs) == 2 {
		glog.Infof("createVolumeGroup: no new namespace found to add to this group: %s", vgName)
		return nil
	}
	if _, err := pmemexec.RunCommand("vgdisplay", vgName); err != nil {
		glog.Infof("createVolumeGroup: No Vgroup with name %v, mark for creation", vgName)
		cmd = "vgcreate"
	} else {
		glog.Infof("createVolumeGroup: VolGroup '%v' exists", vgName)
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
