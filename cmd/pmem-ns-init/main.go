package main

import (
	"flag"
	"fmt"
	"os"

	"github.com/golang/glog"
	"github.com/intel/csi-pmem/pkg/ndctl"
)

const (
	// TODO: try to get rid of hard-coded overhead
	// overhead likely comes from alignment (default 2M) and mapping_mode="dev"
	namespaceOverhead = 4 * 1024 * 1024
	// smaller namespace size (in GB) for devel mode in VM
	namespacesizeVM = 2
)

var (
	/* generic options */
	//TODO: reading name configuration not yet supported
	//configFile    = flag.String("configfile", "/etc/csi-pmem/config", "PMEM CSI driver namespace configuration file")
	namespacesize = flag.Int("namespacesize", 32, "NVDIMM namespace size in GB")
	useforfsdax   = flag.Int("useforfsdax", 100, "Percentage of total to use in Fsdax mode")
	useforsector  = flag.Int("useforsector", 0, "Percentage of total to use in Sector mode")
)

func init() {
	flag.Set("logtostderr", "true")
}

func main() {
	flag.Parse()
	ctx, err := ndctl.NewContext()
	if err != nil {
		fmt.Printf("Failed to initialize pmem context: %s", err.Error())
		os.Exit(1)
	}

	initNVdimms(ctx, *namespacesize, *useforfsdax, *useforsector)
}

func initNVdimms(ctx *ndctl.Context, namespacesize int, useforfsdax int, useforsector int) {
	// check is there physical NVDIMM(s) present. What happens if we run this without NVDIMM:
	// verified on a VM without NVDIMMs:
	// loop attempt in CreateNamespaces over buses-regions-namespaces makes zero loops,
	// and CheckVG() creates zero PVs, then driver starts running,
	// but ops will fail as there is no regions and PVs, so it's safe.
	// TODO: Should we detect device(s) explicitly here?

	glog.Infof("Configured namespacesize; %v GB", namespacesize)
	createNamespaces(ctx, namespacesize, useforfsdax, useforsector)
	/* for debug
	nss := ctx.GetActiveNamespaces()
	glog.Info("elems in Namespaces:", len(nss))
	for _, ns := range nss {
		glog.Info("Namespace Name:", ns.Name())
		glog.Info("    Size:", ns.Size())
		glog.Info("    Device:", ns.DeviceName())
		glog.Info("    Mode:", ns.Mode())
		glog.Info("    BlockDevice:", ns.BlockDeviceName())
	}*/
}

// Try to create more namespaces
// for all regions:
// - Check available size, if bigger than one NS size, create one more, repeat this in loop
func createNamespaces(ctx *ndctl.Context, namespacesize int, useforfsdax int, useforsector int) {
	// TODO: rethink is it good idea to reset to sane values or is it cleaner to fail
	if useforfsdax < 0 || useforfsdax > 100 {
		glog.Infof("useforfsdax limit should be 0..100 (seeing %v), resetting to default=100", useforfsdax)
		useforfsdax = 100
	}
	if useforsector < 0 || useforsector > 100 {
		glog.Infof("useforsector limit should be 0..100 (seeing %v), resetting to default=0", useforsector)
		useforsector = 0
	}
	if useforfsdax+useforsector > 100 {
		glog.Infof("useforfsdax and useforsector combined can not exceed 100, resetting to defaults 100,0")
		useforfsdax = 100
		useforsector = 0
	}
	nsSize := (uint64(namespacesize) * 1024 * 1024 * 1024)
	for _, bus := range ctx.GetBuses() {
		for _, r := range bus.ActiveRegions() {
			createNS(r, nsSize, useforfsdax, "fsdax")
			createNS(r, nsSize, useforsector, "sector")
		}
	}
}

func createNS(r *ndctl.Region, nsSize uint64, uselimit int, nsmode ndctl.NamespaceMode) {
	// uselimit is the percentage we can use
	canUse := uint64(uselimit) * r.Size() / 100
	glog.Infof("Create %s-namespaces in %v, allowed %d %%:\ntotal       : %16d\navail       : %16d\ncan use     : %16d",
		nsmode, r.DeviceName(), uselimit, r.Size(), r.AvailableSize(), canUse)
	// Find sum of sizes of existing active namespaces with currently handled mode
	var used uint64 = 0
	for _, ns := range r.ActiveNamespaces() {
		glog.Infof("createNS: Existing namespace: Size %16d Mode: %v Device:%v", ns.Size(), ns.Mode(), ns.DeviceName())
		if ns.Mode() == nsmode {
			used += ns.Size() + namespaceOverhead
		}
	}
	glog.Infof("total used: %v", used)
	if used < canUse {
		actuallyAvailable := canUse - used
		// because of NS sizes overhead, r.Available() is less then actuallyAvailable on an empty media
		if r.AvailableSize() < actuallyAvailable {
			actuallyAvailable = r.AvailableSize()
		}
		nPossibleNS := int(actuallyAvailable / (nsSize + namespaceOverhead))
		glog.Infof("%d %s-namespaces of size %v possible in region %s",
			nPossibleNS, nsmode, nsSize, r.DeviceName())
		for i := 0; i < nPossibleNS; i++ {
			glog.Infof("Creating namespace %d", i)
			_, err := r.CreateNamespace(ndctl.CreateNamespaceOpts{
				Mode: nsmode,
				Size: nsSize,
				// TODO: setting mapping location to "mem" avoids use (and problems in qemu env) of pfn
				// but it also will force namespace to "raw" mode for some (still unknown) reason
				//Location: "mem",
				// An error was about not-given SectorSize, but when I give SectorSize specified:
				//SectorSize: 2048,
				// same forcing to raw happens without error reported
			})
			if err != nil {
				glog.Warning("Failed to create namespace:", err.Error())
				/* ??? something went wrong, leave this region ??? */
				break
			}
		}
	}
}
