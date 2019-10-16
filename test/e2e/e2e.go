/*
Copyright 2015 The Kubernetes Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package e2e

import (
	"context"
	"fmt"
	"os"
	"path"
	"strings"
	"testing"
	"time"

	"github.com/onsi/ginkgo"
	"github.com/onsi/ginkgo/config"
	"github.com/onsi/ginkgo/reporters"
	"github.com/onsi/gomega"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	clientset "k8s.io/client-go/kubernetes"
	"k8s.io/kubernetes/pkg/version"
	"k8s.io/kubernetes/test/e2e/framework"
	"k8s.io/kubernetes/test/e2e/framework/ginkgowrapper"
	e2epod "k8s.io/kubernetes/test/e2e/framework/pod"
	"k8s.io/kubernetes/test/e2e/framework/podlogs"
)

// There are certain operations we only want to run once per overall test invocation
// (such as deleting old namespaces, or verifying that all system pods are running.
// Because of the way Ginkgo runs tests in parallel, we must use SynchronizedBeforeSuite
// to ensure that these operations only run on the first parallel Ginkgo node.
//
// This function takes two parameters: one function which runs on only the first Ginkgo node,
// returning an opaque byte array, and then a second function which runs on all Ginkgo nodes,
// accepting the byte array.
var _ = ginkgo.SynchronizedBeforeSuite(func() []byte {
	// Run only on Ginkgo node 1
	var data []byte

	framework.Logf("checking config")

	c, err := framework.LoadClientset()
	if err != nil {
		framework.Failf("Error loading client: %v", err)
	}

	if framework.TestContext.ReportDir != "" {
		if err := os.MkdirAll(framework.TestContext.ReportDir, 0755); err != nil {
			framework.Failf("Failed creating report directory: %v", err)
		}
	}

	// Delete any namespaces except those created by the system. This ensures no
	// lingering resources are left over from a previous test run.
	if framework.TestContext.CleanStart {
		deleted, err := framework.DeleteNamespaces(c, nil, /* deleteFilter */
			[]string{
				metav1.NamespaceSystem,
				metav1.NamespaceDefault,
				metav1.NamespacePublic,
			})
		if err != nil {
			framework.Failf("Error deleting orphaned namespaces: %v", err)
		}
		framework.Logf("Waiting for deletion of the following namespaces: %v", deleted)
		if err := framework.WaitForNamespacesDeleted(c, deleted, framework.NamespaceCleanupTimeout); err != nil {
			framework.Failf("Failed to delete orphaned namespaces %v: %v", deleted, err)
		}
	}

	// In large clusters we may get to this point but still have a bunch
	// of nodes without Routes created. Since this would make a node
	// unschedulable, we need to wait until all of them are schedulable.
	framework.ExpectNoError(framework.WaitForAllNodesSchedulable(c, framework.TestContext.NodeSchedulableTimeout))

	// Ensure all pods are running and ready before starting tests (otherwise,
	// cluster infrastructure pods that are being pulled or started can block
	// test pods from running, and tests that ensure all pods are running and
	// ready will fail).
	podStartupTimeout := framework.TestContext.SystemPodsStartupTimeout
	// TODO: In large clusters, we often observe a non-starting pods due to
	// #41007. To avoid those pods preventing the whole test runs (and just
	// wasting the whole run), we allow for some not-ready pods (with the
	// number equal to the number of allowed not-ready nodes).
	if err := e2epod.WaitForPodsRunningReady(c, metav1.NamespaceSystem, int32(framework.TestContext.MinStartupPods), int32(framework.TestContext.AllowedNotReadyNodes), podStartupTimeout, map[string]string{}); err != nil {
		framework.DumpAllNamespaceInfo(c, metav1.NamespaceSystem)
		framework.LogFailedContainers(c, metav1.NamespaceSystem, framework.Logf)
		framework.Failf("Error waiting for all pods to be running and ready: %v", err)
	}

	if err := framework.WaitForDaemonSets(c, metav1.NamespaceSystem, int32(framework.TestContext.AllowedNotReadyNodes), framework.TestContext.SystemDaemonsetStartupTimeout); err != nil {
		framework.Logf("WARNING: Waiting for all daemonsets to be ready failed: %v", err)
	}

	// Log the version of the server and this client.
	framework.Logf("e2e test version: %s", version.Get().GitVersion)

	// PMEM-CSI needs to be installed already in the cluster, running in the default
	// namespace. We want to see what the pods are doing while the test runs. This
	// isn't perfect because we will get a dump also of log output from before the
	// E2E test run, but better than nothing.
	ctx := context.Background()
	to := podlogs.LogOutput{
		StatusWriter: ginkgo.GinkgoWriter,
		LogWriter:    ginkgo.GinkgoWriter,
	}
	podlogs.CopyAllLogs(ctx, c, "default", to)
	podlogs.WatchPods(ctx, c, "default", ginkgo.GinkgoWriter)

	WaitForPMEMDriver(c)

	dc := c.DiscoveryClient

	serverVersion, serverErr := dc.ServerVersion()
	if serverErr != nil {
		framework.Logf("Unexpected server error retrieving version: %v", serverErr)
	}
	if serverVersion != nil {
		framework.Logf("kube-apiserver version: %s", serverVersion.GitVersion)
	}

	// Reference common test to make the import valid.
	// commontest.CurrentSuite = commontest.E2E

	return data

}, func(data []byte) {
	// Run on all Ginkgo nodes
})

// Similar to SynchornizedBeforeSuite, we want to run some operations only once (such as collecting cluster logs).
// Here, the order of functions is reversed; first, the function which runs everywhere,
// and then the function that only runs on the first Ginkgo node.
var _ = ginkgo.SynchronizedAfterSuite(func() {
	// Run on all Ginkgo nodes
	framework.Logf("Running AfterSuite actions on all node")
	framework.RunCleanupActions()
}, func() {
	// Run only Ginkgo on node 1
})

// RunE2ETests checks configuration parameters (specified through flags) and then runs
// E2E tests using the Ginkgo runner.
// This function is called on each Ginkgo node in parallel mode.
func RunE2ETests(t *testing.T) {
	gomega.RegisterFailHandler(ginkgowrapper.Fail)

	// Run tests through the Ginkgo runner with output to console + JUnit for Jenkins
	var r []ginkgo.Reporter
	if framework.TestContext.ReportDir != "" {
		r = append(r, reporters.NewJUnitReporter(path.Join(framework.TestContext.ReportDir, fmt.Sprintf("junit_%v%02d.xml", framework.TestContext.ReportPrefix, config.GinkgoConfig.ParallelNode))))
	}
	ginkgo.RunSpecsWithDefaultAndCustomReporters(t, "PMEM E2E suite", r)
}

// WaitForPMEMDriver ensures that the PMEM-CSI driver is ready for use.
//
// A relatively simple check is to check the nodes: if all nodes with
// "storage: pmem" also have a PMEM-CSI entry in the
// "csi.volume.kubernetes.io/nodeid" annotation, then kubelet has found
// the driver, which in turn implies that the node has been registered
// with the PMEM-CSI controller, because the node driver only makes
// its csi.sock available after that.
//
// There's just one loophole: the annotation might be from a previous
// successful registration of the driver. For a fresh cluster as in the CI
// the check should be good enough.
func WaitForPMEMDriver(c clientset.Interface) {
	ticker := time.NewTicker(10 * time.Second)
	defer ticker.Stop()
	deadline, cancel := context.WithTimeout(context.Background(), framework.TestContext.SystemDaemonsetStartupTimeout)
	defer cancel()

	ready := func() bool {
		nodes, err := c.CoreV1().Nodes().List(metav1.ListOptions{})
		if err != nil {
			framework.Failf("get nodes: %s", err)
		}
		var notReady []string
		for _, node := range nodes.Items {
			if node.Labels["storage"] == "pmem" &&
				// Strictly speaking we would have to parse the annotation value (a JSON map),
				// but a substring search should be good enough.
				!strings.Contains(node.Annotations["csi.volume.kubernetes.io/nodeid"], `"pmem-csi.intel.com"`) {
				notReady = append(notReady, node.Name)
			}
		}
		if notReady == nil {
			// All ready.
			return true
		}
		framework.Logf("WARNING: PMEM-CSI not ready yet on some nodes: %s", strings.Join(notReady, ", "))
		return false
	}

	if ready() {
		return
	}
	for {
		select {
		case <-ticker.C:
			if ready() {
				return
			}
		case <-deadline.Done():
			framework.Failf("giving up waiting for PMEM-CSI to start up, check the previous warnings and log output")
		}
	}
}
