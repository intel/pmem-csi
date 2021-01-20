/*
Copyright 2019,2020 Intel Corporation

SPDX-License-Identifier: Apache-2.0
*/

package pmemcsidriver

import (
	"bytes"
	"context"
	"fmt"
	"io/ioutil"
	"net/http"
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var (
	caFile   = os.ExpandEnv("${TEST_WORK}/pmem-ca/ca.pem")
	certFile = os.ExpandEnv("${TEST_WORK}/pmem-ca/pmem-registry.pem")
	keyFile  = os.ExpandEnv("${TEST_WORK}/pmem-ca/pmem-registry-key.pem")
)

func TestMetrics(t *testing.T) {
	cases := map[string]struct {
		path     string
		response http.Response
	}{
		"version": {
			response: http.Response{
				StatusCode: 200,
				Body: ioutil.NopCloser(bytes.NewBufferString(`# HELP build_info A metric with a constant '1' value labeled by version.
# TYPE build_info gauge
build_info{version="foo-bar-test"} 1
`)),
			},
		},
		"not found": {
			path: "/invalid",
			response: http.Response{
				StatusCode: 404,
			},
		},
	}
	for n, c := range cases {
		t.Run(n, func(t *testing.T) {
			path := "/metrics2"
			pmemd, err := GetCSIDriver(Config{
				Mode:          Webhooks,
				DriverName:    "pmem-csi",
				NodeID:        "testnode",
				Endpoint:      "unused",
				Version:       "foo-bar-test",
				CAFile:        caFile,
				CertFile:      certFile,
				KeyFile:       keyFile,
				metricsPath:   path,
				metricsListen: "127.0.0.1:", // port allocated dynamically
			})
			require.NoError(t, err, "get PMEM-CSI driver")

			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			addr, err := pmemd.startMetrics(ctx, cancel)
			require.NoError(t, err, "start server")

			tr := &http.Transport{}
			defer tr.CloseIdleConnections()
			client := &http.Client{
				Transport: tr,
			}
			url := fmt.Sprintf("http://%s%s%s", addr, path, c.path)
			resp, err := client.Get(url)
			checkResponse(t, &c.response, resp, err, n)
		})
	}
}

func checkResponse(t *testing.T, expected, actual *http.Response, err error, what string) {
	if assert.NoError(t, err, what) {
		defer actual.Body.Close()

		assert.Equal(t, expected.StatusCode, actual.StatusCode, what)
		body, err := ioutil.ReadAll(actual.Body)
		if assert.NoError(t, err, "read actual body") &&
			expected.Body != nil {
			body, err = ioutil.ReadAll(expected.Body)
			if assert.NoError(t, err, "read expected body") {
				expectedBody := string(body)
				actualBody := string(body)
				// Substring search because the full body contains a lot of additional metrics data.
				assert.Contains(t, actualBody, expectedBody, "body")
			}
		}
	}
}
