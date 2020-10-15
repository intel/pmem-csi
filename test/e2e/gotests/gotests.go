/*
Copyright 2019 Intel Corporation.

SPDX-License-Identifier: Apache-2.0
*/

package gotests

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/kubernetes/test/e2e/framework"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"

	"github.com/intel/pmem-csi/test/e2e/deploy"
	"github.com/intel/pmem-csi/test/e2e/pod"
)

// We are using direct mode here because it needs to do less work
// during startup than LVM and the pod is more privileged (writable
// /sys).
var _ = deploy.Describe("direct-testing", "direct-testing-gotests", "", func(d *deploy.Deployment) {
	f := framework.NewDefaultFramework("gotests")
	f.SkipNamespaceCreation = true

	// Register one test for each package.
	for _, pkg := range strings.Split(os.Getenv("TEST_PKGS"), " ") {
		pkg := pkg
		It(pkg, func() { runGoTest(f, pkg) })
	}
})

// runGoTest builds and copies the Go test binary into the PMEM-CSI
// driver container and executes it there. This way it runs in exactly
// the same environment as the driver (the distro's kernel, our
// container user space), which may or may not expose bugs that are
// not found when running those tests on the build host.
func runGoTest(f *framework.Framework, pkg string) {
	root := os.Getenv("REPO_ROOT")
	var err error

	build := exec.Command("/bin/sh", "-c", os.Getenv("TEST_CMD")+" -c -o _work/test.test "+pkg)
	build.Stdout = GinkgoWriter
	build.Stderr = GinkgoWriter
	build.Dir = root
	By("Compiling with: " + strings.Join(build.Args, " "))
	err = build.Run()
	framework.ExpectNoError(err, "compile test program for %s", pkg)

	label := labels.SelectorFromSet(labels.Set(map[string]string{"app": "pmem-csi-node"}))
	pods, err := f.ClientSet.CoreV1().Pods("default").List(context.Background(), metav1.ListOptions{LabelSelector: label.String()})
	framework.ExpectNoError(err, "list PMEM-CSI pods")
	Expect(pods.Items).NotTo(BeEmpty(), "have PMEM-CSI pods")
	pmem := pods.Items[0]

	By(fmt.Sprintf("Running in PMEM-CSI pod %s", pmem.Name))
	pod.RunInPod(f, root,
		[]string{"_work/test.test", "_work/evil-ca", "_work/pmem-ca", "deploy/crd/"},
		"if _work/test.test -h 2>&1 | grep -q ginkgo; then "+
			"TEST_WORK=_work REPO_ROOT=. _work/test.test -test.v -ginkgo.v; else "+
			"TEST_WORK=_work REPO_ROOT=. _work/test.test -test.v; fi",
		pmem.Namespace, pmem.Name, "pmem-driver")
}
