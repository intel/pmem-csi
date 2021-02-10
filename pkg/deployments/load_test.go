/*
Copyright 2020 Intel Corporation.

SPDX-License-Identifier: Apache-2.0
*/

package deployments_test

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/intel/pmem-csi/deploy"
	api "github.com/intel/pmem-csi/pkg/apis/pmemcsi/v1beta1"
	"github.com/intel/pmem-csi/pkg/deployments"
	"github.com/intel/pmem-csi/pkg/version"
)

func TestLoadObjects(t *testing.T) {
	_, err := deployments.LoadObjects(version.NewVersion(1, 0), api.DeviceModeDirect)
	assert.Error(t, err, "load yaml for unsupported version")

	yamls := deploy.ListAll()
	assert.NotEmpty(t, yamls, "should have builtin yaml deployments")

	for _, testCase := range yamls {
		t.Run(testCase.Name, func(t *testing.T) {
			objects, err := deployments.LoadObjects(testCase.Kubernetes, testCase.DeviceMode)
			if assert.NoError(t, err, "load yaml") {
				assert.NotEmpty(t, objects, "have objects")

				// Check that all objects have the right label.
				expectedDeployment := fmt.Sprintf("%s-production", testCase.DeviceMode)
				for _, obj := range objects {
					labels := obj.GetLabels()
					if assert.Contains(t, labels, "pmem-csi.intel.com/deployment", "object %v should have deployment label", obj) {
						deployment := labels["pmem-csi.intel.com/deployment"]
						assert.Equal(t, expectedDeployment, deployment, "deployment label")
					}
				}
			}
			// Check customizing namespace and name. More customization tests
			// currently run as part of test/e2e/operator API testing, with
			// the code in controller_driver.go serving as reference.
			namespace := "kube-system"
			deployment := api.PmemCSIDeployment{
				ObjectMeta: metav1.ObjectMeta{
					Name: "pmem-csi.example.org",
				},
			}
			objects, err = deployments.LoadAndCustomizeObjects(testCase.Kubernetes, testCase.DeviceMode, namespace, deployment, nil)
			if assert.NoError(t, err, "load and customize yaml") {
				assert.NotEmpty(t, objects, "have customized objects")

				for _, obj := range objects {
					if obj.GetKind() == "CSIDriver" {
						assert.Equal(t, deployment.GetName(), obj.GetName(), "CSIDriver name")
					} else {
						assert.Contains(t, obj.GetName(), deployment.GetHyphenedName(), "other object name")
					}
					switch obj.GetKind() {
					case "CSIDriver", "ClusterRole", "ClusterRoleBinding":
						assert.Equal(t, "", obj.GetNamespace(), "non-namespaced %s namespace", obj.GetName())
					default:
						assert.Equal(t, namespace, obj.GetNamespace(), "non-namespaced %s namespace", obj.GetName())
					}
				}
			}
		})
	}
}
