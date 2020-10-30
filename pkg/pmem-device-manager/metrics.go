/*
Copyright 2020 Intel Corporation.

SPDX-License-Identifier: Apache-2.0
*/

package pmdmanager

import (
	"github.com/prometheus/client_golang/prometheus"
)

var (
	pmemMaxDesc = prometheus.NewDesc(
		"pmem_amount_max_volume_size",
		"The size of the largest PMEM volume that can be created.",
		nil, nil,
	)
	pmemAvailableDesc = prometheus.NewDesc(
		"pmem_amount_available",
		"Remaining amount of PMEM on the host that can be used for new volumes.",
		nil, nil,
	)
	pmemManagedDesc = prometheus.NewDesc(
		"pmem_amount_managed",
		"Amount of PMEM on the host that is managed by PMEM-CSI.",
		nil, nil,
	)
	pmemTotalDesc = prometheus.NewDesc(
		"pmem_amount_total",
		"Total amount of PMEM on the host.",
		nil, nil,
	)
)

// NodeLabel is a label used for Prometheus which identifies the
// node that the controller talks to.
const NodeLabel = "node"

// CapacityCollector is a wrapper around a PMEM device manager which
// takes GetCapacity values and turns them into metrics data.
type CapacityCollector struct {
	PmemDeviceCapacity
}

// MustRegister adds the collector to the registry, using labels to tag each sample with node and driver name.
func (cc CapacityCollector) MustRegister(reg prometheus.Registerer, nodeName, driverName string) {
	labels := prometheus.Labels{
		NodeLabel:     nodeName,
		"driver_name": driverName, // same label name as in csi-lib-utils for CSI gRPC calls
	}
	prometheus.WrapRegistererWith(labels, reg).MustRegister(cc)
}

// Describe implements prometheus.Collector.Describe.
func (cc CapacityCollector) Describe(ch chan<- *prometheus.Desc) {
	prometheus.DescribeByCollect(cc, ch)
}

// Collect implements prometheus.Collector.Collect.
func (cc CapacityCollector) Collect(ch chan<- prometheus.Metric) {
	capacity, err := cc.GetCapacity()
	if err != nil {
		return
	}
	ch <- prometheus.MustNewConstMetric(
		pmemMaxDesc,
		prometheus.GaugeValue,
		float64(capacity.MaxVolumeSize),
	)
	ch <- prometheus.MustNewConstMetric(
		pmemAvailableDesc,
		prometheus.GaugeValue,
		float64(capacity.Available),
	)
	ch <- prometheus.MustNewConstMetric(
		pmemManagedDesc,
		prometheus.GaugeValue,
		float64(capacity.Managed),
	)
	ch <- prometheus.MustNewConstMetric(
		pmemTotalDesc,
		prometheus.GaugeValue,
		float64(capacity.Total),
	)
}

var _ prometheus.Collector = CapacityCollector{}
