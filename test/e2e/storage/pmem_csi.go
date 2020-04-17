/*
Copyright 2019 Intel Corporation.

SPDX-License-Identifier: Apache-2.0
*/

package storage

import (
	"context"

	k8scsi "k8s.io/api/storage/v1beta1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/discovery"
	"k8s.io/kubernetes/test/e2e/framework"

	"github.com/intel/pmem-csi/test/e2e/deploy"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
)

var _ = deploy.DescribeForAll("Deployment", func(d *deploy.Deployment) {
	f := framework.NewDefaultFramework("pmem")

	// This checks that cluster-driver-registrar added the
	// CSIDriverInfo for pmem-csi at some point in the past. A
	// full test must include resetting the cluster and installing
	// pmem-csi.
	It("has CSIDriverInfo", func() {
		if hasBetaAPI(f.ClientSet.Discovery()) {
			_, err := f.ClientSet.StorageV1beta1().CSIDrivers().Get(context.Background(), "pmem-csi.intel.com", metav1.GetOptions{})
			Expect(err).NotTo(HaveOccurred(), "get csidriver.storage.k8s.io for pmem-csi failed")
		} else {
			dc := f.DynamicClient
			csiDriverGVR := schema.GroupVersionResource{Group: "csi.storage.k8s.io", Version: "v1alpha1", Resource: "csidrivers"}
			_, err := dc.Resource(csiDriverGVR).Namespace("").Get(context.Background(), "pmem-csi.intel.com", metav1.GetOptions{})
			Expect(err).NotTo(HaveOccurred(), "get csidriver.csi.storage.k8s.io for pmem-csi failed")
		}
	})
})

func hasBetaAPI(ds discovery.DiscoveryInterface) bool {
	resources, err := discovery.ServerResources(ds)
	framework.ExpectNoError(err, "discover server resources")
	return hasResource(resources, k8scsi.SchemeGroupVersion.String(), "CSIDriver")
}

func hasResource(resources []*metav1.APIResourceList, groupVersion string, kind string) bool {
	for _, list := range resources {
		if list.GroupVersion == groupVersion {
			for _, resource := range list.APIResources {
				if resource.Kind == kind {
					return true
				}
			}
		}
	}
	return false
}
