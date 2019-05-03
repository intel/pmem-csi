package pmemnsinit

import (
	"flag"
	"fmt"

	"k8s.io/klog"
	"k8s.io/klog/glog"

	"github.com/intel/pmem-csi/pkg/ndctl"
	"github.com/intel/pmem-csi/pkg/pmem-common"
)

var (
	/* generic options */
	//TODO: reading name configuration not yet supported
	//configFile    = flag.String("configfile", "/etc/pmem-csi/config", "PMEM CSI driver namespace configuration file")
	namespacesize = flag.Int("namespacesize", 32, "Namespace size in GB")
	useforfsdax   = flag.Int("useforfsdax", 100, "Percentage of total to use in Fsdax mode")
	useforsector  = flag.Int("useforsector", 0, "Percentage of total to use in Sector mode")
	showVersion   = flag.Bool("version", false, "Show release version and exit")

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

	if err := CheckArgs(*namespacesize, *useforfsdax, *useforsector); err != nil {
		pmemcommon.ExitError("invalid arguments", err)
		return 1
	}
	ctx, err := ndctl.NewContext()
	if err != nil {
		pmemcommon.ExitError("failed to initialize pmem context", err)
		return 1
	}

	initNVdimms(ctx, *namespacesize, *useforfsdax, *useforsector)
	return 0
}

func CheckArgs(namespacesize int, useforfsdax int, useforsector int) error {
	if useforfsdax < 0 || useforfsdax > 100 {
		return fmt.Errorf("useforfsdax value must be 0..100")
	}
	if useforsector < 0 || useforsector > 100 {
		return fmt.Errorf("useforsector value must be 0..100")
	}
	if useforfsdax+useforsector > 100 {
		return fmt.Errorf("useforfsdax and useforsector combined must not exceed 100")
	}
	if namespacesize < 2 {
		return fmt.Errorf("namespacesize has to be at least 2 GB")
	}
	return nil
}

func initNVdimms(ctx *ndctl.Context, namespacesize int, useforfsdax int, useforsector int) {
	glog.V(4).Infof("Configured namespacesize; %v GB", namespacesize)
	// we get 1GB smaller than we ask so lets ask for 1GB more
	nsSize := uint64(namespacesize+1) * 1024 * 1024 * 1024
	for _, bus := range ctx.GetBuses() {
		for _, r := range bus.ActiveRegions() {
			createNS(r, nsSize, useforfsdax, ndctl.FsdaxMode)
			createNS(r, nsSize, useforsector, ndctl.SectorMode)
		}
	}
}

func createNS(r *ndctl.Region, nsSize uint64, uselimit int, nsmode ndctl.NamespaceMode) {
	// uselimit is the percentage we can use
	canUse := uint64(uselimit) * r.Size() / 100
	glog.V(3).Infof("Create %s-namespaces in %v, allowed %d %%:\ntotal       : %16d\navail       : %16d\ncan use     : %16d",
		nsmode, r.DeviceName(), uselimit, r.Size(), r.AvailableSize(), canUse)
	// Subtract sizes of existing active namespaces with currently handled mode and owned by pmem-csi
	for _, ns := range r.ActiveNamespaces() {
		glog.V(5).Infof("createNS: Exists: Size %16d Mode:%v Device:%v Name:%v", ns.Size(), ns.Mode(), ns.DeviceName(), ns.Name())
		if ns.Mode() == nsmode && ns.Name() == "pmem-csi" {
			canUse -= ns.Size()
		}
	}
	glog.V(4).Infof("%v bytes calculated available after scan for existing %s-mode namespaces", canUse, nsmode)
	// cast to signed, otherwise can never be negative and risks forever-looping,
	// broken only by break stmt below which happens after a failed creation attempt
	for int64(canUse) > 0 {
		if nsSize > canUse {
			// this creates one last smaller namespace in leftover space
			nsSize = canUse
			glog.V(4).Infof("Less than configured namespacesize remaining, change desired size to %v", nsSize)
		}
		glog.V(4).Infof("Calculated canUse:%v, available by Region info:%v", canUse, r.AvailableSize())
		// Because of overhead by alignement and extra space for page mapping, calculated available may show more than actual
		if r.AvailableSize() < nsSize {
			glog.V(4).Infof("Available in Region:%v is less than desired nsSize, limit nsSize to that", r.AvailableSize())
			nsSize = r.AvailableSize()
		}
		// Should not happen easily, fragmented space could lead to r.MaxAvailableExtent() being less than r.AvailableSize()
		if r.MaxAvailableExtent() < nsSize {
			glog.V(4).Infof("MaxAvailableExtent in Region:%v is less than desired nsSize, limit nsSize to that", r.MaxAvailableExtent())
			nsSize = r.MaxAvailableExtent()
		}
		// If NSize drops to less than 2GB, stop as creation would fail
		if nsSize < 2*1024*1024*1024 {
			glog.V(4).Infof("nsSize:%v is less then 2 GB, stop creating", nsSize)
			break
		}
		glog.V(3).Infof("Create next %v-bytes %s-namespace", nsSize, nsmode)
		_, err := r.CreateNamespace(ndctl.CreateNamespaceOpts{
			Name:  "pmem-csi",
			Mode:  nsmode,
			Size:  nsSize,
			Align: 1024 * 1024 * 1024,
		})
		if err != nil {
			glog.Warning("Failed to create namespace:", err.Error())
			/* ??? something went wrong, leave this region ??? */
			break
		}
		canUse -= nsSize
	}
}
