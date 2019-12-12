package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"runtime"

	"k8s.io/klog"

	"github.com/intel/pmem-csi/operator/pkg/apis"
	"github.com/intel/pmem-csi/operator/pkg/controller"
	"github.com/intel/pmem-csi/operator/version"

	"github.com/operator-framework/operator-sdk/pkg/k8sutil"
	"github.com/operator-framework/operator-sdk/pkg/leader"
	"github.com/operator-framework/operator-sdk/pkg/restmapper"
	sdkVersion "github.com/operator-framework/operator-sdk/version"
	"sigs.k8s.io/controller-runtime/pkg/client/config"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"sigs.k8s.io/controller-runtime/pkg/manager/signals"
)

func printVersion() {
	klog.Info(fmt.Sprintf("Operator Version: %s", version.Version))
	klog.Info(fmt.Sprintf("Go Version: %s", runtime.Version()))
	klog.Info(fmt.Sprintf("Go OS/Arch: %s/%s", runtime.GOOS, runtime.GOARCH))
	klog.Info(fmt.Sprintf("Version of operator-sdk: %v", sdkVersion.Version))
}

func init() {
	klog.InitFlags(nil)
	flag.Set("logtostderr", "true")
}

func main() {
	flag.Parse()

	printVersion()

	namespace, err := k8sutil.GetWatchNamespace()
	if err != nil {
		klog.Error("Failed to get watch namespace: ", err)
		os.Exit(1)
	}

	// Get a config to talk to the apiserver
	cfg, err := config.GetConfig()
	if err != nil {
		klog.Error(err)
		os.Exit(1)
	}

	ctx := context.TODO()
	// Become the leader before proceeding
	err = leader.Become(ctx, "pmem-csi-operator-lock")
	if err != nil {
		klog.Error(err)
		os.Exit(1)
	}

	// Create a new Cmd to provide shared dependencies and start components
	mgr, err := manager.New(cfg, manager.Options{
		Namespace:      namespace,
		MapperProvider: restmapper.NewDynamicRESTMapper,
	})
	if err != nil {
		klog.Error(err)
		os.Exit(1)
	}

	klog.Info("Registering Components.")

	// Setup Scheme for all resources
	if err := apis.AddToScheme(mgr.GetScheme()); err != nil {
		klog.Error(err)
		os.Exit(1)
	}

	// Setup all Controllers
	if err := controller.AddToManager(mgr); err != nil {
		klog.Error(err)
		os.Exit(1)
	}

	klog.Info("Starting the Cmd.")

	// Start the Cmd
	if err := mgr.Start(signals.SetupSignalHandler()); err != nil {
		klog.Error("Manager exited non-zero: ", err)
		os.Exit(1)
	}
}
