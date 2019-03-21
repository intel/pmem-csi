/*
Copyright 2019 Intel Corporation.

SPDX-License-Identifier: Apache-2.0
*/

package storage

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/kubernetes/test/e2e/framework"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
)

var _ = Describe("PMEM Cluster", func() {
	f := framework.NewDefaultFramework("pmem")

	// This checks that cluster-driver-registrar added the
	// CSIDriverInfo for pmem-csi at some point in the past. A
	// full test must include resetting the cluster and installing
	// pmem-csi.
	It("has CSIDriverInfo", func() {
		dc := f.DynamicClient
		csiDriverGVR := schema.GroupVersionResource{Group: "csi.storage.k8s.io", Version: "v1alpha1", Resource: "csidrivers"}
		_, err := dc.Resource(csiDriverGVR).Namespace("").Get("pmem-csi.intel.com", metav1.GetOptions{})
		Expect(err).NotTo(HaveOccurred(), "get csidriver.csi.storage.k8s.io for pmem-csi failed")
	})
})
