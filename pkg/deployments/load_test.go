/*
Copyright 2020 Intel Corporation.

SPDX-License-Identifier: Apache-2.0
*/

package deployments_test

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/intel/pmem-csi/deploy"
	api "github.com/intel/pmem-csi/pkg/apis/pmemcsi/v1alpha1"
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
		})
	}
}
