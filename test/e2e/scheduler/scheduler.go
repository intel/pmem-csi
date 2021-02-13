/*
Copyright 2019,2020 Intel Corporation.

SPDX-License-Identifier: Apache-2.0
*/

package gotests

import (
	"bytes"
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io/ioutil"
	"math"
	"math/big"
	"net/http"
	"strings"
	"time"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"

	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	schedulerapi "k8s.io/kube-scheduler/extender/v1"
	"k8s.io/kubernetes/test/e2e/framework"

	pmemgrpc "github.com/intel/pmem-csi/pkg/pmem-grpc"
	"github.com/intel/pmem-csi/test/e2e/deploy"
	testconfig "github.com/intel/pmem-csi/test/test-config"
)

// We only need to test this in one deployment because the tests
// do not depend on the driver mode or how it was deployed.
var _ = deploy.Describe("direct-testing", "direct-testing", "scheduler", func(d *deploy.Deployment) {
	f := framework.NewDefaultFramework("scheduler")
	f.SkipNamespaceCreation = true

	timeout := 5 * time.Second
	schedulerURL := ""
	caFile := testconfig.GetOrFail("TEST_CA") + ".pem"
	caKeyFile := testconfig.GetOrFail("TEST_CA") + "-key.pem"
	schedulerPort := testconfig.GetOrFail("TEST_SCHEDULER_EXTENDER_NODE_PORT")

	caCert, err := loadCertificate(caFile)
	framework.ExpectNoError(err, "load CA certificate from file: %s", caFile)

	caKey, err := loadKey(caKeyFile)
	framework.ExpectNoError(err, "load CA key from file: %s", caKeyFile)

	clientKey, err := rsa.GenerateKey(rand.Reader, 3072)
	framework.ExpectNoError(err, "generate client key")

	clientCert, err := generateCertificate(caKey, caCert, "test-scheduler", time.Now(), time.Now().Add(time.Hour), clientKey.Public())
	framework.ExpectNoError(err, "generate client certificate")

	tlsConfig, err := pmemgrpc.ClientTLS(encodeCert(caCert), encodeCert(clientCert), encodeKey(clientKey), "pmem-csi-scheduler")
	framework.ExpectNoError(err, "prepare client tls config")

	client := &http.Client{
		Transport: &http.Transport{
			TLSClientConfig: tlsConfig,
		},
		Timeout: timeout,
	}

	BeforeEach(func() {
		cluster, err := deploy.NewCluster(f.ClientSet, f.DynamicClient)
		framework.ExpectNoError(err, "get cluster information")
		deploy.WaitForPMEMDriver(cluster, d)

		pods, err := f.ClientSet.CoreV1().Pods(d.Namespace).List(context.Background(), metav1.ListOptions{
			LabelSelector: "app.kubernetes.io/name in ( pmem-csi-controller )",
		})
		framework.ExpectNoError(err, "list controller pods")
		Expect(pods.Items).NotTo(BeEmpty(), "at least one controller pod should be running")

		schedulerURL = "https://" + pods.Items[0].Status.HostIP + ":" + schedulerPort
		By(fmt.Sprintf("Scheduler URL: %s", schedulerURL))
	})

	It("works", func() {
		nodeLabel := testconfig.GetOrFail("TEST_PMEM_NODE_LABEL")
		parts := strings.Split(nodeLabel, "=")
		Expect(len(parts)).Should(Equal(2), "parse node label '%s'", nodeLabel)

		// fetch worker nodes
		nodes, err := f.ClientSet.CoreV1().Nodes().List(context.Background(), metav1.ListOptions{
			LabelSelector: parts[0] + " in (" + parts[1] + ")",
		})
		framework.ExpectNoError(err, "get pmem nodes")

		pod := &v1.Pod{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test-pod",
				Namespace: "testns",
			},
			Spec: v1.PodSpec{
				NodeName:   nodes.Items[0].GetName(),
				Containers: []v1.Container{},
				Volumes: []v1.Volume{
					{
						Name: "volume",
						VolumeSource: v1.VolumeSource{
							CSI: &v1.CSIVolumeSource{
								Driver: d.DriverName,
								VolumeAttributes: map[string]string{
									"size": "2Mi",
								},
							},
						},
					},
				},
			},
		}

		// Any worker node in test cluster should be fine as they
		// all should have enough pmem capacity for the request
		// volume size, i.e, "2Mi"
		nodeNames := []string{nodes.Items[0].Name}
		args := schedulerapi.ExtenderArgs{
			Pod:       pod,
			NodeNames: &nodeNames,
		}

		requestBody, err := json.Marshal(args)
		framework.ExpectNoError(err, "marshal request")

		request, err := http.NewRequest("GET", schedulerURL+"/filter", bytes.NewReader(requestBody))
		framework.ExpectNoError(err, "create GET request for %s", schedulerURL)

		resp, err := client.Do(request)
		framework.ExpectNoError(err, "execute GET request for %s", schedulerURL)

		defer resp.Body.Close()

		var result schedulerapi.ExtenderFilterResult
		bytes, err := ioutil.ReadAll(resp.Body)
		framework.ExpectNoError(err, "read response body")
		err = json.Unmarshal(bytes, &result)
		framework.ExpectNoError(err, "unmarshal response")
		Expect(result.Error).Should(BeEmpty(), "failed fileter")
		Expect(result.NodeNames).ShouldNot(BeNil(), "filter nodes are empty")
		Expect(*result.NodeNames).Should(ContainElements(nodeNames), "should match all nodes")
	})

	It("rejects large headers", func() {
		req, err := http.NewRequest("GET", schedulerURL+"/filter", nil)
		framework.ExpectNoError(err, "create GET request for %s", schedulerURL)
		for i := 0; i < 100000; i++ {
			req.Header.Add(fmt.Sprintf("My-Header-%d", i), "foobar")
		}
		resp, err := client.Do(req)
		framework.ExpectNoError(err, "execute GET request for %s", schedulerURL)
		Expect(resp.StatusCode).Should(Equal(http.StatusRequestHeaderFieldsTooLarge), "header too large")
	})

	It("rejects invalid path", func() {
		url := schedulerURL + "/extra-path"
		resp, err := client.Get(url)
		framework.ExpectNoError(err, "execute GET request for %s", url)
		Expect(resp.StatusCode).Should(Equal(http.StatusNotFound), "not found")
	})

	It("rejects evil CA", func() {
		evilCAKey, err := rsa.GenerateKey(rand.Reader, 3072)
		framework.ExpectNoError(err, "generate CA key")
		evilCACert, err := selfSignedCACertificate(evilCAKey)
		framework.ExpectNoError(err, "generate CA certificate")

		clientCert, err := generateCertificate(evilCAKey, evilCACert, "evil-host", time.Now(), time.Now().Add(365*24*time.Hour), clientKey.Public())
		framework.ExpectNoError(err, "generate client certificate")

		tlsConfig, err := pmemgrpc.ClientTLS(encodeCert(evilCACert), encodeCert(clientCert), encodeKey(clientKey), "pmem-csi-scheduler")
		framework.ExpectNoError(err, "prepare tls config")

		client := http.Client{
			Transport: &http.Transport{
				TLSClientConfig: tlsConfig,
			},
			Timeout: timeout,
		}

		req, err := http.NewRequest("GET", schedulerURL+"/filter", bytes.NewBufferString("Some Body"))
		framework.ExpectNoError(err, "create GET request for %s", schedulerURL)

		_, err = client.Do(req)
		time.Sleep(time.Second * 5)
		Expect(err).ShouldNot(BeNil(), "GET request for %s should fail with error", schedulerURL)
		Expect(err.Error()).Should(HaveSuffix("x509: certificate signed by unknown authority"), "GET should fail with bad certificate")
	})

	It("rejects expired certificate", func() {
		expiredCert, err := generateCertificate(caKey, caCert, "expired-ca", time.Now().Add(-1*time.Hour), time.Now().Add(-1*time.Minute), clientKey.Public())
		framework.ExpectNoError(err, "generate expired certificate")

		// valid CA - but expired certificate
		tlsConfig, err := pmemgrpc.ClientTLS(encodeCert(caCert), encodeCert(expiredCert), encodeKey(clientKey), "pmem-csi-scheduler")
		framework.ExpectNoError(err, "prepare tls config")

		client := http.Client{
			Transport: &http.Transport{
				TLSClientConfig: tlsConfig,
			},
			Timeout: timeout,
		}

		req, err := http.NewRequest("GET", schedulerURL+"/filter", bytes.NewBufferString("Some Body"))
		framework.ExpectNoError(err, "create GET request for %s", schedulerURL)

		_, err = client.Do(req)
		Expect(err).ShouldNot(BeNil(), "GET request for %s should fail with error", schedulerURL)
		Expect(err.Error()).Should(HaveSuffix("tls: bad certificate"), "GET should fail with bad certificate")
	})
})

func loadCertificate(certFile string) (*x509.Certificate, error) {
	bytes, err := ioutil.ReadFile(certFile)
	if err != nil {
		return nil, err
	}

	blk, _ := pem.Decode(bytes)
	return x509.ParseCertificate(blk.Bytes)
}

func loadKey(keyFile string) (*rsa.PrivateKey, error) {
	bytes, err := ioutil.ReadFile(keyFile)
	if err != nil {
		return nil, err
	}

	blk, _ := pem.Decode(bytes)
	key, err := x509.ParsePKCS1PrivateKey(blk.Bytes)
	if err != nil {
		return nil, err
	}

	return key, nil
}

func encodeKey(key *rsa.PrivateKey) []byte {
	if key == nil {
		return []byte{}
	}
	return pem.EncodeToMemory(&pem.Block{
		Type:  "RSA PRIVATE KEY",
		Bytes: x509.MarshalPKCS1PrivateKey(key),
	})
}

func encodeCert(cert *x509.Certificate) []byte {
	if cert == nil {
		return []byte{}
	}
	return pem.EncodeToMemory(&pem.Block{
		Type:  "CERTIFICATE",
		Bytes: cert.Raw,
	})
}

func selfSignedCACertificate(key *rsa.PrivateKey) (*x509.Certificate, error) {
	max := new(big.Int).SetInt64(math.MaxInt64)
	serial, err := rand.Int(rand.Reader, max)
	if err != nil {
		return nil, err
	}
	tmpl := &x509.Certificate{
		Version:               tls.VersionTLS12,
		SerialNumber:          serial,
		NotBefore:             time.Now(),
		NotAfter:              time.Now().Add(time.Hour * 24 * 365).UTC(),
		KeyUsage:              x509.KeyUsageKeyEncipherment | x509.KeyUsageDigitalSignature | x509.KeyUsageCertSign,
		IsCA:                  true,
		BasicConstraintsValid: true,
		DNSNames:              []string{"evil-ca"},
	}
	certBytes, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, key.Public(), key)
	if err != nil {
		return nil, err
	}

	return x509.ParseCertificate(certBytes)
}

func generateCertificate(caKey *rsa.PrivateKey, caCert *x509.Certificate, cn string, notBefore, notAfter time.Time, key crypto.PublicKey) (*x509.Certificate, error) {
	max := new(big.Int).SetInt64(math.MaxInt64)
	serial, err := rand.Int(rand.Reader, max)
	if err != nil {
		return nil, err
	}

	tmpl := &x509.Certificate{
		Version:      tls.VersionTLS12,
		SerialNumber: serial,
		NotBefore:    notBefore,
		NotAfter:     notAfter,
		KeyUsage:     x509.KeyUsageKeyEncipherment | x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth, x509.ExtKeyUsageServerAuth},
		DNSNames:     []string{cn},
	}

	certBytes, err := x509.CreateCertificate(rand.Reader, tmpl, caCert, key, caKey)
	*tmpl = x509.Certificate{}
	if err != nil {
		return nil, err
	}

	cert, err := x509.ParseCertificate(certBytes)
	if err != nil {
		return nil, err
	}

	return cert, nil
}
