/*
Copyright 2020 Intel Corporation.

SPDX-License-Identifier: Apache-2.0
*/

package deploy

import (
	"context"
	"fmt"
	"strings"

	appsv1 "k8s.io/api/apps/v1"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
	e2essh "k8s.io/kubernetes/test/e2e/framework/ssh"

	. "github.com/onsi/gomega"
)

type Cluster struct {
	nodeIPs []string
	cs      kubernetes.Interface
	dc      dynamic.Interface
}

func NewCluster(cs kubernetes.Interface, dc dynamic.Interface) (*Cluster, error) {
	cluster := &Cluster{
		cs: cs,
		dc: dc,
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

func (c *Cluster) ClientSet() kubernetes.Interface {
	return c.cs
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
	service, err := c.cs.CoreV1().Services(namespace).Get(context.Background(), serviceName, metav1.GetOptions{})
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
	pods, err := c.cs.CoreV1().Pods(namespace).List(context.Background(), metav1.ListOptions{})
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
	return c.cs.AppsV1().DaemonSets(namespace).Get(context.Background(), setName, metav1.GetOptions{})
}

func (c *Cluster) WaitForDaemonSet(setName string) *appsv1.DaemonSet {
	var set *appsv1.DaemonSet
	Eventually(func() bool {
		var err error
		set, err = c.cs.AppsV1().DaemonSets("default").Get(context.Background(), setName, metav1.GetOptions{})
		return err == nil
	}, "3m").Should(BeTrue(), "%s DaemonSet running", setName)
	return set
}

func (c *Cluster) GetStatefulSet(setName, namespace string) (*appsv1.StatefulSet, error) {
	return c.cs.AppsV1().StatefulSets(namespace).Get(context.Background(), setName, metav1.GetOptions{})
}
