/*
Copyright 2020 Intel Corp.

SPDX-License-Identifier: Apache-2.0
*/

package scheduler

import (
	"errors"
	"fmt"
	"io"
	"net/http"

	dto "github.com/prometheus/client_model/go"
	"github.com/prometheus/common/expfmt"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/labels"
	corelistersv1 "k8s.io/client-go/listers/core/v1"
)

type capacityFromMetrics struct {
	namespace  string
	driverName string
	podLister  corelistersv1.PodLister
	client     http.Client
}

func CapacityViaMetrics(namespace, driverName string, podLister corelistersv1.PodLister) Capacity {
	return &capacityFromMetrics{
		namespace:  namespace,
		driverName: driverName,
		podLister:  podLister,
	}
}

// TODO: this needs to be configurable
var pmemCSINode = labels.Set{"app": "pmem-csi-node"}.AsSelector()

// NodeCapacity implements the necessary method for the NodeCapacity interface by
// looking up pods in the namespace which run on the node (usually one)
// and retrieving metrics data from them. The driver name is checked to allow
// more than one driver instance per node (unlikely).
func (c *capacityFromMetrics) NodeCapacity(nodeName string) (int64, error) {
	pods, err := c.podLister.List(pmemCSINode)
	if err != nil {
		return 0, fmt.Errorf("list PMEM-CSI node pods: %v", err)
	}
	for _, pod := range pods {
		if pod.Spec.NodeName != nodeName ||
			pod.Namespace != c.namespace {
			continue
		}
		url := metricsURL(pod)
		if url == "" {
			continue
		}
		capacity, err := c.retrieveMaxVolumeSize(url)
		switch err {
		case wrongPod:
			continue
		case nil:
			return capacity, nil
		default:
			return 0, fmt.Errorf("get metrics from pod %s via %s: %v", pod.Name, url, err)
		}
	}

	// Node not known or no metrics.
	return 0, nil
}

var wrongPod = errors.New("wrong driver pod")

func (c *capacityFromMetrics) retrieveMaxVolumeSize(url string) (int64, error) {
	// TODO (?): negotiate encoding (https://pkg.go.dev/github.com/prometheus/common/expfmt#Negotiate)
	resp, err := c.client.Get(url)
	if err != nil {
		return 0, err
	}
	if resp.StatusCode != http.StatusOK {
		return 0, fmt.Errorf("bad HTTP response status: %s", resp.Status)
	}
	decoder := expfmt.NewDecoder(resp.Body, expfmt.ResponseFormat(resp.Header))
	if err != nil {
		return 0, fmt.Errorf("read response: %v", err)
	}
	var metrics dto.MetricFamily

	for {
		err := decoder.Decode(&metrics)
		if err != nil {
			if errors.Is(err, io.EOF) {
				// If we get here without finding what we look for, we must be talking
				// to the wrong pod and should keep looking.
				return 0, wrongPod
			}
			return 0, fmt.Errorf("decode response: %v", err)
		}
		if metrics.GetName() == "pmem_amount_max_volume_size" {
			for _, metric := range metrics.GetMetric() {
				for _, label := range metric.GetLabel() {
					if label.GetName() == "driver_name" &&
						label.GetValue() != c.driverName {
						return 0, wrongPod
					}
				}
				// "driver_name" was not present yet in PMEM-CSI 0.8.0, so
				// we cannot fail when it is missing.
				gauge := metric.GetGauge()
				if gauge == nil {
					return 0, fmt.Errorf("unexpected metric type for pmem_amount_max_volume_size: %s", metrics.GetType())
				}
				return int64(gauge.GetValue()), nil
			}
		}
	}
}

func metricsURL(pod *corev1.Pod) string {
	for _, container := range pod.Spec.Containers {
		if container.Name == "pmem-driver" {
			for _, containerPort := range container.Ports {
				if containerPort.Name == "metrics" {
					return fmt.Sprintf("http://%s:%d/metrics", pod.Status.PodIP, containerPort.ContainerPort)
				}
			}
			return ""
		}
	}
	return ""
}
