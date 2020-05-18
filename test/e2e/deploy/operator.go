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
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/kubernetes/test/e2e/framework"

	"github.com/intel/pmem-csi/pkg/apis"
	api "github.com/intel/pmem-csi/pkg/apis/pmemcsi/v1alpha1"

	"github.com/onsi/gomega"
)

var (
	DeploymentResource = schema.GroupVersionResource{
		Group:    api.SchemeGroupVersion.Group,
		Version:  api.SchemeGroupVersion.Version,
		Resource: "deployments",
	}
	Scheme = runtime.NewScheme()
)

func init() {
	api.SchemeBuilder.Register(&api.Deployment{}, &api.DeploymentList{})
	err := apis.AddToScheme(Scheme)
	if err != nil {
		panic(err)
	}
}

func DeploymentToUnstructured(in *api.Deployment) *unstructured.Unstructured {
	if in == nil {
		return nil
	}
	var out unstructured.Unstructured
	err := Scheme.Convert(in, &out, nil)
	framework.ExpectNoError(err, "convert to unstructured deployment")

	// Ensure that type info is set. It's required when passing
	// the unstructured object to a dynamic client.
	out.SetGroupVersionKind(schema.GroupVersionKind{
		Group:   "pmem-csi.intel.com",
		Version: "v1alpha1",
		Kind:    "Deployment",
	})
	return &out
}

func DeploymentFromUnstructured(in *unstructured.Unstructured) *api.Deployment {
	if in == nil {
		return nil
	}
	var out api.Deployment
	err := Scheme.Convert(in, &out, nil)
	framework.ExpectNoError(err, "convert from unstructured deployment")
	return &out
}

func CreateDeploymentCR(f *framework.Framework, dep api.Deployment) api.Deployment {
	in := DeploymentToUnstructured(&dep)
	var out *unstructured.Unstructured

	gomega.Eventually(func() error {
		var err error
		out, err = f.DynamicClient.Resource(DeploymentResource).Create(context.Background(), in, metav1.CreateOptions{})
		LogError(err, "create deployment error: %v, will retry...", err)
		return err
	}, "3m", "1s").Should(gomega.BeNil(), "create deployment %q", dep.Name)
	framework.Logf("Created deployment %q", dep.Name)
	return *DeploymentFromUnstructured(out)
}

func EnsureDeploymentCR(f *framework.Framework, dep api.Deployment) api.Deployment {
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
		// On Kubernetes 1.18, we have seen test failures
		// because the CR and it's services weren't getting
		// deleted. The reason were the endpoints which were
		// owned by the services: somehow they had no
		// deletionTimestamp and also weren't
		// garbage-collected for extended periods of time (>5
		// min, potentially longer), but prevented deleting
		// the services due to foreground deletion.
		//
		// As a quick hack, we delete the services ourselves. For this
		// we need to find all services which are owned by the
		// deployment.
		dep, err := f.DynamicClient.Resource(DeploymentResource).Get(context.Background(), name, metav1.GetOptions{})
		if err != nil {
			return false
		}
		services, err := f.ClientSet.CoreV1().Services("").List(context.Background(), metav1.ListOptions{})
		if err != nil {
			return false
		}
		for _, service := range services.Items {
			owned := false
			for _, owner := range service.OwnerReferences {
				if owner.UID == dep.GetUID() {
					owned = true
					break
				}
			}
			if owned {
				// Before we can delete the services, we have to remove the "foregroundDeletion" finalizer.
				for i, finalizer := range service.Finalizers {
					if finalizer == "foregroundDeletion" {
						service.Finalizers = append(service.Finalizers[:i], service.Finalizers[i+1:]...)
						_, err := f.ClientSet.CoreV1().Services(service.Namespace).Update(context.Background(), &service, metav1.UpdateOptions{})
						framework.Logf("Remove deployment service %s/%s finalizer: %v", service.Namespace, service.Name, err)
						break
					}
				}

				// Now deleted the service.
				if err := f.ClientSet.CoreV1().Services(service.Namespace).Delete(context.Background(), service.Name, metav1.DeleteOptions{}); err == nil {
					framework.Logf("Deleted deployment service %s/%s", service.Namespace, service.Name)
				}

				// Just to be thorough, we also delete the corresponding endpoint, if there still is one.
				// It has the same name as the service.
				if err := f.ClientSet.CoreV1().Endpoints(service.Namespace).Delete(context.Background(), service.Name, metav1.DeleteOptions{}); err == nil {
					framework.Logf("Deleted deployment endpoint %s/%s", service.Namespace, service.Name)
				}
			}
		}
		return false
	}, "3m", "1s").Should(gomega.BeTrue(), "delete deployment %q", name)
	framework.Logf("Deleted deployment %q", name)
}

func UpdateDeploymentCR(f *framework.Framework, dep api.Deployment) api.Deployment {
	in := DeploymentToUnstructured(&dep)
	var out *unstructured.Unstructured

	gomega.Eventually(func() error {
		var err error
		out, err = f.DynamicClient.Resource(DeploymentResource).Update(context.Background(), in, metav1.UpdateOptions{})
		LogError(err, "update deployment error: %v, will retry...", err)
		return err
	}, "3m", "1s").Should(gomega.BeNil(), "update deployment: %q", dep.Name)

	framework.Logf("Updated deployment %q", dep.Name)
	return *DeploymentFromUnstructured(out)
}

func GetDeploymentCR(f *framework.Framework, name string) api.Deployment {
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