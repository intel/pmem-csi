/*
Copyright 2021 The Kubernetes Authors.

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

package storage

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"strings"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	k8stypes "k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes"
	"k8s.io/kubernetes/test/e2e/framework"
	e2epod "k8s.io/kubernetes/test/e2e/framework/pod"

	api "github.com/intel/pmem-csi/pkg/apis/pmemcsi/v1beta1"
	"github.com/intel/pmem-csi/test/e2e/deploy"
	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
)

// Run the csi-test sanity tests against a PMEM-CSI driver.
var _ = deploy.DescribeForSome("raw-conversion", func(d *deploy.Deployment) bool {
	// This test expects that PMEM-CSI was deployed in LVM mode.
	// We don't need to repeat this test for all deployments, but
	// at least YAML and operator seem useful. By focusing on
	// production deployments, we get that.
	return d.Mode == api.DeviceModeLVM && !d.Testing && d.HasDriver
}, func(d *deploy.Deployment) {
	f := framework.NewDefaultFramework("conversion")

	It("works", func() {
		testRawNamespaceConversion(f, d.DriverName, d.Namespace)
	})
})

func testRawNamespaceConversion(f *framework.Framework, driverName, namespace string) {
	ctx := context.Background()
	var err error
	var out []byte

	masterNode, err := findMasterNode(ctx, f.ClientSet)
	framework.ExpectNoError(err)
	sshcmd := fmt.Sprintf("%s/_work/%s/ssh.0", os.Getenv("REPO_ROOT"), os.Getenv("CLUSTER"))
	_, err = os.Stat(sshcmd)
	framework.ExpectNoError(err, "SSH command for master node")

	// Verify that the node is empty. We don't want to destroy
	// something that might exist.
	out, err = exec.Command(sshcmd, "sudo ndctl list -NR").CombinedOutput()
	framework.ExpectNoError(err, "ndctl list: %q", string(out))
	parsed, err := deploy.ParseNdctlOutput(out)
	framework.ExpectNoError(err, "parse ndctl list output %q", string(out))
	Expect(parsed.Regions).NotTo(BeEmpty(), "should have PMEM regions")
	for _, region := range parsed.Regions {
		Expect(region.Namespaces).To(BeEmpty(), "should have no namespaces")
	}

	// Whatever we do, always delete all namespaces and remove
	// labels which might have cause the PMEM-CSI driver to run
	// on the master node. None of the other tests expect that.
	defer func() {
		By("destroying volume groups again")
		vgs := exec.Command(sshcmd, "sudo vgs --noheadings --options name")
		out, err := vgs.Output() // ignore stderr, it may contain warnings
		if err != nil {
			framework.Logf("listing volume groups with %+v failed: %s", vgs, string(out))
		}
		for _, vg := range strings.Split(string(out), "\n") {
			vgremove := exec.Command(sshcmd, "sudo vgremove "+vg)
			out, err := vgremove.CombinedOutput()
			if err != nil {
				framework.Logf("removing volume group with %+v failed: %s", vgremove, string(out))
			}
		}

		By("destroying namespace again")
		cmd := exec.Command(sshcmd, "sudo ndctl destroy-namespace --force all")
		if out, err := cmd.CombinedOutput(); err != nil {
			framework.Logf("erasing namespaces with %+v failed: %s", cmd, string(out))
		}

		By("reverting labels")
		labels := []string{
			`"feature.node.kubernetes.io/memory-nv.dax": null`,
			`"storage": null`,
			fmt.Sprintf(`"%s/convert-raw-namespaces": null`, driverName),
		}
		patch := fmt.Sprintf(`{"metadata":{"labels":{%s}}}`, strings.Join(labels, ", "))
		if _, err := f.ClientSet.CoreV1().Nodes().Patch(ctx, masterNode, k8stypes.MergePatchType, []byte(patch), metav1.PatchOptions{}, ""); err != nil {
			framework.Logf("removing labels failed: %v", err)
		}
	}()

	By("prepare for raw namespace conversion")

	// Create a raw namespace.
	out, err = exec.Command(sshcmd, "sudo ndctl create-namespace --mode raw").CombinedOutput()
	framework.ExpectNoError(err, "ndctl create-namespace: %q", string(out))

	// Now force conversion.
	patch := fmt.Sprintf(`{"metadata":{"labels":{"%s/convert-raw-namespaces": "force"}}}`, driverName)
	_, err = f.ClientSet.CoreV1().Nodes().Patch(ctx, masterNode, k8stypes.MergePatchType, []byte(patch), metav1.PatchOptions{}, "")
	framework.ExpectNoError(err, "set convert-raw-namespaces label")

	// Eventually the raw namespace should get converted to fsdax.
	// If this does not happen, then hopefully our log capturing will
	// show why.
	By("wait for fsdax namespace")
	Eventually(func() error {
		out, err := exec.Command(sshcmd, "sudo ndctl list -NR").CombinedOutput()
		if err != nil {
			return fmt.Errorf("ndctl list: %q: %v", string(out), err)
		}
		parsed, err := deploy.ParseNdctlOutput(out)
		if err != nil {
			return fmt.Errorf("parse ndctl list output %q: %v", string(out), err)
		}
		haveDax := false
		for _, region := range parsed.Regions {
			for _, namespace := range region.Namespaces {
				if namespace.Mode == "fsdax" {
					haveDax = true
				}
			}
		}
		if !haveDax {
			return fmt.Errorf("no fsdax namespace in\n%s", string(out))
		}
		return nil
	}, "5m", "10s").ShouldNot(HaveOccurred(), "fsdax namespace conversion")

	// Shortly after the conversion, the LVM driver should run and
	// find the existing volume group. We detect that based on log messages.
	// Metrics data would be nicer, but is harder to access from outside of
	// the cluster.
	By("wait for running driver on master node")
	Eventually(func() error {
		pods, err := f.ClientSet.CoreV1().Pods(namespace).List(context.Background(),
			metav1.ListOptions{
				LabelSelector: labels.SelectorFromSet(labels.Set(map[string]string{"app.kubernetes.io/part-of": "pmem-csi",
					"app.kubernetes.io/component": "node"})).String(),
				FieldSelector: "spec.nodeName=" + masterNode,
			},
		)
		if err != nil {
			return err
		}
		if len(pods.Items) != 1 {
			return errors.New("not exactly one pod running on master node")
		}
		pod := pods.Items[0]
		output, err := e2epod.GetPodLogs(f.ClientSet, pod.Namespace, pod.Name, "pmem-driver")
		if err != nil {
			return fmt.Errorf("get pmem-driver output on master node: %v", err)
		}
		parts := regexp.MustCompile(`"PMEM-CSI ready." capacity="[^\n]*(\d+)\s*\S+ available[^\n]*`).FindStringSubmatch(output)
		if len(parts) != 2 {
			return fmt.Errorf("pod %s has not printed the expected `PMEM-CSI ready.` log message yet", pod.Name)
		}
		if parts[1] == "0" {
			return fmt.Errorf("pod %s is ready, but without any capacity: %s", pod.Name, parts[0])
		}
		return nil
	}, "5m", "10s").ShouldNot(HaveOccurred(), "driver running on master node")
}

func findMasterNode(ctx context.Context, cs kubernetes.Interface) (string, error) {
	nodes, err := cs.CoreV1().Nodes().List(ctx, metav1.ListOptions{})
	if err != nil {
		return "", fmt.Errorf("list nodes: %v", err)
	}
	var nodeNames []string
	for _, node := range nodes.Items {
		nodeNames = append(nodeNames, node.Name)
		if strings.Contains(node.Name, "master") {
			return node.Name, nil
		}
	}
	return "", fmt.Errorf("none of the node names seems to be for the master node: %v", nodeNames)
}
