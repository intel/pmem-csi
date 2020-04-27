/*
Copyright 2019 Intel Corporation.

SPDX-License-Identifier: Apache-2.0
*/

package operator

import (
	"context"
	"time"

	"github.com/intel/pmem-csi/test/e2e/deploy"
	"github.com/intel/pmem-csi/test/e2e/operator/validate"

	"k8s.io/kubernetes/test/e2e/framework"

	. "github.com/onsi/ginkgo"
)

var _ = deploy.DescribeForSome("driver", func(d *deploy.Deployment) bool {
	// Run these tests for all driver deployments that were created
	// through the operator.
	return d.HasOperator && d.HasDriver
}, func(d *deploy.Deployment) {

	f := framework.NewDefaultFramework("driver")
	f.SkipNamespaceCreation = true

	It("runs", func() {
		// Once we get here, the deploy package has already checked for us that the driver is operational.
		// We can verify that it meets the spec.

		ctx, cancel := context.WithTimeout(context.Background(), 1*time.Minute)
		defer cancel()
		validate.DriverDeployment(ctx, f, d, d.GetDriverDeployment())
	})
})
