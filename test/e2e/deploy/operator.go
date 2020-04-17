/*
Copyright 2020 Intel Corporation.

SPDX-License-Identifier: Apache-2.0
*/

package deploy

import (
	"context"
	"fmt"

	apierrs "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/kubernetes/test/e2e/framework"

	"github.com/intel/pmem-csi/pkg/apis"
	api "github.com/intel/pmem-csi/pkg/apis/pmemcsi/v1alpha1"

	"github.com/onsi/ginkgo"
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

func CreateDeploymentCR(f *framework.Framework, dep *unstructured.Unstructured) *unstructured.Unstructured {
	var outDep *unstructured.Unstructured
	metadata := dep.Object["metadata"].(map[string]interface{})
	depName := metadata["name"].(string)
	gomega.Eventually(func() error {
		var err error
		outDep, err = f.DynamicClient.Resource(DeploymentResource).Create(context.Background(), dep, metav1.CreateOptions{})
		LogError(err, "create deployment error: %v, will retry...", err)
		return err
	}, "3m", "10s").Should(gomega.BeNil(), "create deployment %q", depName)
	ginkgo.By(fmt.Sprintf("Created deployment %q", depName))
	return outDep
}

func DeleteDeploymentCR(f *framework.Framework, name string) {
	gomega.Eventually(func() error {
		err := f.DynamicClient.Resource(DeploymentResource).Delete(context.Background(), name, metav1.DeleteOptions{})
		if err != nil && apierrs.IsNotFound(err) {
			return nil
		}
		LogError(err, "delete deployment error: %v, will retry...", err)
		return err
	}, "3m", "10s").Should(gomega.BeNil(), "delete deployment %q", name)
	ginkgo.By(fmt.Sprintf("Deleted deployment %q", name))
}

func UpdateDeploymentCR(f *framework.Framework, dep *unstructured.Unstructured) *unstructured.Unstructured {
	var outDep *unstructured.Unstructured
	metadata := dep.Object["metadata"].(map[string]interface{})
	depName := metadata["name"].(string)

	gomega.Eventually(func() error {
		var err error
		outDep, err = f.DynamicClient.Resource(DeploymentResource).Update(context.Background(), dep, metav1.UpdateOptions{})
		LogError(err, "update deployment error: %v, will retry...", err)
		return err
	}, "3m", "10s").Should(gomega.BeNil(), "update deployment: %q", depName)

	ginkgo.By(fmt.Sprintf("Updated deployment %q", depName))
	return outDep
}

func GetDeploymentCR(f *framework.Framework, name string) *unstructured.Unstructured {
	var outDep *unstructured.Unstructured
	gomega.Eventually(func() error {
		var err error
		outDep, err = f.DynamicClient.Resource(DeploymentResource).Get(context.Background(), name, metav1.GetOptions{})
		LogError(err, "get deployment error: %v, will retry...", err)
		return err
	}, "3m", "10s").Should(gomega.BeNil(), "get deployment")

	return outDep
}

// LogError will log the message only if err is non-nil. The error
// must also be part of the arguments if it is to be included in the
// message.
func LogError(err error, format string, args ...interface{}) {
	if err != nil {
		framework.Logf(format, args...)
	}
}
