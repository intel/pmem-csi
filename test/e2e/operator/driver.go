/*
Copyright 2019 Intel Corporation.

SPDX-License-Identifier: Apache-2.0
*/

package operator

import (
	"context"
	"time"

	"github.com/intel/pmem-csi/pkg/k8sutil"
	"github.com/intel/pmem-csi/test/e2e/deploy"
	"github.com/intel/pmem-csi/test/e2e/driver"
	"github.com/intel/pmem-csi/test/e2e/operator/validate"
	"github.com/intel/pmem-csi/test/e2e/storage/dax"

	"k8s.io/kubernetes/test/e2e/framework"
	"k8s.io/kubernetes/test/e2e/storage/testsuites"
	runtime "sigs.k8s.io/controller-runtime/pkg/client"

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

		client, err := runtime.New(f.ClientConfig(), runtime.Options{})
		framework.ExpectNoError(err, "new operator runtime client")

		k8sver, err := k8sutil.GetKubernetesVersion(f.ClientConfig())
		framework.ExpectNoError(err, "get Kubernetes version")

		// We need the actual CR from the apiserver to check ownership.
		deployment := d.GetDriverDeployment()
		deployment = deploy.GetDeploymentCR(f, deployment.Name)

		framework.ExpectNoError(validate.DriverDeploymentEventually(ctx, client, *k8sver, d.Namespace, deployment), "validate driver")
	})

	// Just very minimal testing at the moment.
	csiTestDriver := driver.New(d.Name, d.GetDriverDeployment().Name, []string{""} /* only the default fs type */, nil)
	var csiTestSuites = []func() testsuites.TestSuite{
		dax.InitDaxTestSuite,
	}

	testsuites.DefineTestSuite(csiTestDriver, csiTestSuites)
})
