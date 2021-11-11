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

	v1 "k8s.io/api/core/v1"
	storagev1 "k8s.io/api/storage/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	k8stypes "k8s.io/apimachinery/pkg/types"
	"k8s.io/kubernetes/test/e2e/framework"

	"github.com/intel/pmem-csi/test/e2e/deploy"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	"github.com/onsi/gomega/types"
)

var _ = deploy.DescribeForAll("Deployment", func(d *deploy.Deployment) {
	f := framework.NewDefaultFramework("pmem")

	// This checks that cluster-driver-registrar added the
	// CSIDriverInfo for pmem-csi at some point in the past. A
	// full test must include resetting the cluster and installing
	// pmem-csi.
	It("has CSIDriverInfo", func() {
		_, err := f.ClientSet.StorageV1().CSIDrivers().Get(context.Background(), "pmem-csi.intel.com", metav1.GetOptions{})
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
		Eventually(getMasterCSINode, "2m", "10s").ShouldNot(driverRunning{d.DriverName})
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
