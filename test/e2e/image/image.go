/*
Copyright 2019 Intel Corporation.

SPDX-License-Identifier: Apache-2.0
*/

package image

import (
	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/kubernetes/test/e2e/framework"

	. "github.com/onsi/ginkgo"

	"github.com/intel/pmem-csi/test/e2e/deploy"
	"github.com/intel/pmem-csi/test/e2e/pod"
)

// We are using direct mode here because it needs to do less work
// during startup. All we care about is that we get pods running our
// image.
var _ = deploy.Describe("direct-production", "direct-production-image", "", func(d *deploy.Deployment) {
	f := framework.NewDefaultFramework("image")
	f.SkipNamespaceCreation = true

	var nodeDriver *v1.Pod

	BeforeEach(func() {
		cluster, err := deploy.NewCluster(f.ClientSet, f.DynamicClient, f.ClientConfig())
		framework.ExpectNoError(err, "create cluster")
		nodeDriver = cluster.WaitForAppInstance(labels.Set{"app.kubernetes.io/name": "pmem-csi-node"},
			"", /* no IP, any of the pods is fine */
			d.Namespace,
		)
	})

	Context("ipmctl", func() {
		It("can run", func() {
			// "ipmctl version" would be nicer, but
			// doesn't work at the moment
			// (https://github.com/intel/ipmctl/issues/172).
			pod.RunInPod(f, "/", nil, "ipmctl help", nodeDriver.Namespace, nodeDriver.Name, "pmem-driver")
		})
	})
})
