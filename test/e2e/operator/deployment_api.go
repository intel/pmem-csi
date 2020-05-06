/*
Copyright 2019 Intel Corporation.

SPDX-License-Identifier: Apache-2.0
*/

package operator

import (
	"context"
	"fmt"
	"strings"
	"time"

	api "github.com/intel/pmem-csi/pkg/apis/pmemcsi/v1alpha1"
	pmemtls "github.com/intel/pmem-csi/pkg/pmem-csi-operator/pmem-tls"
	"github.com/intel/pmem-csi/test/e2e/deploy"
	"github.com/intel/pmem-csi/test/e2e/operator/validate"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/kubernetes/test/e2e/framework"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
)

var _ = deploy.DescribeForSome("API", func(d *deploy.Deployment) bool {
	// Run these tests for all bare operator deployments, i.e.
	// those which did not already install the driver.
	return d.HasOperator && !d.HasDriver
}, func(d *deploy.Deployment) {
	var (
		c      *deploy.Cluster
		ctx    context.Context
		cancel func()
	)

	f := framework.NewDefaultFramework("operator")
	// test/e2e/deploy.go is using default namespace for deploying operator.
	// So we could skip default namespace creation/deletion steps
	f.SkipNamespaceCreation = true

	BeforeEach(func() {
		Expect(f).ShouldNot(BeNil(), "framework init")
		cluster, err := deploy.NewCluster(f.ClientSet, f.DynamicClient)
		Expect(err).ShouldNot(HaveOccurred(), "new cluster")
		c = cluster

		// All tests are expected to complete in 5 minutes.
		// We need to set up the global variables indirectly to avoid a watning about cancel not being called.
		ctx2, cancel2 := context.WithTimeout(context.Background(), 5*time.Minute)
		ctx, cancel = ctx2, cancel2
	})

	AfterEach(func() {
		cancel()
	})

	Context("deployment", func() {
		// We use intentionally use this non-existing driver image
		// because these tests do not actually need a running driver.
		dummyImage := "unexisting/pmem-csi-driver"

		tests := map[string]*unstructured.Unstructured{
			"with defaults": &unstructured.Unstructured{
				Object: map[string]interface{}{
					"apiVersion": api.SchemeGroupVersion.String(),
					"kind":       "Deployment",
					"metadata": map[string]interface{}{
						"name": "test-deployment-with-defaults",
					},
					"spec": map[string]interface{}{
						"image": dummyImage,
					},
				},
			},
			"with explicit values": &unstructured.Unstructured{
				Object: map[string]interface{}{
					"apiVersion": api.SchemeGroupVersion.String(),
					"kind":       "Deployment",
					"metadata": map[string]interface{}{
						"name": "test-deployment-with-explicit",
					},
					"spec": map[string]interface{}{
						"driverName":      "test-csi-driver",
						"deviceMode":      "direct",
						"imagePullPolicy": "Never",
						"image":           dummyImage,
						"controllerResources": map[string]interface{}{
							"limits": map[string]interface{}{
								"cpu":    "200m",
								"memory": "100Mi",
							},
						},
						"nodeResources": map[string]interface{}{
							"limits": map[string]interface{}{
								"cpu":    "500m",
								"memory": "500Mi",
							},
						},
					},
				},
			},
		}

		for name, dep := range tests {
			It(name, func() {
				deployment, err := toDeployment(dep)
				Expect(err).ShouldNot(HaveOccurred(), "unstructured to deployment conversion")

				deploy.CreateDeploymentCR(f, dep)
				defer deploy.DeleteDeploymentCR(f, deployment.Name)
				Expect(validate.DriverDeployment(ctx, f, d, *deployment)).Should(BeNil(), "validate driver")
			})
		}

		It("driver image shall default to operator image", func() {
			dep := &unstructured.Unstructured{
				Object: map[string]interface{}{
					"apiVersion": api.SchemeGroupVersion.String(),
					"kind":       "Deployment",
					"metadata": map[string]interface{}{
						"name": "test-deployment-driver-image",
					},
					"spec": map[string]interface{}{
						// NOTE(avalluri): we do not use lvm mode so that
						// running this test does not pollute the PMEM space
						"deviceMode": "direct",
						"driverName": "test-driver-image",
					},
				},
			}

			deployment, err := toDeployment(dep)
			Expect(err).ShouldNot(HaveOccurred(), "unstructured to deployment conversion")

			deploy.CreateDeploymentCR(f, dep)
			defer deploy.DeleteDeploymentCR(f, deployment.Name)

			operatorPod, err := findOperatorPod(c, d)
			Expect(err).ShouldNot(HaveOccurred(), "find operator deployment")

			// operator image should be the driver image
			deployment.Spec.Image = operatorPod.Spec.Containers[0].Image
			Expect(validate.DriverDeployment(ctx, f, d, *deployment)).Should(BeNil(), "validate driver")
		})

		It("shall be able to edit running deployment", func() {
			dep := &unstructured.Unstructured{
				Object: map[string]interface{}{
					"apiVersion": api.SchemeGroupVersion.String(),
					"kind":       "Deployment",
					"metadata": map[string]interface{}{
						"name": "test-deployment-update",
					},
					"spec": map[string]interface{}{
						"driverName": "update-deployment.test.com",
						"image":      dummyImage,
					},
				},
			}

			deployment, err := toDeployment(dep)
			Expect(err).ShouldNot(HaveOccurred(), "unstructured to deployment conversion")

			deploy.CreateDeploymentCR(f, dep)
			defer deploy.DeleteDeploymentCR(f, deployment.Name)
			Expect(validate.DriverDeployment(ctx, f, d, *deployment)).Should(BeNil(), "validate driver before editing")

			// prepare custom certificates
			ca, err := pmemtls.NewCA(nil, nil)
			Expect(err).Should(BeNil(), "failed to instantiate CA")

			regKey, err := pmemtls.NewPrivateKey()
			Expect(err).Should(BeNil(), "failed to generate a private key: %v", err)
			regCert, err := ca.GenerateCertificate("pmem-registry", regKey.Public())
			Expect(err).Should(BeNil(), "failed to sign registry key")

			ncKey, err := pmemtls.NewPrivateKey()
			Expect(err).Should(BeNil(), "failed to generate a private key: %v", err)
			ncCert, err := ca.GenerateCertificate("pmem-node-controller", ncKey.Public())
			Expect(err).Should(BeNil(), "failed to sign node controller key")

			dep = deploy.GetDeploymentCR(f, deployment.Name)

			/* Update fields */
			spec := dep.Object["spec"].(map[string]interface{})
			spec["logLevel"] = api.DefaultLogLevel + 1
			spec["image"] = "test-driver-image"
			spec["imagePullPolicy"] = "Never"
			spec["provisionerImage"] = "test-provisioner"
			spec["controllerResources"] = map[string]interface{}{
				"limits": map[string]interface{}{
					"cpu":    "150m",
					"memory": "1Mi",
				},
			}
			spec["nodeResources"] = map[string]interface{}{
				"limits": map[string]interface{}{
					"cpu":    "350m",
					"memory": "2Mi",
				},
			}
			spec["caCert"] = ca.EncodedCertificate()
			spec["registryKey"] = pmemtls.EncodeKey(regKey)
			spec["registryCert"] = pmemtls.EncodeCert(regCert)
			spec["nodeControllerKey"] = pmemtls.EncodeKey(ncKey)
			spec["nodeControllerCert"] = pmemtls.EncodeCert(ncCert)

			deployment, err = toDeployment(dep)
			Expect(err).ShouldNot(HaveOccurred(), "unstructured to deployment conversion")

			ss, err := f.ClientSet.AppsV1().StatefulSets(d.Namespace).Get(context.Background(), deployment.Name+"-controller", metav1.GetOptions{})
			Expect(err).Should(BeNil(), "existence of controller statefulset")
			ssVersion := ss.GetResourceVersion()

			ds, err := f.ClientSet.AppsV1().DaemonSets(d.Namespace).Get(context.Background(), deployment.Name+"-node", metav1.GetOptions{})
			Expect(err).Should(BeNil(), "existence of node daemonset")
			dsVersion := ds.GetResourceVersion()

			registrySecret, err := f.ClientSet.CoreV1().Secrets(d.Namespace).Get(context.Background(), deployment.Name+"-registry-secrets", metav1.GetOptions{})
			Expect(err).Should(BeNil(), "existence of registry secret")
			registrySecretVersion := registrySecret.GetResourceVersion()

			nodeSecret, err := f.ClientSet.CoreV1().Secrets(d.Namespace).Get(context.Background(), deployment.Name+"-node-secrets", metav1.GetOptions{})
			Expect(err).Should(BeNil(), "existence of node secret")
			nodeSecretVersion := nodeSecret.GetResourceVersion()

			deploy.UpdateDeploymentCR(f, dep)

			// Wait till the sub-resources get updated
			// As a interim solution we are depending on subresoure(daemon set, stateful set, secrets)
			// versions to make sure the resource got updated. Instead, operator should update
			// deployment status with appropriate events/condition messages.
			Eventually(func() bool {
				ss, err := f.ClientSet.AppsV1().StatefulSets(d.Namespace).Get(context.Background(), deployment.Name+"-controller", metav1.GetOptions{})
				if err != nil {
					framework.Logf("Get stateful set error: %v", err)
					return false
				}
				ds, err := f.ClientSet.AppsV1().DaemonSets(d.Namespace).Get(context.Background(), deployment.Name+"-node", metav1.GetOptions{})
				if err != nil {
					framework.Logf("Get daemon set error: %v", err)
					return false
				}

				registrySecret, err := f.ClientSet.CoreV1().Secrets(d.Namespace).Get(context.Background(), deployment.Name+"-registry-secrets", metav1.GetOptions{})
				Expect(err).Should(BeNil(), "update check of registry secret")

				nodeSecret, err := f.ClientSet.CoreV1().Secrets(d.Namespace).Get(context.Background(), deployment.Name+"-node-secrets", metav1.GetOptions{})
				Expect(err).Should(BeNil(), "update check of node secret")

				return ss.GetResourceVersion() != ssVersion &&
					ds.GetResourceVersion() != dsVersion &&
					registrySecret.GetResourceVersion() != registrySecretVersion &&
					nodeSecret.GetResourceVersion() != nodeSecretVersion
			}, "3m", "1s").Should(BeTrue(), "expected both daemonset and stateupset get updated")

			Expect(validate.DriverDeployment(ctx, f, d, *deployment)).Should(BeNil(), "validate driver after editing")
		})

		It("shall not allow to change device manager of a running deployment", func() {
			oldMode := api.DeviceModeDirect
			newMode := api.DeviceModeLVM
			dep := &unstructured.Unstructured{
				Object: map[string]interface{}{
					"apiVersion": api.SchemeGroupVersion.String(),
					"kind":       "Deployment",
					"metadata": map[string]interface{}{
						"name": "test-update-driver-mode",
					},
					"spec": map[string]interface{}{
						"driverName": "update-driver-mode.test.com",
						"deviceMode": oldMode,
						"image":      dummyImage,
					},
				},
			}

			deployment, err := toDeployment(dep)
			Expect(err).ShouldNot(HaveOccurred(), "unstructured to deployment conversion")

			deploy.CreateDeploymentCR(f, dep)
			defer deploy.DeleteDeploymentCR(f, deployment.Name)
			Expect(validate.DriverDeployment(ctx, f, d, *deployment)).Should(BeNil(), "validate driver")

			dep = deploy.GetDeploymentCR(f, deployment.Name)

			/* Update fields */
			spec := dep.Object["spec"].(map[string]interface{})
			spec["deviceMode"] = newMode

			deployment, err = toDeployment(dep)
			Expect(err).ShouldNot(HaveOccurred(), "unstructured to deployment conversion")

			deploy.UpdateDeploymentCR(f, dep)

			Eventually(func() bool {
				updatedDep := deploy.GetDeploymentCR(f, deployment.Name)
				spec := updatedDep.Object["spec"].(map[string]interface{})
				mode := spec["deviceMode"].(string)
				return mode == string(oldMode)
			}, "3m", "2s").Should(BeTrue(), "device manager should not be updated")

			// ensure that the driver is still using the old device manager
			ds, err := f.ClientSet.AppsV1().DaemonSets(d.Namespace).Get(context.Background(), deployment.Name+"-node", metav1.GetOptions{})
			Expect(err).ShouldNot(HaveOccurred(), "daemon set should exists")
			for _, c := range ds.Spec.Template.Spec.Containers {
				if c.Name == "pmem-driver" {
					Expect(c.Command).Should(ContainElement("-deviceManager="+string(oldMode)), "mismatched device manager")
				}
			}
		})

		It("shall not allow to change pmem percentage of a running LVM deployment", func() {
			oldPercentage := 50
			newPercentage := 100
			dep := &unstructured.Unstructured{
				Object: map[string]interface{}{
					"apiVersion": api.SchemeGroupVersion.String(),
					"kind":       "Deployment",
					"metadata": map[string]interface{}{
						"name": "test-update-pmem-space",
					},
					"spec": map[string]interface{}{
						"driverName":     "update-pmem-space.test.com",
						"image":          dummyImage,
						"deviceMode":     api.DeviceModeLVM,
						"pmemPercentage": oldPercentage,
					},
				},
			}

			deployment, err := toDeployment(dep)
			Expect(err).ShouldNot(HaveOccurred(), "unstructured to deployment conversion")

			deploy.CreateDeploymentCR(f, dep)
			defer deploy.DeleteDeploymentCR(f, deployment.Name)
			Expect(validate.DriverDeployment(ctx, f, d, *deployment)).Should(BeNil(), "validate driver")

			dep = deploy.GetDeploymentCR(f, deployment.Name)

			/* Update fields */
			spec := dep.Object["spec"].(map[string]interface{})
			spec["pmemPercentage"] = newPercentage

			deployment, err = toDeployment(dep)
			Expect(err).ShouldNot(HaveOccurred(), "unstructured to deployment conversion")

			deploy.UpdateDeploymentCR(f, dep)

			Eventually(func() bool {
				updatedDep := deploy.GetDeploymentCR(f, deployment.Name)
				spec := updatedDep.Object["spec"].(map[string]interface{})
				value := spec["pmemPercentage"].(int64)
				return int(value) == oldPercentage
			}, "3m", "2s").Should(BeTrue(), "pmem percentage value should not be updated")

			// ensure that the driver is still using the old device manager
			ds, err := f.ClientSet.AppsV1().DaemonSets(d.Namespace).Get(context.Background(), deployment.Name+"-node", metav1.GetOptions{})
			Expect(err).ShouldNot(HaveOccurred(), "daemon set should exists")
			for _, c := range ds.Spec.Template.Spec.InitContainers {
				if c.Name == "pmem-ns-init" {
					Expect(c.Command).Should(ContainElement(fmt.Sprintf("--useforfsdax=%d", oldPercentage)), "mismatched pmem percentage")
				}
			}
		})

		It("shall not allow to change labels of a running deployment", func() {
			oldLabels := map[string]string{
				"foo": "bar",
			}
			newLabels := map[string]string{
				"foo":  "bar",
				"foo2": "bar2",
			}
			dep := &unstructured.Unstructured{
				Object: map[string]interface{}{
					"apiVersion": api.SchemeGroupVersion.String(),
					"kind":       "Deployment",
					"metadata": map[string]interface{}{
						"name": "test-update-pmem-space",
					},
					"spec": map[string]interface{}{
						"driverName": "update-pmem-space.test.com",
						"image":      dummyImage,
						"deviceMode": api.DeviceModeLVM,
						"labels":     oldLabels,
					},
				},
			}

			deployment, err := toDeployment(dep)
			Expect(err).ShouldNot(HaveOccurred(), "unstructured to deployment conversion")

			deploy.CreateDeploymentCR(f, dep)
			defer deploy.DeleteDeploymentCR(f, deployment.Name)
			Expect(validate.DriverDeployment(ctx, f, d, *deployment)).Should(BeNil(), "validate driver")

			dep = deploy.GetDeploymentCR(f, deployment.Name)

			/* Update fields */
			spec := dep.Object["spec"].(map[string]interface{})
			spec["labels"] = newLabels

			deployment, err = toDeployment(dep)
			Expect(err).ShouldNot(HaveOccurred(), "unstructured to deployment conversion")

			deploy.UpdateDeploymentCR(f, dep)

			Eventually(func() bool {
				updatedDep := deploy.GetDeploymentCR(f, deployment.Name)
				spec := updatedDep.Object["spec"].(map[string]interface{})
				labels := spec["labels"].(map[string]interface{})
				// We cannot use reflect.DeepEqual here because the types are different.
				if len(labels) != len(oldLabels) {
					return false
				}
				for key, value := range oldLabels {
					if labels[key].(string) != value {
						return false
					}
				}
				return true
			}, "3m", "2s").Should(BeTrue(), "labels value should not be updated")

			// Ensure that the driver is still using the old labels. We only check the daemonset here
			// as one object of the driver deployment.
			ds, err := f.ClientSet.AppsV1().DaemonSets(d.Namespace).Get(context.Background(), deployment.Name+"-node", metav1.GetOptions{})
			Expect(err).ShouldNot(HaveOccurred(), "daemon set should exists")
			Expect(ds.Labels).Should(Equal(oldLabels), "mismatched labels in daemon set")
		})

		It("shall allow multiple deployments", func() {
			dep1 := &unstructured.Unstructured{
				Object: map[string]interface{}{
					"apiVersion": api.SchemeGroupVersion.String(),
					"kind":       "Deployment",
					"metadata": map[string]interface{}{
						"name": "test-deployment-1",
					},
					"spec": map[string]interface{}{
						"driverName": "deployment1.test.com",
						"image":      dummyImage,
					},
				},
			}
			dep2 := &unstructured.Unstructured{
				Object: map[string]interface{}{
					"apiVersion": api.SchemeGroupVersion.String(),
					"kind":       "Deployment",
					"metadata": map[string]interface{}{
						"name": "test-deployment-2",
					},
					"spec": map[string]interface{}{
						"driverName": "deployment2.test.com",
						"image":      dummyImage,
					},
				},
			}

			deployment1, err := toDeployment(dep1)
			Expect(err).ShouldNot(HaveOccurred(), "conversion from unstructured to deployment")

			deploy.CreateDeploymentCR(f, dep1)
			defer deploy.DeleteDeploymentCR(f, deployment1.Name)
			Expect(validate.DriverDeployment(ctx, f, d, *deployment1)).Should(BeNil(), "validate driver #1")

			deployment2, err := toDeployment(dep2)
			Expect(err).ShouldNot(HaveOccurred(), "conversion from unstructured to deployment")
			deploy.CreateDeploymentCR(f, dep2)
			defer deploy.DeleteDeploymentCR(f, deployment2.Name)
			Expect(validate.DriverDeployment(ctx, f, d, *deployment2)).Should(BeNil(), "validate driver #2")
		})

		It("shall not allow multiple deployments with same driver name", func() {
			driverName := "deployment-name-clash.test.com"
			dep1 := &unstructured.Unstructured{
				Object: map[string]interface{}{
					"apiVersion": api.SchemeGroupVersion.String(),
					"kind":       "Deployment",
					"metadata": map[string]interface{}{
						"name": "test-deployment-1",
					},
					"spec": map[string]interface{}{
						"driverName": driverName,
						"image":      dummyImage,
					},
				},
			}
			dep2 := &unstructured.Unstructured{
				Object: map[string]interface{}{
					"apiVersion": api.SchemeGroupVersion.String(),
					"kind":       "Deployment",
					"metadata": map[string]interface{}{
						"name": "test-deployment-2",
					},
					"spec": map[string]interface{}{
						"driverName": driverName,
						"image":      dummyImage,
					},
				},
			}

			deployment1, err := toDeployment(dep1)
			Expect(err).ShouldNot(HaveOccurred(), "conversion from unstructured to deployment")

			deploy.CreateDeploymentCR(f, dep1)
			defer deploy.DeleteDeploymentCR(f, deployment1.Name)
			Expect(validate.DriverDeployment(ctx, f, d, *deployment1)).Should(BeNil(), "validate driver #1")

			deployment2, err := toDeployment(dep2)
			Expect(err).ShouldNot(HaveOccurred(), "conversion from unstructured to deployment")
			deploy.CreateDeploymentCR(f, dep2)
			defer deploy.DeleteDeploymentCR(f, deployment2.Name)

			// Deployment should be In Failure state as other
			// deployment with that name exists
			validateDeploymentFailure(f, deployment2.Name)

			dep2 = deploy.GetDeploymentCR(f, deployment2.Name)
			// Resolve deployment name and update
			spec := dep2.Object["spec"].(map[string]interface{})
			spec["driverName"] = "new-driver-name"

			// and redeploy with new name
			deploy.UpdateDeploymentCR(f, dep2)

			deployment2, err = toDeployment(dep2)
			Expect(err).ShouldNot(HaveOccurred(), "conversion from unstructured to deployment")
			// Now it should succeed
			Expect(validate.DriverDeployment(ctx, f, d, *deployment2)).Should(BeNil(), "validate driver #2")
		})

		It("shall be able to use custom CA certificates", func() {
			caKey, err := pmemtls.NewPrivateKey()
			Expect(err).ShouldNot(HaveOccurred(), "create ca private key")
			regKey, err := pmemtls.NewPrivateKey()
			Expect(err).ShouldNot(HaveOccurred(), "create registry private key")
			nodeControllerKey, err := pmemtls.NewPrivateKey()
			Expect(err).ShouldNot(HaveOccurred(), "create node controller private key")
			ca, err := pmemtls.NewCA(nil, caKey)
			Expect(err).ShouldNot(HaveOccurred(), "create ca")

			regCert, err := ca.GenerateCertificate("pmem-registry", regKey.Public())
			Expect(err).ShouldNot(HaveOccurred(), "sign registry key")
			nodeControllerCert, err := ca.GenerateCertificate("pmem-node-controller", nodeControllerKey.Public())
			Expect(err).ShouldNot(HaveOccurred(), "sign node controller key")

			dep := &unstructured.Unstructured{
				Object: map[string]interface{}{
					"apiVersion": api.SchemeGroupVersion.String(),
					"kind":       "Deployment",
					"metadata": map[string]interface{}{
						"name": "test-deployment-with-certificates",
					},
					"spec": map[string]interface{}{
						"driverName":         "custom-ca.test.com",
						"image":              dummyImage,
						"caCert":             ca.EncodedCertificate(),
						"registryKey":        pmemtls.EncodeKey(regKey),
						"registryCert":       pmemtls.EncodeCert(regCert),
						"nodeControllerKey":  pmemtls.EncodeKey(nodeControllerKey),
						"nodeControllerCert": pmemtls.EncodeCert(nodeControllerCert),
					},
				},
			}

			deployment, err := toDeployment(dep)
			Expect(err).ShouldNot(HaveOccurred(), "conversion from unstructured to deployment")

			deploy.CreateDeploymentCR(f, dep)
			defer deploy.DeleteDeploymentCR(f, deployment.Name)
			Expect(validate.DriverDeployment(ctx, f, d, *deployment)).Should(BeNil(), "validate driver")
		})

		It("driver deployment shall be running even after operator exit", func() {
			dep := &unstructured.Unstructured{
				Object: map[string]interface{}{
					"apiVersion": api.SchemeGroupVersion.String(),
					"kind":       "Deployment",
					"metadata": map[string]interface{}{
						"name": "test-deployment-operator-exit",
					},
					"spec": map[string]interface{}{
						"driverName": "operator-exit.com",
						"image":      dummyImage,
					},
				},
			}

			deployment, err := toDeployment(dep)
			Expect(err).ShouldNot(HaveOccurred(), "unstructured to deployment conversion")

			deploy.CreateDeploymentCR(f, dep)

			defer deploy.DeleteDeploymentCR(f, deployment.Name)
			Expect(validate.DriverDeployment(ctx, f, d, *deployment)).Should(BeNil(), "validate driver before stopping")

			// Stop the operator
			stopOperator(c, d)
			// restore the deployment so that next test should  not effect
			defer startOperator(c, d)

			// Ensure that the driver is running consistently
			Consistently(func() error {
				return validate.DriverDeployment(ctx, f, d, *deployment)
			}, "1m", "20s").Should(BeNil(), "driver validation failure after restarting")
		})

		It("should be able to capture deployment changes when operator is not running", func() {
			dep := &unstructured.Unstructured{
				Object: map[string]interface{}{
					"apiVersion": api.SchemeGroupVersion.String(),
					"kind":       "Deployment",
					"metadata": map[string]interface{}{
						"name": "test-deployment-operator-restart",
					},
					"spec": map[string]interface{}{
						"driverName": "operator-restart.com",
						"image":      dummyImage,
					},
				},
			}

			deployment, err := toDeployment(dep)
			Expect(err).ShouldNot(HaveOccurred(), "unstructured to deployment conversion")

			deploy.CreateDeploymentCR(f, dep)
			defer deploy.DeleteDeploymentCR(f, deployment.Name)
			Expect(validate.DriverDeployment(ctx, f, d, *deployment)).Should(BeNil(), "validate driver")

			restored := false
			stopOperator(c, d)
			defer func() {
				if !restored {
					startOperator(c, d)
				}
			}()

			ca, err := pmemtls.NewCA(nil, nil)
			Expect(err).Should(BeNil(), "failed to instantiate CA")

			regKey, err := pmemtls.NewPrivateKey()
			Expect(err).Should(BeNil(), "failed to generate a private key: %v", err)
			regCert, err := ca.GenerateCertificate("pmem-registry", regKey.Public())
			Expect(err).Should(BeNil(), "failed to sign registry key")

			ncKey, err := pmemtls.NewPrivateKey()
			Expect(err).Should(BeNil(), "failed to generate a private key: %v", err)
			ncCert, err := ca.GenerateCertificate("pmem-node-controller", ncKey.Public())
			Expect(err).Should(BeNil(), "failed to sign node controller key")

			dep = deploy.GetDeploymentCR(f, deployment.Name)
			spec := dep.Object["spec"].(map[string]interface{})
			spec["image"] = "fake-image"
			spec["logLevel"] = api.DefaultLogLevel + 1
			spec["imagePullPolicy"] = "Never"
			spec["provisionerImage"] = "test-provisioner"
			spec["controllerResources"] = map[string]interface{}{
				"limits": map[string]interface{}{
					"cpu":    "150m",
					"memory": "1Mi",
				},
			}
			spec["nodeResources"] = map[string]interface{}{
				"limits": map[string]interface{}{
					"cpu":    "350m",
					"memory": "2Mi",
				},
			}
			spec["caCert"] = ca.EncodedCertificate()
			spec["registryKey"] = pmemtls.EncodeKey(regKey)
			spec["registryCert"] = pmemtls.EncodeCert(regCert)
			spec["nodeControllerKey"] = pmemtls.EncodeKey(ncKey)
			spec["nodeControllerCert"] = pmemtls.EncodeCert(ncCert)

			deployment, err = toDeployment(dep)
			Expect(err).ShouldNot(HaveOccurred(), "unstructured to deployment conversion")

			timestamp := deployment.Status.LastUpdated

			By("Updating PMEM-CSI deployment...")
			deploy.UpdateDeploymentCR(f, dep)

			startOperator(c, d)
			restored = true

			// Ensure that the operator reconciled the changes
			Eventually(func() bool {
				var err error
				dep := deploy.GetDeploymentCR(f, deployment.Name)
				deployment, err = toDeployment(dep)
				Expect(err).ShouldNot(HaveOccurred(), "unstructured to deployment conversion")
				return deployment.Status.LastUpdated.After(timestamp.Time)
			}, "3m", "2s").Should(BeTrue(), "reconcile updated deployment")

			defer GinkgoRecover()
			Eventually(func() string {
				if err := validate.DriverDeployment(ctx, f, d, *deployment); err != nil {
					return err.Error()
				}
				return ""
			}, "3m", "2s").Should(BeEmpty(), "validate driver after update")
		})
	})
})

func toDeployment(dep *unstructured.Unstructured) (*api.Deployment, error) {
	deployment := &api.Deployment{}
	if err := deploy.Scheme.Convert(dep, deployment, nil); err != nil {
		return nil, err
	}
	if err := deployment.EnsureDefaults(""); err != nil {
		return nil, fmt.Errorf("ensure defaults: %v", err)
	}

	return deployment, nil
}

func validateDeploymentFailure(f *framework.Framework, name string) {
	deployment := &api.Deployment{}
	Eventually(func() bool {
		dep, err := f.DynamicClient.Resource(deploy.DeploymentResource).Get(context.Background(), name, metav1.GetOptions{})
		if err != nil {
			return false
		}

		if err = deploy.Scheme.Convert(dep, deployment, nil); err != nil {
			return false
		}
		By(fmt.Sprintf("Deployment %q is in %q pahse", deployment.Name, deployment.Status.Phase))
		return deployment.Status.Phase == api.DeploymentPhaseFailed
	}, "3m", "5s").Should(BeTrue(), "deployment %q not running", name)
}

// findOperatorPod checks whether there is a PMEM-CSI operator
// installation in the cluster and returns the found operator pod.
func findOperatorPod(c *deploy.Cluster, d *deploy.Deployment) (*corev1.Pod, error) {
	return deploy.WaitForOperator(c, d.Namespace), nil
}

// stopOperator ensures operator deployment replica counter == 0 and the
// operator pod gets deleted
func stopOperator(c *deploy.Cluster, d *deploy.Deployment) error {
	By("Decrease operator deployment replicas to 0")
	Eventually(func() bool {
		dep, err := c.ClientSet().AppsV1().Deployments(d.Namespace).Get(context.Background(), "pmem-csi-operator", metav1.GetOptions{})
		if err != nil && !errors.IsNotFound(err) {
			return false
		}

		By(fmt.Sprintf("Replicas : %d", *dep.Spec.Replicas))
		if *dep.Spec.Replicas == 0 {
			return true
		}

		*dep.Spec.Replicas = 0
		_, err = c.ClientSet().AppsV1().Deployments(dep.Namespace).Update(context.Background(), dep, metav1.UpdateOptions{})
		deploy.LogError(err, "failed update operator's replica counter: %v", err)
		return false
	}, "3m", "1s").Should(BeTrue(), "set operator deployment replicas to 0")

	By("Ensure the operator pod deleted")

	Eventually(func() bool {
		_, err := c.GetAppInstance("pmem-csi-operator", "", d.Namespace)
		deploy.LogError(err, "get operator error: %v, will retry...", err)
		return err != nil && strings.HasPrefix(err.Error(), "no app")
	}, "3m", "1s").Should(BeTrue(), "delete operator pod")

	By("Operator pod deleted!")

	return nil
}

// startOperator ensures the operator deployment counter == 1 and the operator pod
// is in running state
func startOperator(c *deploy.Cluster, d *deploy.Deployment) {
	Eventually(func() bool {
		dep, err := c.ClientSet().AppsV1().Deployments(d.Namespace).Get(context.Background(), "pmem-csi-operator", metav1.GetOptions{})

		deploy.LogError(err, "Failed to get error: %v", err)
		if err != nil {
			return false
		}

		By(fmt.Sprintf("Replicas : %d", *dep.Spec.Replicas))
		if *dep.Spec.Replicas == 1 {
			return true
		}

		*dep.Spec.Replicas = 1
		_, err = c.ClientSet().AppsV1().Deployments(dep.Namespace).Update(context.Background(), dep, metav1.UpdateOptions{})
		deploy.LogError(err, "failed update operator's replication counter: %v", err)

		return false
	}, "3m", "1s").Should(BeTrue(), "increase operator deployment replicas to 1")

	By("Ensure operator pod is ready")
	deploy.WaitForOperator(c, d.Namespace)
	By("Operator is restored!")
}
