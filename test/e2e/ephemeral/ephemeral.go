/*
Copyright 2019 Intel Corporation.

SPDX-License-Identifier: Apache-2.0
*/

package ephemeral

import (
	"fmt"
	"os"

	"k8s.io/kubernetes/test/e2e/framework"
)

var Supported = func() bool {
	k8sVersion := os.Getenv("TEST_KUBERNETES_VERSION")

	if k8sVersion == "" {
		// No K8S version set, default enable ephemeral tests
		framework.Logf("No Kubernetes version set! Providing (via TEST_KUBERNETES_VERSION environment) the right Kubernetes version might affect the test suites to be run.")
		return true
	}
	var major, minor int
	if _, err := fmt.Sscanf(k8sVersion, "%d.%d", &major, &minor); err != nil {
		framework.Logf("Failed to parse 'TEST_KUBERNETES_VERSION=%s': %s. Enabling ephemeral volume tests.", k8sVersion, err.Error())
		// Allow ephemeral tests
		return true
	}
	if (major <= 0) || (major == 1 && minor <= 14) {
		// Kubernetes version <= 1.14 does not support ephemeral volumes.
		framework.Logf("Provided Kubernetes version '%s' does not support ephemeral volumes. Tests involving ephemeral volumes are disabled.", k8sVersion)
		return false
	}

	return true
}()
