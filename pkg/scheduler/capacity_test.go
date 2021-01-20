/*
Copyright 2020 Intel Corp.

SPDX-License-Identifier: Apache-2.0
*/

package scheduler

import (
	"net"
	"net/http"
	"strconv"
	"testing"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	corelistersv1 "k8s.io/client-go/listers/core/v1"
	"k8s.io/client-go/tools/cache"

	pmdmanager "github.com/intel/pmem-csi/pkg/pmem-device-manager"
)

type node struct {
	name, namespace string
	capacity        pmdmanager.Capacity
	driverName      string
	noMetrics       bool
}

func (n node) createPMEMPod(port int) *corev1.Pod {
	pod := corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "pmem-csi-node-" + n.name,
			Namespace: n.namespace,
			Labels: map[string]string{
				"app.kubernetes.io/part-of":   "pmem-csi",
				"app.kubernetes.io/component": "node",
				"app.kubernetes.io/instance":  n.driverName,
			},
		},
		Spec: corev1.PodSpec{
			NodeName: n.name,
			Containers: []corev1.Container{
				{
					Name: "pmem-driver",
				},
			},
		},
		Status: corev1.PodStatus{
			PodIP: "127.0.0.1", // address we listen on
		},
	}
	if port != 0 {
		pod.Spec.Containers[0].Ports = []corev1.ContainerPort{
			{
				Name:          "metrics",
				ContainerPort: int32(port),
			},
		}
	}
	return &pod
}

func TestCapacityFromMetrics(t *testing.T) {
	cap := pmdmanager.Capacity{
		MaxVolumeSize: 1000,
		Available:     2000,
		Managed:       3000,
		Total:         4000,
	}
	capSmall := pmdmanager.Capacity{
		MaxVolumeSize: 1,
		Available:     2,
		Managed:       3,
		Total:         4,
	}
	testcases := map[string]struct {
		nodes       []node
		node        string
		namespace   string
		driverName  string
		expected    int64
		expectError bool
	}{
		"one node": {
			nodes: []node{
				{
					name:     "foobar",
					capacity: cap,
				},
			},
			node:     "foobar",
			expected: 1000,
		},
		"no such node": {
			nodes: []node{
				{
					name:     "foo",
					capacity: pmdmanager.Capacity{MaxVolumeSize: 1000},
				},
			},
			node: "bar",
		},
		"no driver": {
			node: "foobar",
		},
		"wrong driver": {
			nodes: []node{
				{
					name:       "foobar",
					driverName: "AAA",
					capacity:   cap,
				},
			},
			node:       "foobar",
			driverName: "BBB",
		},
		"wrong namespace": {
			nodes: []node{
				{
					name:      "foobar",
					namespace: "default",
					capacity:  cap,
				},
			},
			node:      "foobar",
			namespace: "pmem-csi",
		},
		"multiple drivers": {
			nodes: []node{
				{
					name:       "foobar",
					driverName: "AAA",
					capacity:   capSmall,
				},
				{
					name:       "foobar",
					driverName: "BBB",
					capacity:   cap,
				},
			},
			node:       "foobar",
			driverName: "BBB",
			expected:   1000,
		},
		"metrics handler missing": {
			nodes: []node{
				{
					name:      "foobar",
					capacity:  cap,
					noMetrics: true,
				},
			},
			node:        "foobar",
			expectError: true,
		},
	}

	for name, tc := range testcases {
		t.Run(name, func(t *testing.T) {
			podIndexer := cache.NewIndexer(cache.MetaNamespaceKeyFunc, cache.Indexers{})

			// We need one metrics server per node.
			for _, node := range tc.nodes {
				mux := http.NewServeMux()
				registry := prometheus.NewPedanticRegistry()
				collector := pmdmanager.CapacityCollector{PmemDeviceCapacity: node.capacity}
				collector.MustRegister(registry, node.name, node.driverName)
				if !node.noMetrics {
					mux.Handle("/metrics", promhttp.HandlerFor(registry, promhttp.HandlerOpts{}))
				}
				listen := "127.0.0.1:"
				listener, err := net.Listen("tcp", listen)
				require.NoError(t, err, "listen")
				tcpListener := listener.(*net.TCPListener)
				server := http.Server{
					Handler: mux,
				}
				go server.Serve(listener)
				defer server.Close()

				// Now fake a PMEM-CSI pod on that node.
				_, portStr, err := net.SplitHostPort(tcpListener.Addr().String())
				require.NoError(t, err, "split listen address")
				port, err := strconv.Atoi(portStr)
				require.NoError(t, err, "parse listen port")
				pod := node.createPMEMPod(port)
				if pod != nil {
					podIndexer.Add(pod)
				}
			}
			podLister := corelistersv1.NewPodLister(podIndexer)
			c := CapacityViaMetrics(tc.namespace, tc.driverName, podLister)
			actual, err := c.NodeCapacity(tc.node)
			if tc.expectError {
				t.Logf("got error %v", err)
				require.Error(t, err, "NodeCapacity should have failed")
			} else {
				require.NoError(t, err, "NodeCapacity should have succeeded")
				require.Equal(t, tc.expected, actual, "capacity")
			}
		})
	}
}
