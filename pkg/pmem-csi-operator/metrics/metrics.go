/*
Copyright 2021 The Kubernetes Authors.

SPDX-License-Identifier: Apache-2.0
*/

package metrics

import (
	"strings"

	"github.com/prometheus/client_golang/prometheus"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/metrics"
)

const (
	// PmemCSIDeploymentSubsystemKey represents the key used for
	// PMEM-CSI deployment metrics sub-system.
	PmemCSIDeploymentSubsystemKey = "pmem_csi_deployment"
)

var (
	// Reconcile creates new prometheus metrics counter
	// that gets incremented on each reconcile loop
	// of a PmemCSIDeployment CR, with information: {"name", "uid"}.
	Reconcile = prometheus.NewCounterVec(prometheus.CounterOpts{
		Subsystem: PmemCSIDeploymentSubsystemKey,
		Name:      "reconcile",
		Help:      "Number of reconcile loops gone through by a PmemCSIDeployment CR.",
	}, []string{"name", "uid"})

	// SubResourceCreatedAt creates new prometheus metrics for
	// a sub resource deployed for a PmemCSIDeployment,
	// with information: {"name", "namespace", "group", "version", "kind", "uid", "ownedBy"}
	SubResourceCreatedAt = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Subsystem: PmemCSIDeploymentSubsystemKey,
		Name:      "sub_resource_created_at",
		Help:      "Timestamp at which a sub resource was created.",
	}, []string{"name", "namespace", "group", "version", "kind", "uid", "ownedBy"})

	// SubResourceUpdatedAt creates new prometheus metrics for
	// a sub resource redeployed for a PmemCSIDeployment,
	// with information: {"name", "namespace", "group", "version", "kind", "uid", "ownedBy"}
	SubResourceUpdatedAt = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Subsystem: PmemCSIDeploymentSubsystemKey,
		Name:      "sub_resource_updated_at",
		Help:      "Timestamp at which a sub resource was update.",
	}, []string{"name", "namespace", "group", "version", "kind", "uid", "ownedBy"})
)

func RegisterMetrics() {
	metrics.Registry.MustRegister(
		Reconcile,
		SubResourceCreatedAt,
		SubResourceUpdatedAt,
	)
}

func SetSubResourceCreateMetric(obj client.Object) error {
	if obj == nil {
		return nil
	}
	return setGauge(SubResourceCreatedAt, GetSubResourceLabels(obj))
}

func SetSubResourceUpdateMetric(obj client.Object) error {
	if obj == nil {
		return nil
	}
	return setGauge(SubResourceUpdatedAt, GetSubResourceLabels(obj))
}

func SetReconcileMetrics(name, uid string) error {
	return setCounter(Reconcile, map[string]string{
		"name": name,
		"uid":  uid,
	})
}

func GetSubResourceLabels(obj client.Object) map[string]string {
	owners := []string{}
	for _, ref := range obj.GetOwnerReferences() {
		owners = append(owners, string(ref.UID))
	}
	return map[string]string{
		"name":      obj.GetName(),
		"namespace": obj.GetNamespace(),
		"group":     obj.GetObjectKind().GroupVersionKind().Group,
		"version":   obj.GetObjectKind().GroupVersionKind().Version,
		"kind":      obj.GetObjectKind().GroupVersionKind().Kind,
		"uid":       string(obj.GetUID()),
		"ownedBy":   strings.Join(owners, ","),
	}
}

func setGauge(gauge *prometheus.GaugeVec, labels map[string]string) error {
	m, err := gauge.GetMetricWith(labels)
	if err != nil {
		return err
	}
	m.SetToCurrentTime()
	return nil
}

func setCounter(counter *prometheus.CounterVec, labels map[string]string) error {
	m, err := counter.GetMetricWith(labels)
	if err != nil {
		return err
	}

	m.Inc()
	return nil
}
