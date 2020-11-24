/*
Copyright 2020 The Kubernetes Authors.

SPDX-License-Identifier: Apache-2.0
*/

package pmemoperator

import (
	"context"
	"flag"
	"fmt"
	"runtime"

	"k8s.io/client-go/kubernetes"
	"k8s.io/klog/v2"

	"github.com/intel/pmem-csi/pkg/apis"
	api "github.com/intel/pmem-csi/pkg/apis/pmemcsi/v1beta1"
	"github.com/intel/pmem-csi/pkg/k8sutil"
	pmemcommon "github.com/intel/pmem-csi/pkg/pmem-common"
	"github.com/intel/pmem-csi/pkg/pmem-csi-operator/controller"

	"github.com/operator-framework/operator-lib/leader"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	apiruntime "k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client/config"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"sigs.k8s.io/controller-runtime/pkg/manager/signals"

	// import deployment to ensure that the deployment reconciler get initialized.
	_ "github.com/intel/pmem-csi/pkg/pmem-csi-operator/controller/deployment"
)

func printVersion() {
	//klog.Info(fmt.Sprintf("Operator Version: %s", version.Version))
	klog.Info(fmt.Sprintf("Go Version: %s", runtime.Version()))
	klog.Info(fmt.Sprintf("Go OS/Arch: %s/%s", runtime.GOOS, runtime.GOARCH))
}

var driverImage string
var startWebhook bool

const (
	// Default values used by OLM CA for webhook
	WebhookPort     = 9443
	WebhookCertDir  = "/apiserver.local.config/certificates"
	WebhookCertName = "apiserver.crt"
	WebhookKeyName  = "apiserver.key"
)

func init() {
	klog.InitFlags(nil)
	flag.StringVar(&driverImage, "image", "", "docker container image used for deploying the operator.")
	flag.BoolVar(&startWebhook, "webhook", false, "run conversion webhook server")

	flag.Set("logtostderr", "true")
}

func Main() int {
	flag.Parse()

	printVersion()

	// Get a config to talk to the apiserver
	cfg, err := config.GetConfig()
	if err != nil {
		pmemcommon.ExitError("Failed to get configuration: ", err)
		return 1
	}

	ctx := context.Background()
	// Become the leader before proceeding
	err = leader.Become(ctx, "pmem-csi-operator-lock")
	if err != nil {
		pmemcommon.ExitError("Failed to become leader: ", err)
		return 1
	}

	// Retrieve namespace to watch for new deployments and to create sub-resources
	namespace := k8sutil.GetNamespace()

	// Create a new Cmd to provide shared dependencies and start components
	mgr, err := manager.New(cfg, manager.Options{
		Namespace: namespace,
	})
	if err != nil {
		pmemcommon.ExitError("Failed to create controller manager: ", err)
		return 1
	}

	ver, err := k8sutil.GetKubernetesVersion(mgr.GetConfig())
	if err != nil {
		pmemcommon.ExitError("Failed retrieve kubernetes version: ", err)
		return 1
	}
	klog.Info("Kubernetes Version: ", ver)

	klog.Info("Registering Components.")

	// Setup Scheme for all resources
	if err := apis.AddToScheme(mgr.GetScheme()); err != nil {
		pmemcommon.ExitError("Failed to add API schema: ", err)
		return 1
	}

	cs, err := kubernetes.NewForConfig(mgr.GetConfig())
	if err != nil {
		pmemcommon.ExitError("failed to get in-cluster client: %v", err)
		return 1
	}
	// Setup all Controllers
	if err := controller.AddToManager(ctx, mgr, controller.ControllerOptions{
		Config:       mgr.GetConfig(),
		Namespace:    namespace,
		K8sVersion:   *ver,
		DriverImage:  driverImage,
		EventsClient: cs.CoreV1().Events(""),
	}); err != nil {
		pmemcommon.ExitError("Failed to add controller to manager: ", err)
		return 1
	}

	// Setup conversion webhooks

	if startWebhook {
		if err := setupWebhookWithManager(mgr, &api.Deployment{
			ObjectMeta: metav1.ObjectMeta{Name: "pmem-csi-operator"},
		}); err != nil {
			pmemcommon.ExitError("Failed to create webhook(v1alpha1)", err)
			return 1
		}
	}

	klog.Info("Starting the Cmd.")

	// Start the Cmd
	if err := mgr.Start(signals.SetupSignalHandler()); err != nil {
		pmemcommon.ExitError("Manager exited non-zero: ", err)
		return 1
	}

	list := &api.DeploymentList{}
	if err := mgr.GetClient().List(ctx, list); err != nil {
		pmemcommon.ExitError("failed to get deployment list: %v", err)
		return 1
	}

	activeList := []string{}
	for _, d := range list.Items {
		if d.DeletionTimestamp == nil {
			activeList = append(activeList, d.Name)
		}
	}

	if len(activeList) != 0 {
		klog.Infof("There are active PMEM-CSI deployments (%v), hence not deleting the CRD.", activeList)
		return 0
	}

	return 0
}

func setupWebhookWithManager(mgr ctrl.Manager, apiType apiruntime.Object) error {
	klog.Infof("Setting up webhook...")
	bldr := ctrl.NewWebhookManagedBy(mgr).For(apiType)

	// Specify OLM CA Info for webhook
	srv := mgr.GetWebhookServer()
	srv.CertDir = WebhookCertDir
	srv.CertName = WebhookCertName
	srv.KeyName = WebhookKeyName
	srv.Port = WebhookPort

	return bldr.Complete()

}
