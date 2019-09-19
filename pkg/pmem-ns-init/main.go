package pmemnsinit

import (
	"flag"
	"fmt"

	"k8s.io/klog"

	"github.com/intel/pmem-csi/pkg/ndctl"
	"github.com/intel/pmem-csi/pkg/pmem-common"
)

var (
	/* generic options */
	//TODO: reading name configuration not yet supported
	//configFile    = flag.String("configfile", "/etc/pmem-csi/config", "PMEM CSI driver namespace configuration file")
	useforfsdax  = flag.Int("useforfsdax", 100, "Percentage of total to use in Fsdax mode")
	useforsector = flag.Int("useforsector", 0, "Percentage of total to use in Sector mode")
	showVersion  = flag.Bool("version", false, "Show release version and exit")

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

	if err := CheckArgs(*useforfsdax, *useforsector); err != nil {
		pmemcommon.ExitError("invalid arguments", err)
		return 1
	}
	ctx, err := ndctl.NewContext()
	if err != nil {
		pmemcommon.ExitError("failed to initialize pmem context", err)
		return 1
	}

	initNVdimms(ctx, *useforfsdax, *useforsector)
	return 0
}

func CheckArgs(useforfsdax int, useforsector int) error {
	if useforfsdax < 0 || useforfsdax > 100 {
		return fmt.Errorf("useforfsdax value must be 0..100")
	}
	if useforsector < 0 || useforsector > 100 {
		return fmt.Errorf("useforsector value must be 0..100")
	}
	if useforfsdax+useforsector > 100 {
		return fmt.Errorf("useforfsdax and useforsector combined must not exceed 100")
	}
	return nil
}

func initNVdimms(ctx *ndctl.Context, useforfsdax int, useforsector int) {
	for _, bus := range ctx.GetBuses() {
		for _, r := range bus.ActiveRegions() {
			createNS(r, useforfsdax, ndctl.FsdaxMode)
			createNS(r, useforsector, ndctl.SectorMode)
		}
	}
}

func createNS(r *ndctl.Region, uselimit int, nsmode ndctl.NamespaceMode) {
	const align uint64 = 1024 * 1024 * 1024
	realalign := align * r.InterleaveWays()
	// uselimit is the percentage we can use
	canUse := uint64(uselimit) * r.Size() / 100
	klog.V(3).Infof("Create %s-namespaces in %v, allowed %d %%, real align %d:\ntotal       : %16d\navail       : %16d\ncan use     : %16d",
		nsmode, r.DeviceName(), uselimit, realalign, r.Size(), r.AvailableSize(), canUse)
	// Subtract sizes of existing active namespaces with currently handled mode and owned by pmem-csi
	for _, ns := range r.ActiveNamespaces() {
		klog.V(5).Infof("createNS: Exists: Size %16d Mode:%v Device:%v Name:%v", ns.Size(), ns.Mode(), ns.DeviceName(), ns.Name())
		if ns.Mode() == nsmode && ns.Name() == "pmem-csi" {
			canUse -= ns.Size()
		}
	}
	klog.V(4).Infof("Calculated canUse:%v, available by Region info:%v", canUse, r.AvailableSize())
	// Because of overhead by alignement and extra space for page mapping, calculated available may show more than actual
	if r.AvailableSize() < canUse {
		klog.V(4).Infof("Available in Region:%v is less than desired size, limit to that", r.AvailableSize())
		canUse = r.AvailableSize()
	}
	// Should not happen often: fragmented space could lead to r.MaxAvailableExtent() being less than r.AvailableSize()
	if r.MaxAvailableExtent() < canUse {
		klog.V(4).Infof("MaxAvailableExtent in Region:%v is less than desired size, limit to that", r.MaxAvailableExtent())
		canUse = r.MaxAvailableExtent()
	}
	// Align down to next real alignment boundary, as trying creation above it may fail.
	canUse /= realalign
	canUse *= realalign
	// If less than 2GB usable, don't attempt as creation would fail
	const minsize uint64 = 2 * 1024 * 1024 * 1024
	if canUse >= minsize {
		klog.V(3).Infof("Create %v-bytes %s-namespace", canUse, nsmode)
		_, err := r.CreateNamespace(ndctl.CreateNamespaceOpts{
			Name:  "pmem-csi",
			Mode:  nsmode,
			Size:  canUse,
			Align: align,
		})
		if err != nil {
			klog.Warning("Failed to create namespace:", err.Error())
		}
	}
}
