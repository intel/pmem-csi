/*
Copyright 2020 The Kubernetes Authors.

SPDX-License-Identifier: Apache-2.0
*/

package deployment

import (
	"fmt"
	"time"

	pmemcsiv1alpha1 "github.com/intel/pmem-csi/pkg/apis/pmemcsi/v1alpha1"
	apiextensions "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	"k8s.io/apiextensions-apiserver/pkg/client/clientset/clientset"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/rest"
	"k8s.io/klog"
)

func EnsureCRDInstalled(config *rest.Config) error {
	aeClientset, err := clientset.NewForConfig(config)
	if err != nil {
		return err
	}
	crd := GetDeploymentCRD()

	if _, err := aeClientset.ApiextensionsV1().CustomResourceDefinitions().Create(crd); err != nil {
		if errors.IsAlreadyExists(err) {
			return nil
		}
		return err
	} else {
		/* Wait till the CRD is visible in API server */
		retryCount := 6
		retryDelay := 10 * time.Second

		for retryCount > 0 {
			_, err := aeClientset.ApiextensionsV1().CustomResourceDefinitions().Get(
				"deployments.pmem-csi.intel.com",
				metav1.GetOptions{
					TypeMeta: metav1.TypeMeta{
						Kind:       "CustomResourceDefinition",
						APIVersion: "apiextensions.k8s.io/v1",
					},
				})
			if err != nil {
				klog.Warningf("Failed to fetch Deployment CRD: %v. Will retry after %d Seconds", err, retryDelay)
				retryCount--
				time.Sleep(retryDelay)
			} else {
				klog.Info("CRD registered with API Server")
				break
			}
		}

		if retryCount == 0 {
			return fmt.Errorf("Failed to retrieve Deployment CRD from API server")
		}
	}
	return nil
}

// DeleteCRD deletes the PMEM-CSI Deployment CRD.
// Supposed to be called while existing the operator
func DeleteCRD(config *rest.Config) error {
	aeClientset, err := clientset.NewForConfig(config)
	if err != nil {
		return err
	}
	delOptions := &metav1.DeleteOptions{
		TypeMeta: metav1.TypeMeta{
			APIVersion: "apiextensions.k8s.io/v1",
			Kind:       "CustomResourceDefinition",
		},
	}

	if err := aeClientset.ApiextensionsV1().CustomResourceDefinitions().Delete("deployments.pmem-csi.intel.com", delOptions); err != nil {
		if errors.IsNotFound(err) {
			return nil
		}
		return err
	}
	return nil
}

func GetDeploymentCRD() *apiextensions.CustomResourceDefinition {
	return &apiextensions.CustomResourceDefinition{
		TypeMeta: metav1.TypeMeta{
			APIVersion: "apiextensions.k8s.io/v1",
			Kind:       "CustomResourceDefinition",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name: "deployments.pmem-csi.intel.com",
		},
		Spec: apiextensions.CustomResourceDefinitionSpec{
			Group: "pmem-csi.intel.com",
			Versions: []apiextensions.CustomResourceDefinitionVersion{
				{
					Name:    "v1alpha1",
					Served:  true,
					Storage: true,
					Subresources: &apiextensions.CustomResourceSubresources{
						Status: &apiextensions.CustomResourceSubresourceStatus{},
					},
					Schema: &apiextensions.CustomResourceValidation{
						OpenAPIV3Schema: pmemcsiv1alpha1.GetDeploymentCRDSchema(),
					},
				},
			},

			Names: apiextensions.CustomResourceDefinitionNames{
				Singular: "deployment",
				Plural:   "deployments",
				Kind:     "Deployment",
				ListKind: "DeploymentList",
			},
			// PMEM-CSI Deployment is a cluster scoped resource
			Scope: apiextensions.ClusterScoped,
		},
	}
}
