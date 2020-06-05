/*
Copyright 2019,2020 Intel Corporation.

SPDX-License-Identifier: Apache-2.0
*/

package gotests

import (
	"crypto/tls"
	"fmt"
	"k8s.io/kubernetes/test/e2e/framework"
	"net/http"
	"time"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"

	"github.com/intel/pmem-csi/test/e2e/deploy"
)

// We only need to test this in one deployment because the tests
// do not depend on the driver mode or how it was deployed.
var _ = deploy.Describe("direct-testing", "direct-testing-metrics", "", func(d *deploy.Deployment) {
	f := framework.NewDefaultFramework("metrics")
	f.SkipNamespaceCreation = true

	var (
		metricsURL string
		tlsConfig  = tls.Config{
			// We could load ca.pem with pmemgrpc.LoadClientTLS, but as we are not connecting to it
			// via the service name, that would be enough.
			InsecureSkipVerify: true,
		}
		tr = http.Transport{
			TLSClientConfig: &tlsConfig,
		}
		timeout = 5 * time.Second
		client  = &http.Client{
			Transport: &tr,
			Timeout:   timeout,
		}
	)

	BeforeEach(func() {
		cluster, err := deploy.NewCluster(f.ClientSet, f.DynamicClient)
		framework.ExpectNoError(err, "get cluster information")
		metricsURL = deploy.WaitForPMEMDriver(cluster, "pmem-csi", d.Namespace)
	})

	It("works", func() {
		// WaitForPMEMDriver already verified that "version"
		// is returned. More tests could be added here.
	})

	It("rejects large headers", func() {
		req, err := http.NewRequest("GET", metricsURL, nil)
		framework.ExpectNoError(err, "create GET request for %s", metricsURL)
		for i := 0; i < 100000; i++ {
			req.Header.Add(fmt.Sprintf("My-Header-%d", i), "foobar")
		}
		resp, err := client.Do(req)
		framework.ExpectNoError(err, "execute GET request for %s", metricsURL)
		Expect(resp.StatusCode).Should(Equal(http.StatusRequestHeaderFieldsTooLarge), "header too large")
	})

	It("rejects invalid path", func() {
		url := metricsURL + "/extra-path"
		resp, err := client.Get(url)
		framework.ExpectNoError(err, "execute GET request for %s", url)
		Expect(resp.StatusCode).Should(Equal(http.StatusNotFound), "not found")
	})
})
