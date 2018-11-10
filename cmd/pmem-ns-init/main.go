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
	namespaceOverhead = 4 * 1024 * 1024
	// smaller namespace size (in GB) for devel mode in VM
	namespacesizeVM = 2
)

var (
	/* generic options */
	//TODO: reading name configuration not yet supported
	//configFile    = flag.String("configfile", "/etc/csi-pmem/config", "PMEM CSI driver namespace configuration file")
	namespacesize = flag.Int("namespacesize", 32, "NVDIMM namespace size in GB")
	uselimit      = flag.Int("uselimit", 100, "Limit use of total PMEM amount, used percent")
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

	initNVdimms(ctx, *namespacesize, *uselimit)
}

func initNVdimms(ctx *ndctl.Context, namespacesize, uselimit int) {
	// check is there physical NVDIMM(s) present. What happens if we run this without NVDIMM:
	// verified on a VM without NVDIMMs:
	// loop attempt in CreateNamespaces over buses-regions-namespaces makes zero loops,
	// and CheckVG() creates zero PVs, then driver starts running,
	// but ops will fail as there is no regions and PVs, so it's safe.
	// TODO: Should we detect device(s) explicitly here?

	glog.Infof("Configured namespacesize; %v GB", namespacesize)
	createNamespaces(ctx, namespacesize, uselimit)
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

// Try to make all space in all regions consumed by namespaces
// for all regions:
// - Check available size, if bigger than one NS size, create one more, repeat this in loop
func createNamespaces(ctx *ndctl.Context, namespacesize int, uselimit int) {
	if uselimit < 0 || uselimit > 100 {
		glog.Infof("Use limit should be 0..100 (seeing %v), resetting to 100", uselimit)
		uselimit = 100
	}
	// TODO: add sanity checking of namespacesize (but using what limits?)
	nsSize := (uint64(namespacesize) * 1024 * 1024 * 1024)
	for _, bus := range ctx.GetBuses() {
		for _, r := range bus.ActiveRegions() {
			// uselimit sets the percentage we can use
			leaveUnused := uint64(100-uselimit) * r.Size() / 100
			glog.Infof("CreateNamespaces in %v:\ntotal       : %16d\navail       : %16d\nleave unused: %16d",
				r.DeviceName(), r.Size(), r.AvailableSize(), leaveUnused)
			if r.AvailableSize() > leaveUnused {
				nPossibleNS := int((r.AvailableSize() - leaveUnused) / (nsSize + namespaceOverhead))
				glog.Infof("%v namespaces of size %v possible in region %s",
					nPossibleNS, nsSize, r.DeviceName())
				for i := 0; i < nPossibleNS; i++ {
					glog.Infof("Creating namespace%d", i)
					_, err := r.CreateNamespace(ndctl.CreateNamespaceOpts{
						Size: nsSize,
					})
					if err != nil {
						glog.Warning("Failed to create namespace:", err.Error())
						/* ??? something went wrong, leave this region ??? */
						break
					}
				}
			}
		}
	}
}
