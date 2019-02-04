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
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/kubernetes-csi/csi-test/pkg/sanity"
	appsv1 "k8s.io/api/apps/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/kubernetes/test/e2e/framework"
	"k8s.io/kubernetes/test/e2e/framework/podlogs"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
)

// Run the csi-test sanity tests against a pmem-csi driver deployed in
// unified mode.
var _ = Describe("sanity", func() {
	f := framework.NewDefaultFramework("pmem")

	var (
		cleanup func()
		config  = sanity.Config{
			TestVolumeSize: 1 * 1024 * 1024,
		}
		targetTmp string
		cancel    context.CancelFunc
	)

	BeforeEach(func() {
		cs := f.ClientSet
		ns := f.Namespace
		ctx, c := context.WithCancel(context.Background())
		cancel = c
		to := podlogs.LogOutput{
			StatusWriter: GinkgoWriter,
			LogWriter:    GinkgoWriter,
		}
		podlogs.CopyAllLogs(ctx, cs, ns.Name, to)
		podlogs.WatchPods(ctx, cs, ns.Name, GinkgoWriter)

		By("deploying pmem-csi")
		// The "unified" mode is needed only because of https://github.com/kubernetes-csi/csi-test/issues/142.
		// TODO (?): address that issue, then remove "unified" mode to make the driver simpler
		// and sanity testing more realistic.
		// TODO: use the already deployed driver. That also gets rid of the hard-coded version number.
		cl, err := f.CreateFromManifests(func(item interface{}) error {
			switch item := item.(type) {
			case *appsv1.StatefulSet:
				// Lock the unified driver onto a well-known host. We need to know that
				// for ssh below.
				item.Spec.Template.Spec.NodeName = "host-1"
			}
			return nil
		},
			"deploy/kubernetes-1.13/pmem-unified-csi.yaml",
		)
		Expect(err).NotTo(HaveOccurred())
		cleanup = cl

		targetTmp = ssh("mktemp", "-d", "-p", "/var/tmp")
		targetTmp = strings.Trim(targetTmp, "\n")
		config.TargetPath = filepath.Join(targetTmp, "target")
		config.StagingPath = filepath.Join(targetTmp, "staging")

		// The sanity packages creates these directories locally, but they are needed
		// inside the cluster (https://github.com/kubernetes-csi/csi-test/issues/144).
		// We work around that by creating them ourselves. However, the sanity package
		// will also do that locally under /var/tmp, which therefore must exist
		// and be writable.
		ssh("mkdir", "-p", config.TargetPath)
		ssh("mkdir", "-p", config.StagingPath)

		// Wait for pod before proceeding. StatefulSet gives
		// us a well-known name that we can wait for.
		f.WaitForPodRunning("pmem-csi-controller-0")

		// We let Kubernetes pick a new port for each test run dynamically.
		// That way we don't need to hard-code something that might
		// not be allowed by the kubelet configuration and more importantly,
		// whatever connection reuse logic is implemented in gRPC and
		// csi-test gets avoided. Such logic only works when dropped
		// connections are detected quickly, which didn't seem to be
		// the case when using a fixed port.
		var port int32
		Eventually(func() bool {
			service, err := cs.CoreV1().Services(f.Namespace.Name).Get("pmem-unified", metav1.GetOptions{})
			if err != nil {
				return false
			}
			port = service.Spec.Ports[0].NodePort
			return port != 0
		}, "3m").Should(BeTrue(), "pmem-csi service running")
		config.Address = fmt.Sprintf("192.168.8.2:%d", port)
	})

	AfterEach(func() {
		if cancel != nil {
			cancel()
		}
		if cleanup != nil {
			cleanup()
		}
		if targetTmp != "" {
			ssh("rm", "-rf", targetTmp)
		}
	})
	// This adds several tests that just get skipped.
	// TODO: static definition of driver capabilities (https://github.com/kubernetes-csi/csi-test/issues/143)
	sanity.GinkgoTest(&config)
})

func ssh(args ...string) string {
	sshWrapper := os.ExpandEnv("${REPO_ROOT}/_work/ssh-clear-kvm.1")
	cmd := exec.Command(sshWrapper, args...)
	out, err := cmd.CombinedOutput()
	Expect(err).NotTo(HaveOccurred(), "%s %s failed with error %s: %s", sshWrapper, args, err, out)
	return string(out)
}
