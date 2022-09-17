/*
Copyright 2019 Intel Corporation.

SPDX-License-Identifier: Apache-2.0
*/

package storage

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"time"

	v1 "k8s.io/api/core/v1"
	storagev1 "k8s.io/api/storage/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	k8stypes "k8s.io/apimachinery/pkg/types"
	"k8s.io/kubernetes/test/e2e/framework"
	storageframework "k8s.io/kubernetes/test/e2e/storage/framework"
	admissionapi "k8s.io/pod-security-admission/api"
	runtime "sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/intel/pmem-csi/pkg/k8sutil"
	"github.com/intel/pmem-csi/test/e2e/deploy"
	"github.com/intel/pmem-csi/test/e2e/driver"
	"github.com/intel/pmem-csi/test/e2e/operator/validate"
	"github.com/intel/pmem-csi/test/e2e/storage/scheduler"
	"github.com/intel/pmem-csi/test/e2e/versionskew"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/onsi/gomega/types"
)

var _ = deploy.DescribeForAll("Deployment", func(d *deploy.Deployment) {
	csiTestDriver := driver.New(d.Name(), d.DriverName, nil, nil, nil)

	// List of testSuites to be added below.
	var csiTestSuites = []func() storageframework.TestSuite{
		versionskew.InitSkewTestSuite,
	}

	if d.HasController {
		// Scheduler tests depend on the webhooks in the controller.
		csiTestSuites = append(csiTestSuites, scheduler.InitSchedulerTestSuite)
	}

	storageframework.DefineTestSuites(csiTestDriver, csiTestSuites)
})

var _ = deploy.DescribeForAll("Deployment", func(d *deploy.Deployment) {
	f := framework.NewDefaultFramework("pmem-csi")

	// Several pods needs privileges.
	f.NamespacePodSecurityEnforceLevel = admissionapi.LevelPrivileged

	DefineLateBindingTests(d, f)
	DefineImmediateBindingTests(d, f)
	DefineKataTests(d)

	It("works", func() {
		// If we get here, the deployment is up and running.

		if d.HasOperator {
			// Additional checks when the operator was used for deployment.
			c, err := deploy.NewCluster(f.ClientSet, f.DynamicClient, f.ClientConfig())
			framework.ExpectNoError(err, "create cluster object")

			// We need the actual CR from the apiserver to check ownership.
			deployment := deploy.GetDeploymentCR(f, d.DriverName)

			ctx, cancel := context.WithTimeout(context.Background(), 1*time.Minute)
			defer cancel()

			client, err := runtime.New(f.ClientConfig(), runtime.Options{})
			framework.ExpectNoError(err, "new operator runtime client")

			k8sver, err := k8sutil.GetKubernetesVersion(f.ClientConfig())
			framework.ExpectNoError(err, "get Kubernetes version")

			metricsURL, err := deploy.GetOperatorMetricsURL(ctx, c, d)
			Expect(err).ShouldNot(HaveOccurred(), "get operator metrics URL")

			_, err = validate.DriverDeploymentEventually(ctx, c, client, *k8sver, metricsURL, d.Namespace, deployment, 0)
			framework.ExpectNoError(err, "validate driver")
		}
	})

	// This checks that cluster-driver-registrar added the
	// CSIDriverInfo for pmem-csi at some point in the past. A
	// full test must include resetting the cluster and installing
	// pmem-csi.
	It("has CSIDriverInfo", func() {
		_, err := f.ClientSet.StorageV1().CSIDrivers().Get(context.Background(), d.DriverName, metav1.GetOptions{})
		Expect(err).NotTo(HaveOccurred(), "get csidriver.storage.k8s.io for pmem-csi failed")
	})

	// This checks that temporarily running the driver pod on the master node
	// creates an entry in CSINode and removes it again.
	It("has CSINode", func() {
		ctx := context.Background()

		masterNode, err := findMasterNode(ctx, f.ClientSet)
		framework.ExpectNoError(err)

		addLabel := `{"metadata":{"labels":{"feature.node.kubernetes.io/memory-nv.dax": "true"}}}`
		removeLabel := `{"metadata":{"labels":{"feature.node.kubernetes.io/memory-nv.dax": null}}}`
		patchMasterNode := func(patch string) (*v1.Node, error) {
			return f.ClientSet.CoreV1().Nodes().Patch(ctx, masterNode, k8stypes.MergePatchType, []byte(patch), metav1.PatchOptions{}, "")
		}
		getMasterCSINode := func() (*storagev1.CSINode, error) {
			return f.ClientSet.StorageV1().CSINodes().Get(ctx, masterNode, metav1.GetOptions{})
		}

		// Whatever we do, always remove the label which might
		// have cause the PMEM-CSI driver to run on the master
		// node. None of the other tests expect that.
		defer func() {
			By("reverting labels")
			if _, err := patchMasterNode(removeLabel); err != nil {
				framework.Logf("removing labels failed: %v", err)
			}
			By("destroying namespace again")
			sshcmd := fmt.Sprintf("%s/_work/%s/ssh.0", os.Getenv("REPO_ROOT"), os.Getenv("CLUSTER"))
			cmd := exec.Command(sshcmd, "sudo ndctl destroy-namespace --force all")
			out, err := cmd.CombinedOutput()
			if err != nil {
				framework.Logf("erasing namespaces with %+v failed: %s", cmd, string(out))
			}
		}()

		_, err = patchMasterNode(addLabel)
		framework.ExpectNoError(err, "set label with %q", addLabel)
		Eventually(getMasterCSINode, "5m", "10s").Should(driverRunning{d.DriverName})

		_, err = patchMasterNode(removeLabel)
		framework.ExpectNoError(err, "remove label with %q", removeLabel)
		Eventually(getMasterCSINode, "5m", "10s").ShouldNot(driverRunning{d.DriverName})
	})
})

type driverRunning struct {
	driverName string
}

var _ types.GomegaMatcher = driverRunning{}

func (d driverRunning) Match(actual interface{}) (success bool, err error) {
	csiNode := actual.(*storagev1.CSINode)
	for _, driver := range csiNode.Spec.Drivers {
		if driver.Name == d.driverName {
			return true, nil
		}
	}
	return false, nil
}

func (d driverRunning) FailureMessage(actual interface{}) (message string) {
	csiNode := actual.(*storagev1.CSINode)
	return fmt.Sprintf("driver %s is not in %+v", d.driverName, *csiNode)
}

func (d driverRunning) NegatedFailureMessage(actual interface{}) (message string) {
	csiNode := actual.(*storagev1.CSINode)
	return fmt.Sprintf("driver %s is in %+v", d.driverName, *csiNode)
}
