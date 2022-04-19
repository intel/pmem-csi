/*
Copyright 2020 Intel Corporation.

SPDX-License-Identifier: Apache-2.0
*/

package deploy

import (
	"context"
	"fmt"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/fields"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"

	"github.com/intel/pmem-csi/pkg/k8sutil"
	"github.com/intel/pmem-csi/pkg/version"
	. "github.com/onsi/gomega"
)

type Cluster struct {
	nodeIPs []string
	cs      kubernetes.Interface
	dc      dynamic.Interface
	cfg     *rest.Config

	version     *version.Version
	isOpenShift bool
}

func NewCluster(cs kubernetes.Interface, dc dynamic.Interface, cfg *rest.Config) (*Cluster, error) {
	cluster := &Cluster{
		cs:  cs,
		dc:  dc,
		cfg: cfg,
	}

	hosts, err := nodeIPs(cs)
	if err != nil {
		return nil, fmt.Errorf("find external/internal IPs for every node: %v", err)
	}
	if len(hosts) <= 1 {
		return nil, fmt.Errorf("expected one master and one worker node, only got: %v", hosts)
	}
	cluster.nodeIPs = hosts

	version, err := k8sutil.GetKubernetesVersion(cfg)
	if err != nil {
		return nil, err
	}
	cluster.version = version
	isOpenShift, err := k8sutil.IsOpenShift(cfg)
	if err != nil {
		return nil, err
	}
	cluster.isOpenShift = isOpenShift
	return cluster, nil
}

func (c *Cluster) ClientSet() kubernetes.Interface {
	return c.cs
}

func (c *Cluster) Config() *rest.Config {
	return c.cfg
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

// NodeServiceAddress returns the gRPC dial address for a certain port on a certain nodes.
func (c *Cluster) NodeServiceAddress(node int, port int) string {
	return fmt.Sprintf("dns:///%s:%d", c.nodeIPs[node], port)
}

// GetServicePort looks up the node port of a service.
func (c *Cluster) GetServicePort(ctx context.Context, serviceName, namespace string) (int, error) {
	service, err := c.cs.CoreV1().Services(namespace).Get(ctx, serviceName, metav1.GetOptions{})
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
		ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
		defer cancel()
		port, err = c.GetServicePort(ctx, serviceName, namespace)
		return err == nil && port != 0
	}, "3m").Should(BeTrue(), "%s service running", serviceName)
	return port
}

// GetAppInstance looks for a pod with certain labels and a specific host or pod IP.
// The IP may also be empty.
func (c *Cluster) GetAppInstance(ctx context.Context, appLabels labels.Set, ip, namespace string) (*v1.Pod, error) {
	pods, err := c.cs.CoreV1().Pods(namespace).List(ctx, metav1.ListOptions{LabelSelector: appLabels.String()})
	if err != nil {
		return nil, err
	}
	for _, p := range pods.Items {
		if ip == "" || p.Status.HostIP == ip || p.Status.PodIP == ip {
			return &p, nil
		}
	}
	return nil, fmt.Errorf("no app %s in namespace %q with IP %q found", appLabels, namespace, ip)
}

// WaitForAppInstance waits for a running pod which matches the app
// label, optional host or pod IP, and namespace.
func (c *Cluster) WaitForAppInstance(appLabels labels.Set, ip, namespace string) *v1.Pod {
	var pod *v1.Pod
	Eventually(func() bool {
		var err error
		ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
		defer cancel()
		pod, err = c.GetAppInstance(ctx, appLabels, ip, namespace)
		return err == nil && pod.Status.Phase == v1.PodRunning
	}, "3m").Should(BeTrue(), "%s app running on host %s in '%s' namespace", appLabels, ip, namespace)
	return pod
}

func (c *Cluster) GetDaemonSet(ctx context.Context, setName, namespace string) (*appsv1.DaemonSet, error) {
	return c.cs.AppsV1().DaemonSets(namespace).Get(ctx, setName, metav1.GetOptions{})
}

func (c *Cluster) WaitForDaemonSet(setName, namespace string) *appsv1.DaemonSet {
	var set *appsv1.DaemonSet
	Eventually(func() bool {
		var err error
		ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
		defer cancel()
		set, err = c.GetDaemonSet(ctx, setName, namespace)
		return err == nil
	}, "3m").Should(BeTrue(), "%s DaemonSet running", setName)
	return set
}

func (c *Cluster) GetStatefulSet(ctx context.Context, setName, namespace string) (*appsv1.StatefulSet, error) {
	return c.cs.AppsV1().StatefulSets(namespace).Get(ctx, setName, metav1.GetOptions{})
}

// StorageCapacitySupported checks that the v1beta1 CSIStorageCapacity API is supported.
// It only checks the Kubernetes version.
func (c *Cluster) StorageCapacitySupported() bool {
	return c.version.Compare(1, 21) >= 0
}

// nodeIPs returns IP addresses for all schedulable nodes, in the
// order in which the nodes get listed.
//
// If it can't find any external IPs, it falls back to
// looking for internal IPs. If it can't find an internal IP for every node it
// returns an error, though it still returns all hosts that it found in that
// case.
//
// This was copied from https://github.com/kubernetes/kubernetes/blob/9e372bffeffbf52f024f23d050767bbeaa30ad92/test/e2e/framework/ssh/ssh.go
// without https://github.com/kubernetes/kubernetes/commit/9e372bffeffbf52f024f23d050767bbeaa30ad92
// because the order was no longer guaranteed.
func nodeIPs(c kubernetes.Interface) ([]string, error) {
	nodelist, err := waitListSchedulableNodes(c)
	if err != nil {
		return nil, err
	}

	hosts := nodeAddresses(nodelist, v1.NodeExternalIP)
	// If  ExternalIPs aren't available for all nodes, try falling back to the InternalIPs.
	if len(hosts) < len(nodelist.Items) {
		hosts = nodeAddresses(nodelist, v1.NodeInternalIP)
	}

	// Error if neither External nor Internal IPs weren't available for all nodes.
	if len(hosts) != len(nodelist.Items) {
		return hosts, fmt.Errorf(
			"only found %d IPs on nodes, but found %d nodes. Nodelist: %v",
			len(hosts), len(nodelist.Items), nodelist)
	}

	return hosts, nil
}

const (
	// pollNodeInterval is how often to Poll pods.
	pollNodeInterval = 2 * time.Second

	// singleCallTimeout is how long to try single API calls (like 'get' or 'list'). Used to prevent
	// transient failures from failing tests.
	singleCallTimeout = 5 * time.Minute
)

// waitListSchedulableNodes is a wrapper around listing nodes supporting retries.
func waitListSchedulableNodes(c kubernetes.Interface) (*v1.NodeList, error) {
	var nodes *v1.NodeList
	var err error
	if wait.PollImmediate(pollNodeInterval, singleCallTimeout, func() (bool, error) {
		nodes, err = c.CoreV1().Nodes().List(context.TODO(), metav1.ListOptions{FieldSelector: fields.Set{
			"spec.unschedulable": "false",
		}.AsSelector().String()})
		if err != nil {
			return false, err
		}
		return true, nil
	}) != nil {
		return nodes, err
	}
	return nodes, nil
}

// nodeAddresses returns the first address of the given type of each node.
func nodeAddresses(nodelist *v1.NodeList, addrType v1.NodeAddressType) []string {
	hosts := []string{}
	for _, n := range nodelist.Items {
		for _, addr := range n.Status.Addresses {
			if addr.Type == addrType && addr.Address != "" {
				hosts = append(hosts, addr.Address)
				break
			}
		}
	}
	return hosts
}
