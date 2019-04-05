/*
Copyright 2017 The Kubernetes Authors.

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
	"fmt"
	"strings"

	"github.com/kubernetes-csi/csi-test/pkg/sanity"
	appsv1 "k8s.io/api/apps/v1"
	"k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	clientset "k8s.io/client-go/kubernetes"
	"k8s.io/kubernetes/test/e2e/framework"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
)

// Run the csi-test sanity tests against a pmem-csi driver deployed in
// unified mode.
var _ = Describe("sanity", func() {
	f := framework.NewDefaultFramework("pmem")
	f.SkipNamespaceCreation = true // We don't need a per-test namespace and skipping it makes the tests run faster.

	var (
		cleanup func()
		config  = sanity.Config{
			TestVolumeSize: 1 * 1024 * 1024,
			// The actual directories will be created as unique
			// temp directories inside these directories.
			// We intentionally do not use the real /var/lib/kubelet/pods as
			// root for the target path, because kubelet is monitoring it
			// and deletes all extra entries that it does not know about.
			TargetPath:  "/var/lib/kubelet/plugins/kubernetes.io/csi/pv/pmem-sanity-target.XXXXXX",
			StagingPath: "/var/lib/kubelet/plugins/kubernetes.io/csi/pv/pmem-sanity-staging.XXXXXX",
		}
	)

	BeforeEach(func() {
		cs := f.ClientSet

		// This test expects that PMEM-CSI was deployed with
		// socat port forwarding enabled (see deploy/kustomize/testing/README.md).
		hosts, err := framework.NodeSSHHosts(cs)
		Expect(err).NotTo(HaveOccurred(), "failed to find external/internal IPs for every node")
		if len(hosts) <= 1 {
			framework.Failf("not enough nodes with external IP")
		}
		// Node #1 is expected to have a PMEM-CSI node driver
		// instance. If it doesn't, connecting to the PMEM-CSI
		// node service will fail.
		host := strings.Split(hosts[1], ":")[0] // Instead of duplicating the NodeSSHHosts logic we simply strip the ssh port.
		config.Address = fmt.Sprintf("dns:///%s:%d", host, 9735)
		config.ControllerAddress = fmt.Sprintf("dns:///%s:%d", host, getServicePort(cs, "pmem-csi-controller-testing"))

		// Wait for socat pod on that node. We need it for
		// creating directories.  We could use the PMEM-CSI
		// node container, but that then forces us to have
		// mkdir and rmdir in that container, which we might
		// not want long-term.
		socat := getAppInstance(cs, "pmem-csi-node-testing", host)

		// Determine how many nodes have the CSI-PMEM running.
		set := getDaemonSet(cs, "pmem-csi-node")

		// We have to ensure that volumes get provisioned on
		// the host were we can do the node operations. We do
		// that by creating cache volumes on each node.
		config.TestVolumeParameters = map[string]string{
			"persistencyModel": "cache",
			"cacheSize":        fmt.Sprintf("%d", set.Status.DesiredNumberScheduled),
		}

		exec := func(args ...string) string {
			// f.ExecCommandInContainerWithFullOutput assumes that we want a pod in the test's namespace,
			// so we have to set one.
			f.Namespace = &v1.Namespace{
				ObjectMeta: metav1.ObjectMeta{
					Name: "default",
				},
			}
			stdout, stderr, err := f.ExecCommandInContainerWithFullOutput(socat.Name, "socat", args...)
			framework.ExpectNoError(err, "%s in socat container, stderr:\n%s", args, stderr)
			Expect(stderr).To(BeEmpty(), "unexpected stderr from %s in socat container", args)
			return stdout
		}
		mkdir := func(path string) (string, error) {
			return exec("mktemp", "-d", path), nil
		}
		rmdir := func(path string) error {
			exec("rmdir", path)
			return nil
		}

		config.CreateTargetDir = mkdir
		config.CreateStagingDir = mkdir
		config.RemoveTargetPath = rmdir
		config.RemoveStagingPath = rmdir
	})

	AfterEach(func() {
		if cleanup != nil {
			cleanup()
		}
	})
	// This adds several tests that just get skipped.
	// TODO: static definition of driver capabilities (https://github.com/kubernetes-csi/csi-test/issues/143)
	sanity.GinkgoTest(&config)
})

func getServicePort(cs clientset.Interface, serviceName string) int32 {
	var port int32
	Eventually(func() bool {
		service, err := cs.CoreV1().Services("default").Get(serviceName, metav1.GetOptions{})
		if err != nil {
			return false
		}
		port = service.Spec.Ports[0].NodePort
		return port != 0
	}, "3m").Should(BeTrue(), "%s service running", serviceName)
	return port
}

func getAppInstance(cs clientset.Interface, app string, ip string) *v1.Pod {
	var pod *v1.Pod
	Eventually(func() bool {
		pods, err := cs.CoreV1().Pods("default").List(metav1.ListOptions{})
		if err != nil {
			return false
		}
		for _, p := range pods.Items {
			if p.Labels["app"] == app &&
				(p.Status.HostIP == ip || p.Status.PodIP == ip) {
				pod = &p
				return true
			}
		}
		return false
	}, "3m").Should(BeTrue(), "%s app running on host %s", app, ip)
	return pod
}

func getDaemonSet(cs clientset.Interface, setName string) *appsv1.DaemonSet {
	var set *appsv1.DaemonSet
	Eventually(func() bool {
		s, err := cs.AppsV1().DaemonSets("default").Get(setName, metav1.GetOptions{})
		if err != nil {
			return false
		}
		set = s
		return set != nil
	}, "3m").Should(BeTrue(), "%s pod running", setName)
	return set
}
