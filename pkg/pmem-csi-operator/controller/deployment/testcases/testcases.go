/*
Copyright 2020 Intel Corporation

SPDX-License-Identifier: Apache-2.0
*/

// Package testcases contains test cases for the operator which can be used both during
// unit and E2E testing.
package testcases

import (
	"fmt"

	api "github.com/intel/pmem-csi/pkg/apis/pmemcsi/v1beta1"
	pmemtls "github.com/intel/pmem-csi/pkg/pmem-csi-operator/pmem-tls"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// UpdateTest defines a starting deployment and a function which will
// change one or more fields in it.
type UpdateTest struct {
	Name       string
	Deployment api.Deployment
	Mutate     func(d *api.Deployment)
}

func UpdateTests() []UpdateTest {
	singleMutators := map[string]func(d *api.Deployment){
		"image": func(d *api.Deployment) {
			d.Spec.Image = "updated-image"
		},
		"pullPolicy": func(d *api.Deployment) {
			d.Spec.PullPolicy = corev1.PullNever
		},
		"provisionerImage": func(d *api.Deployment) {
			d.Spec.ProvisionerImage = "still-no-such-provisioner-image"
		},
		"nodeRegistrarImage": func(d *api.Deployment) {
			d.Spec.NodeRegistrarImage = "still-no-such-registrar-image"
		},
		"controllerDriverResources": func(d *api.Deployment) {
			d.Spec.ControllerDriverResources = &corev1.ResourceRequirements{
				Limits: corev1.ResourceList{
					corev1.ResourceCPU:    resource.MustParse("201m"),
					corev1.ResourceMemory: resource.MustParse("101Mi"),
				},
			}
		},
		"nodeDriverResources": func(d *api.Deployment) {
			d.Spec.NodeDriverResources = &corev1.ResourceRequirements{
				Limits: corev1.ResourceList{
					corev1.ResourceCPU:    resource.MustParse("501m"),
					corev1.ResourceMemory: resource.MustParse("501Mi"),
				},
			}
		},
		"provisionerResources": func(d *api.Deployment) {
			d.Spec.ProvisionerResources = &corev1.ResourceRequirements{
				Limits: corev1.ResourceList{
					corev1.ResourceCPU:    resource.MustParse("101m"),
					corev1.ResourceMemory: resource.MustParse("101Mi"),
				},
			}
		},
		"nodeRegistrarResources": func(d *api.Deployment) {
			d.Spec.NodeRegistrarResources = &corev1.ResourceRequirements{
				Limits: corev1.ResourceList{
					corev1.ResourceCPU:    resource.MustParse("301m"),
					corev1.ResourceMemory: resource.MustParse("301Mi"),
				},
			}
		},
		"TLS": func(d *api.Deployment) {
			SetTLSOrDie(&d.Spec)
		},
		"logLevel": func(d *api.Deployment) {
			d.Spec.LogLevel++
		},
		"nodeSelector": func(d *api.Deployment) {
			d.Spec.NodeSelector = map[string]string{
				"still-no-such-label": "still-no-such-value",
			}
		},
		"pmemPercentage": func(d *api.Deployment) {
			d.Spec.PMEMPercentage++
		},
		"labels": func(d *api.Deployment) {
			if d.Spec.Labels == nil {
				d.Spec.Labels = map[string]string{}
			}
			d.Spec.Labels["foo"] = "bar"
		},
		"kubeletDir": func(d *api.Deployment) {
			d.Spec.KubeletDir = "/foo/bar"
		},
	}

	full := api.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name: "pmem-csi-with-values",
		},
		Spec: api.DeploymentSpec{
			Image:              "base-image",
			PullPolicy:         corev1.PullIfNotPresent,
			ProvisionerImage:   "no-such-provisioner-image",
			NodeRegistrarImage: "no-such-registrar-image",
			DeviceMode:         api.DeviceModeDirect,
			LogLevel:           4,
			NodeSelector: map[string]string{
				"no-such-label": "no-such-value",
			},
			PMEMPercentage: 50,
			Labels: map[string]string{
				"a": "b",
			},
			ControllerDriverResources: &corev1.ResourceRequirements{
				Requests: corev1.ResourceList{
					corev1.ResourceCPU:    resource.MustParse("20m"),
					corev1.ResourceMemory: resource.MustParse("10Mi"),
				},
				Limits: corev1.ResourceList{
					corev1.ResourceCPU:    resource.MustParse("200m"),
					corev1.ResourceMemory: resource.MustParse("100Mi"),
				},
			},
			NodeDriverResources: &corev1.ResourceRequirements{
				Requests: corev1.ResourceList{
					corev1.ResourceCPU:    resource.MustParse("50m"),
					corev1.ResourceMemory: resource.MustParse("50Mi"),
				},
				Limits: corev1.ResourceList{
					corev1.ResourceCPU:    resource.MustParse("500m"),
					corev1.ResourceMemory: resource.MustParse("500Mi"),
				},
			},

			ProvisionerResources: &corev1.ResourceRequirements{
				Requests: corev1.ResourceList{
					corev1.ResourceCPU:    resource.MustParse("10m"),
					corev1.ResourceMemory: resource.MustParse("10Mi"),
				},
				Limits: corev1.ResourceList{
					corev1.ResourceCPU:    resource.MustParse("100m"),
					corev1.ResourceMemory: resource.MustParse("200Mi"),
				},
			},
			NodeRegistrarResources: &corev1.ResourceRequirements{
				Requests: corev1.ResourceList{
					corev1.ResourceCPU:    resource.MustParse("30m"),
					corev1.ResourceMemory: resource.MustParse("30Mi"),
				},
				Limits: corev1.ResourceList{
					corev1.ResourceCPU:    resource.MustParse("300m"),
					corev1.ResourceMemory: resource.MustParse("300Mi"),
				},
			},
		},
	}
	SetTLSOrDie(&full.Spec)

	baseDeployments := map[string]api.Deployment{
		"default deployment": {
			ObjectMeta: metav1.ObjectMeta{
				Name: "pmem-csi-with-defaults",
			},
		},
		"deployment with specific values": full,
	}

	updateAll := func(d *api.Deployment) {
		for _, mutator := range singleMutators {
			mutator(d)
		}
	}

	var tests []UpdateTest

	for baseName, dep := range baseDeployments {
		for mutatorName, mutator := range singleMutators {
			tests = append(tests, UpdateTest{
				Name:       fmt.Sprintf("%s in %s", mutatorName, baseName),
				Deployment: dep,
				Mutate:     mutator,
			})
		}
		tests = append(tests, UpdateTest{
			Name:       fmt.Sprintf("all in %s", baseName),
			Deployment: dep,
			Mutate:     updateAll,
		})
	}

	return tests
}

func SetTLSOrDie(spec *api.DeploymentSpec) {
	err := SetTLS(spec)
	if err != nil {
		panic(err)
	}
}

func SetTLS(spec *api.DeploymentSpec) error {
	ca, err := pmemtls.NewCA(nil, nil)
	if err != nil {
		return fmt.Errorf("instantiate CA: %v", err)
	}

	regKey, err := pmemtls.NewPrivateKey()
	if err != nil {
		return fmt.Errorf("generate a private key: %v", err)
	}
	regCert, err := ca.GenerateCertificate("pmem-registry", regKey.Public())
	if err != nil {
		return fmt.Errorf("sign registry key: %v", err)
	}

	ncKey, err := pmemtls.NewPrivateKey()
	if err != nil {
		return fmt.Errorf("generate a private key: %v", err)
	}
	ncCert, err := ca.GenerateCertificate("pmem-node-controller", ncKey.Public())
	if err != nil {
		return fmt.Errorf("sign node controller key: %v", err)
	}

	spec.CACert = ca.EncodedCertificate()
	spec.RegistryPrivateKey = pmemtls.EncodeKey(regKey)
	spec.RegistryCert = pmemtls.EncodeCert(regCert)
	spec.NodeControllerPrivateKey = pmemtls.EncodeKey(ncKey)
	spec.NodeControllerCert = pmemtls.EncodeCert(ncCert)

	return nil
}
