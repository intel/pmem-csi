/*
Copyright 2019 Intel Corporation.

SPDX-License-Identifier: Apache-2.0
*/

package operator

import (
	"github.com/intel/pmem-csi/test/e2e/deploy"

	. "github.com/onsi/ginkgo"
)

var _ = deploy.DescribeForSome("driver", func(d *deploy.Deployment) bool {
	// Run these tests for all driver deployments that were created
	// through the operator.
	return d.HasOperator && d.HasDriver
}, func(d *deploy.Deployment) {

	It("runs", func() {
		// Once we get here, the deploy package has already checked for us that the driver is operational.
	})
})
