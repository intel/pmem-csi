/*
Copyright 2020 Intel Corporation.

SPDX-License-Identifier: Apache-2.0
*/

package deploy

import (
	"context"

	apierrs "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/kubernetes/test/e2e/framework"

	"github.com/intel/pmem-csi/pkg/apis"
	api "github.com/intel/pmem-csi/pkg/apis/pmemcsi/v1beta1"
	cm "github.com/prometheus/client_model/go"

	"github.com/onsi/gomega"
)

var (
	DeploymentResource = schema.GroupVersionResource{
		Group:    api.SchemeGroupVersion.Group,
		Version:  api.SchemeGroupVersion.Version,
		Resource: "pmemcsideployments",
	}
	Scheme = runtime.NewScheme()
)

func init() {
	api.SchemeBuilder.Register(&api.PmemCSIDeployment{}, &api.PmemCSIDeploymentList{})
	err := apis.AddToScheme(Scheme)
	if err != nil {
		panic(err)
	}
}

func deploymentToUnstructured(in interface{}, gvk schema.GroupVersionKind) *unstructured.Unstructured {
	if in == nil {
		return nil
	}
	var out unstructured.Unstructured
	err := Scheme.Convert(in, &out, nil)
	framework.ExpectNoError(err, "convert to unstructured deployment")

	// Ensure that type info is set. It's required when passing
	// the unstructured object to a dynamic client.
	out.SetGroupVersionKind(gvk)
	return &out
}

func DeploymentToUnstructured(in *api.PmemCSIDeployment) *unstructured.Unstructured {
	return deploymentToUnstructured(in, schema.GroupVersionKind{
		Group:   api.SchemeGroupVersion.Group,
		Version: api.SchemeGroupVersion.Version,
		Kind:    "PmemCSIDeployment",
	})
}

func deploymentFromUnstructured(in *unstructured.Unstructured, out interface{}) {
	err := Scheme.Convert(in, out, nil)
	framework.ExpectNoError(err, "convert from unstructured deployment")
}

func DeploymentFromUnstructured(in *unstructured.Unstructured) *api.PmemCSIDeployment {
	if in == nil {
		return nil
	}
	var out api.PmemCSIDeployment
	deploymentFromUnstructured(in, &out)
	return &out
}

func createDeploymentCR(f *framework.Framework, dep *unstructured.Unstructured, res schema.GroupVersionResource) *unstructured.Unstructured {
	var out *unstructured.Unstructured

	gomega.Eventually(func() error {
		var err error
		out, err = f.DynamicClient.Resource(res).Create(context.Background(), dep, metav1.CreateOptions{})
		LogError(err, "create deployment error: %v, will retry...", err)
		return err
	}, "3m", "1s").Should(gomega.BeNil(), "create deployment %q", dep.GetName())
	return out
}

func CreateDeploymentCR(f *framework.Framework, dep api.PmemCSIDeployment) api.PmemCSIDeployment {
	in := DeploymentToUnstructured(&dep)
	out := createDeploymentCR(f, in, DeploymentResource)
	framework.Logf("Created deployment %q = (%+v)", dep.Name, out)
	return *DeploymentFromUnstructured(out)
}

func EnsureDeploymentCR(f *framework.Framework, dep api.PmemCSIDeployment) api.PmemCSIDeployment {
	var out *unstructured.Unstructured
	gomega.Eventually(func() error {
		existingDep, err := f.DynamicClient.Resource(DeploymentResource).Get(context.Background(), dep.Name, metav1.GetOptions{})
		if err == nil {
			dep.ResourceVersion = existingDep.GetResourceVersion()
			in := DeploymentToUnstructured(&dep)
			out, err = f.DynamicClient.Resource(DeploymentResource).Update(context.Background(), in, metav1.UpdateOptions{})
			LogError(err, "update deployment error: %v, will retry...", err)
			return err
		}
		if apierrs.IsNotFound(err) {
			in := DeploymentToUnstructured(&dep)
			out, err = f.DynamicClient.Resource(DeploymentResource).Create(context.Background(), in, metav1.CreateOptions{})
			LogError(err, "create deployment error: %v, will retry...", err)
			return err
		}
		return err
	}, "3m", "1s").Should(gomega.BeNil(), "create deployment %q", dep.Name)
	framework.Logf("Created deployment %q", dep.Name)
	return *DeploymentFromUnstructured(out)
}

func DeleteDeploymentCR(f *framework.Framework, name string) {
	// Delete all
	deletionPolicy := metav1.DeletePropagationForeground
	gomega.Eventually(func() bool {
		err := f.DynamicClient.Resource(DeploymentResource).Delete(context.Background(), name, metav1.DeleteOptions{
			PropagationPolicy: &deletionPolicy,
		})
		if err != nil && apierrs.IsNotFound(err) {
			return true
		}
		LogError(err, "delete deployment error: %v, will retry...", err)
		return false
	}, "3m", "1s").Should(gomega.BeTrue(), "delete deployment %q", name)
	framework.Logf("Deleted deployment %q", name)
}

func UpdateDeploymentCR(f *framework.Framework, dep api.PmemCSIDeployment) api.PmemCSIDeployment {
	var out *unstructured.Unstructured

	gomega.Eventually(func() error {
		var err error
		in, err := f.DynamicClient.Resource(DeploymentResource).Get(context.Background(), dep.Name, metav1.GetOptions{})
		d := DeploymentFromUnstructured(in)
		d.Spec = dep.Spec
		in = DeploymentToUnstructured(d)

		out, err = f.DynamicClient.Resource(DeploymentResource).Update(context.Background(), in, metav1.UpdateOptions{})
		LogError(err, "update deployment error: %v, will retry...", err)
		return err
	}, "3m", "1s").Should(gomega.BeNil(), "update deployment: %q", dep.Name)

	framework.Logf("Updated deployment %q", dep.Name)
	return *DeploymentFromUnstructured(out)
}

func GetDeploymentCR(f *framework.Framework, name string) api.PmemCSIDeployment {
	var out *unstructured.Unstructured
	gomega.Eventually(func() error {
		var err error
		out, err = f.DynamicClient.Resource(DeploymentResource).Get(context.Background(), name, metav1.GetOptions{})
		LogError(err, "get deployment error: %v, will retry...", err)
		return err
	}, "3m", "1s").Should(gomega.BeNil(), "get deployment")
	return *DeploymentFromUnstructured(out)
}

// LogError will log the message only if err is non-nil. The error
// must also be part of the arguments if it is to be included in the
// message.
func LogError(err error, format string, args ...interface{}) {
	if err != nil {
		framework.Logf(format, args...)
	}
}

func GetOperatorMetricsURL(ctx context.Context, cluster *Cluster, d *Deployment) (string, error) {
	return GetMetricsURL(ctx, cluster, d.Namespace, labels.Set{
		"pmem-csi.intel.com/deployment": d.Label(),
		"app":                           "pmem-csi-operator",
	})
}

func GetOperatorMetrics(ctx context.Context, cluster *Cluster, d *Deployment) (map[string]*cm.MetricFamily, error) {
	url, err := GetOperatorMetricsURL(ctx, cluster, d)
	if err != nil {
		return nil, err
	}
	return GetMetrics(ctx, cluster, url)
}
