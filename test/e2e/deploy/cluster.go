/*
Copyright 2020 Intel Corporation.

SPDX-License-Identifier: Apache-2.0
*/

package deploy

import (
	"context"
	"crypto/tls"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/prometheus/common/expfmt"
	appsv1 "k8s.io/api/apps/v1"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/kubernetes/test/e2e/framework"
	e2essh "k8s.io/kubernetes/test/e2e/framework/ssh"

	. "github.com/onsi/gomega"
)

type Cluster struct {
	nodeIPs []string
	cs      kubernetes.Interface
}

func NewCluster(cs kubernetes.Interface) (*Cluster, error) {
	cluster := &Cluster{
		cs: cs,
	}

	hosts, err := e2essh.NodeSSHHosts(cs)
	if err != nil {
		return nil, fmt.Errorf("find external/internal IPs for every node: %v", err)
	}
	if len(hosts) <= 1 {
		return nil, fmt.Errorf("expected one master and one worker node, only got: %v", hosts)
	}
	for _, sshHost := range hosts {
		host := strings.Split(sshHost, ":")[0] // Instead of duplicating the NodeSSHHosts logic we simply strip the ssh port.
		cluster.nodeIPs = append(cluster.nodeIPs, host)
	}
	return cluster, nil
}

// NumNodes returns the total number of nodes in the cluster.
// Node #0 is the master node, the rest are workers.
func (c *Cluster) NumNodes() int {
	return len(c.nodeIPs)
}

// NodeIP returns the IP address of a certain node.
func (c *Cluster) NodeIP(node int) string {
	return c.nodeIPs[node]
}

// NodeServiceAddress returns the dial address for a certain port on a certain nodes.
func (c *Cluster) NodeServiceAddress(node int, port int) string {
	return fmt.Sprintf("dns:///%s:%d", c.nodeIPs[node], port)
}

// GetServicePort looks up the node port of a service.
func (c *Cluster) GetServicePort(serviceName, namespace string) (int, error) {
	service, err := c.cs.CoreV1().Services(namespace).Get(serviceName, metav1.GetOptions{})
	if err != nil {
		return 0, err
	}
	return int(service.Spec.Ports[0].NodePort), nil
}

// WaitForServicePort waits for the service to appear and returns its ports.
func (c *Cluster) WaitForServicePort(serviceName, namespace string) int {
	var port int
	Eventually(func() bool {
		var err error
		port, err = c.GetServicePort(serviceName, namespace)
		return err == nil && port != 0
	}, "3m").Should(BeTrue(), "%s service running", serviceName)
	return port
}

// GetAppInstance looks for a pod with a certain app label and a specific host or pod IP.
// The IP may also be empty.
func (c *Cluster) GetAppInstance(app, ip, namespace string) (*v1.Pod, error) {
	pods, err := c.cs.CoreV1().Pods(namespace).List(metav1.ListOptions{})
	if err != nil {
		return nil, err
	}
	for _, p := range pods.Items {
		if p.Labels["app"] == app &&
			(ip == "" || p.Status.HostIP == ip || p.Status.PodIP == ip) {
			return &p, nil
		}
	}
	return nil, fmt.Errorf("no app %q in namespace %q with IP %q found", app, namespace, ip)
}

func (c *Cluster) WaitForAppInstance(app, ip, namespace string) *v1.Pod {
	var pod *v1.Pod
	Eventually(func() bool {
		var err error
		pod, err = c.GetAppInstance(app, ip, namespace)
		return err == nil
	}, "3m").Should(BeTrue(), "%s app running on host %s", app, ip)
	return pod
}

func (c *Cluster) GetDaemonSet(setName, namespace string) (*appsv1.DaemonSet, error) {
	return c.cs.AppsV1().DaemonSets(namespace).Get(setName, metav1.GetOptions{})
}

func (c *Cluster) WaitForDaemonSet(setName string) *appsv1.DaemonSet {
	var set *appsv1.DaemonSet
	Eventually(func() bool {
		var err error
		set, err = c.cs.AppsV1().DaemonSets("default").Get(setName, metav1.GetOptions{})
		return err == nil
	}, "3m").Should(BeTrue(), "%s DaemonSet running", setName)
	return set
}

// WaitForPMEMDriver ensures that the PMEM-CSI driver is ready for use, which is
// defined as:
// - controller service is up and running
// - all nodes have registered
//
// It returns the namespace in which the driver was found. However, at
// the moment it only checks the "default" namespace.
func (c *Cluster) WaitForPMEMDriver() (namespace string) {
	namespace = "default"

	ticker := time.NewTicker(10 * time.Second)
	defer ticker.Stop()
	deadline, cancel := context.WithTimeout(context.Background(), framework.TestContext.SystemDaemonsetStartupTimeout)
	defer cancel()

	tlsConfig := tls.Config{
		// We could load ca.pem with pmemgrpc.LoadClientTLS, but as we are not connecting to it
		// via the service name, that would be enough.
		InsecureSkipVerify: true,
	}
	tr := http.Transport{
		TLSClientConfig: &tlsConfig,
	}
	defer tr.CloseIdleConnections()
	client := &http.Client{
		Transport: &tr,
	}

	ready := func() (err error) {
		defer func() {
			if err != nil {
				framework.Logf("wait for PMEM-CSI: %v", err)
			}
		}()

		// The controller service must be defined.
		port, err := c.GetServicePort("pmem-csi-metrics", "default")
		if err != nil {
			return err
		}

		// We can connect to it and get metrics data.
		url := fmt.Sprintf("https://%s:%d/metrics", c.NodeIP(0), port)
		resp, err := client.Get(url)
		if err != nil {
			return fmt.Errorf("get controller metrics: %v", err)
		}
		if resp.StatusCode != 200 {
			return fmt.Errorf("HTTP GET %s failed: %d", url, resp.StatusCode)
		}

		// Parse and check number of connected nodes. Dump the
		// version number while we are at it.
		parser := expfmt.TextParser{}
		metrics, err := parser.TextToMetricFamilies(resp.Body)
		if err != nil {
			return fmt.Errorf("parse metrics response: %v", err)
		}
		buildInfo, ok := metrics["build_info"]
		if !ok {
			return fmt.Errorf("expected build_info not found in metrics: %v", metrics)
		}
		if len(buildInfo.Metric) != 1 {
			return fmt.Errorf("expected build_info to have one metric, got: %v", buildInfo.Metric)
		}
		buildMetric := buildInfo.Metric[0]
		if len(buildMetric.Label) != 1 {
			return fmt.Errorf("expected build_info to have one label, got: %v", buildMetric.Label)
		}
		label := buildMetric.Label[0]
		if *label.Name != "version" {
			return fmt.Errorf("expected build_info to contain a version label, got: %s", label.Name)
		}
		framework.Logf("PMEM-CSI version: %s", label.Value)

		pmemNodes, ok := metrics["pmem_nodes"]
		if !ok {
			return fmt.Errorf("expected pmem_nodes not found in metrics: %v", metrics)
		}
		if len(pmemNodes.Metric) != 1 {
			return fmt.Errorf("expected pmem_nodes to have one metric, got: %v", pmemNodes.Metric)
		}
		nodesMetric := pmemNodes.Metric[0]
		actualNodes := int(*nodesMetric.Gauge.Value)
		if actualNodes != c.NumNodes()-1 {
			return fmt.Errorf("only %d of %d nodes have registered", actualNodes, c.NumNodes()-1)
		}

		return nil
	}

	if ready() == nil {
		return
	}
	for {
		select {
		case <-ticker.C:
			if ready() == nil {
				return
			}
		case <-deadline.Done():
			framework.Failf("giving up waiting for PMEM-CSI to start up, check the previous warnings and log output")
		}
	}
}
