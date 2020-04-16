/*
Copyright 2019 Intel Corporation.

SPDX-License-Identifier: Apache-2.0
*/

package operator

import (
	"fmt"

	api "github.com/intel/pmem-csi/pkg/apis/pmemcsi/v1alpha1"
	pmemtls "github.com/intel/pmem-csi/pkg/pmem-csi-operator/pmem-tls"
	"github.com/intel/pmem-csi/test/e2e/deploy"

	corev1 "k8s.io/api/core/v1"
	storagev1beta1 "k8s.io/api/storage/v1beta1"
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

	f := framework.NewDefaultFramework("operator")

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
				validateDriverDeployment(f, d, deployment)
			})
		}

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
			validateDriverDeployment(f, d, deployment)

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

			deployment, err = toDeployment(dep)
			Expect(err).ShouldNot(HaveOccurred(), "unstructured to deployment conversion")

			ss, err := f.ClientSet.AppsV1().StatefulSets(d.Namespace).Get(deployment.Name+"-controller", metav1.GetOptions{})
			Expect(err).Should(BeNil(), "existence of controller stateful set")
			ssVersion := ss.GetResourceVersion()

			ds, err := f.ClientSet.AppsV1().DaemonSets(d.Namespace).Get(deployment.Name+"-node", metav1.GetOptions{})
			Expect(err).Should(BeNil(), "existence of node daemonst set")
			dsVersion := ds.GetResourceVersion()

			deploy.UpdateDeploymentCR(f, dep)

			// Wait till the sub-resources get updated
			// As a interm solution we are depending on subresoure(deaemon set, stateful set)
			// versions to make sure the resource got updated. Instead, operator should update
			// deployment status with appropriate events/condtion messages.
			Eventually(func() bool {
				ss, err := f.ClientSet.AppsV1().StatefulSets(d.Namespace).Get(deployment.Name+"-controller", metav1.GetOptions{})
				if err != nil {
					framework.Logf("Get stateful set error: %v", err)
					return false
				}
				ds, err := f.ClientSet.AppsV1().DaemonSets(d.Namespace).Get(deployment.Name+"-node", metav1.GetOptions{})
				if err != nil {
					framework.Logf("Get daemon set error: %v", err)
					return false
				}
				return ss.GetResourceVersion() != ssVersion && ds.GetResourceVersion() != dsVersion
			}, "3m", "1s").Should(BeTrue(), "expected both daemonset and stateupset get updated")

			validateDriverDeployment(f, d, deployment)
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
			validateDriverDeployment(f, d, deployment)

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
			ds, err := f.ClientSet.AppsV1().DaemonSets(d.Namespace).Get(deployment.Name+"-node", metav1.GetOptions{})
			Expect(err).ShouldNot(HaveOccurred(), "daemon set should exists")
			for _, c := range ds.Spec.Template.Spec.Containers {
				if c.Name == "pmem-driver" {
					Expect(c.Args).Should(ContainElement("-deviceManager="+string(oldMode)), "mismatched device manager")
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
			validateDriverDeployment(f, d, deployment)

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
			ds, err := f.ClientSet.AppsV1().DaemonSets(d.Namespace).Get(deployment.Name+"-node", metav1.GetOptions{})
			Expect(err).ShouldNot(HaveOccurred(), "daemon set should exists")
			for _, c := range ds.Spec.Template.Spec.InitContainers {
				if c.Name == "pmem-ns-init" {
					Expect(c.Args).Should(ContainElement(fmt.Sprintf("--useforfsdax=%d", oldPercentage)), "mismatched pmem percentage")
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
			validateDriverDeployment(f, d, deployment)

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
			ds, err := f.ClientSet.AppsV1().DaemonSets(d.Namespace).Get(deployment.Name+"-node", metav1.GetOptions{})
			Expect(err).ShouldNot(HaveOccurred(), "daemon set should exists")
			Expect(ds.Labels).Should(Equal(oldLabels), "mismatched labels in daemon set")
		})

		It("shall allow muliple deployments", func() {
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
			validateDriverDeployment(f, d, deployment1)

			deployment2, err := toDeployment(dep2)
			Expect(err).ShouldNot(HaveOccurred(), "conversion from unstructured to deployment")
			deploy.CreateDeploymentCR(f, dep2)
			defer deploy.DeleteDeploymentCR(f, deployment2.Name)
			validateDriverDeployment(f, d, deployment2)
		})

		It("shall not allow muliple deployments with same driver name", func() {
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
			validateDriverDeployment(f, d, deployment1)

			deployment2, err := toDeployment(dep2)
			Expect(err).ShouldNot(HaveOccurred(), "conversion from unstructured to deployment")
			deploy.CreateDeploymentCR(f, dep2)
			defer deploy.DeleteDeploymentCR(f, deployment2.Name)

			// Deployment should be In Failure state as other
			// deployment with that name exisits
			validateDeploymentFailure(f, deployment2.Name)

			dep2 = deploy.GetDeploymentCR(f, deployment2.Name)
			// Resolve deployment name and update
			dep2.Object["spec"] = map[string]interface{}{
				"driverName": "new-driver-name",
			}

			// and redeploy with new name
			deploy.UpdateDeploymentCR(f, dep2)

			deployment2, err = toDeployment(dep2)
			Expect(err).ShouldNot(HaveOccurred(), "conversion from unstructured to deployment")
			// Now it should succeed
			validateDriverDeployment(f, d, deployment2)
		})

		It("shall be able to use custom CA certificates", func() {
			caKey, err := pmemtls.NewPrivateKey()
			Expect(err).ShouldNot(HaveOccurred(), "creatre ca private key")
			regKey, err := pmemtls.NewPrivateKey()
			Expect(err).ShouldNot(HaveOccurred(), "creatre registry private key")
			nodeControllerKey, err := pmemtls.NewPrivateKey()
			Expect(err).ShouldNot(HaveOccurred(), "creatre node ocntroller private key")
			ca, err := pmemtls.NewCA(nil, caKey)
			Expect(err).ShouldNot(HaveOccurred(), "creatre ca")

			regCert, err := ca.GenerateCertificate("pmem-registry", regKey)
			Expect(err).ShouldNot(HaveOccurred(), "sign registry key")
			nodeControllerCert, err := ca.GenerateCertificate("pmem-node-controller", regKey)
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
			validateDriverDeployment(f, d, deployment)
		})
	})
})

func toDeployment(dep *unstructured.Unstructured) (*api.Deployment, error) {
	deployment := &api.Deployment{}
	if err := deploy.Scheme.Convert(dep, deployment, nil); err != nil {
		return nil, err
	}
	deployment.EnsureDefaults()

	return deployment, nil
}

func validateDriverDeployment(f *framework.Framework, d *deploy.Deployment, expected *api.Deployment) {
	deployment := &api.Deployment{}
	Eventually(func() bool {
		dep, err := f.DynamicClient.Resource(deploy.DeploymentResource).Get(expected.Name, metav1.GetOptions{})
		if err != nil {
			return false
		}

		if err = deploy.Scheme.Convert(dep, deployment, nil); err != nil {
			return false
		}
		By(fmt.Sprintf("Deployment %q is in %q phase", deployment.Name, deployment.Status.Phase))
		return deployment.Status.Phase == api.DeploymentPhaseRunning
	}, "3m", "5s").Should(BeTrue(), "deployment %q not running", expected.Name)

	// Validate sub-resources

	// Secretes
	caSecret, err := f.ClientSet.CoreV1().Secrets(d.Namespace).Get(expected.Name+"-pmem-ca", metav1.GetOptions{})
	Expect(err).ShouldNot(HaveOccurred(), "find ca secret")
	if expected.Spec.CACert != nil {
		Expect(caSecret.Data[corev1.TLSCertKey]).Should(BeEquivalentTo(expected.Spec.CACert))
	}
	regSecret, err := f.ClientSet.CoreV1().Secrets(d.Namespace).Get(expected.Name+"-pmem-registry", metav1.GetOptions{})
	Expect(err).ShouldNot(HaveOccurred(), "find registry secret")
	if expected.Spec.RegistryPrivateKey != nil {
		Expect(regSecret.Data[corev1.TLSPrivateKeyKey]).Should(BeEquivalentTo(expected.Spec.RegistryPrivateKey))
	}
	if expected.Spec.RegistryCert != nil {
		Expect(regSecret.Data[corev1.TLSCertKey]).Should(BeEquivalentTo(expected.Spec.RegistryCert))
	}
	nodeControllerSecret, err := f.ClientSet.CoreV1().Secrets(d.Namespace).Get(expected.Name+"-pmem-node-controller", metav1.GetOptions{})
	Expect(err).ShouldNot(HaveOccurred(), "find node controller secret")
	if expected.Spec.NodeControllerPrivateKey != nil {
		Expect(nodeControllerSecret.Data[corev1.TLSPrivateKeyKey]).Should(BeEquivalentTo(expected.Spec.NodeControllerPrivateKey))
	}
	if expected.Spec.NodeControllerCert != nil {
		Expect(nodeControllerSecret.Data[corev1.TLSCertKey]).Should(BeEquivalentTo(expected.Spec.NodeControllerCert))
	}

	// Statefulset and its containers
	ss, err := f.ClientSet.AppsV1().StatefulSets(d.Namespace).Get(expected.Name+"-controller", metav1.GetOptions{})
	Expect(err).Should(BeNil(), "existence of controller stateful set")
	Expect(*ss.Spec.Replicas).Should(BeEquivalentTo(1), "controller stateful set replication count mismatch")
	svcName := ss.Spec.ServiceName
	Expect(svcName).ShouldNot(BeEmpty(), "controller should have a service ")
	saName := ss.Spec.Template.Spec.ServiceAccountName
	Expect(saName).ShouldNot(BeEmpty(), "controller should a service account")

	findSecret := func(volumes []corev1.Volume, secret string) bool {
		for _, v := range volumes {
			if v.VolumeSource.Secret != nil && v.VolumeSource.Secret.SecretName == secret {
				return true
			}
		}
		return false
	}
	for _, secret := range []string{caSecret.Name, regSecret.Name} {
		Expect(findSecret(ss.Spec.Template.Spec.Volumes, secret)).Should(BeTrue(), "volume sources of stateful set shall have secret %s", secret)
	}

	containers := ss.Spec.Template.Spec.Containers
	Expect(len(containers)).Should(BeEquivalentTo(2), "controller stateful set container count mismatch")
	for _, c := range containers {
		cpu := c.Resources.Limits.Cpu()
		memory := c.Resources.Limits.Memory()
		Expect(c.ImagePullPolicy).Should(BeEquivalentTo(expected.Spec.PullPolicy), "pmem-driver: mismatched image pull policy")
		Expect(cpu).Should(BeEquivalentTo(expected.Spec.ControllerResources.Limits.Cpu()), "controller cpu resource limit mismatch")
		Expect(memory).Should(BeEquivalentTo(expected.Spec.ControllerResources.Limits.Memory()), "controller memory resource limit mismatch")
		switch c.Name {
		case "pmem-driver":
			Expect(c.Image).Should(BeEquivalentTo(expected.Spec.Image), "mismatched driver image")
			Expect(c.Args).Should(ContainElement("-drivername="+expected.Spec.DriverName), "mismatched driver name")
			Expect(c.Args).Should(ContainElement(fmt.Sprintf("-v=%d", expected.Spec.LogLevel)), "mismatched logging level")
		case "provisioner":
			Expect(c.Image).Should(BeEquivalentTo(expected.Spec.ProvisionerImage), "mismatched provisioner image")
		default:
			Fail(fmt.Sprintf("Unknown container name %q in controller stateful set", c.Name))
		}
	}

	// Daemonset and its containers
	ds, err := f.ClientSet.AppsV1().DaemonSets(d.Namespace).Get(expected.Name+"-node", metav1.GetOptions{})
	Expect(err).ShouldNot(HaveOccurred(), "daemon set should exists")
	for _, secret := range []string{caSecret.Name, nodeControllerSecret.Name} {
		Expect(findSecret(ds.Spec.Template.Spec.Volumes, secret)).Should(BeTrue(), "volume sources of daemon set shall have secret %s", secret)
	}

	if deployment.Spec.DeviceMode == api.DeviceModeLVM {
		Expect(len(ds.Spec.Template.Spec.InitContainers)).Should(BeEquivalentTo(2), "init container count")
		for _, c := range ds.Spec.Template.Spec.InitContainers {
			if c.Name == "pmem-ns-init" {
				Expect(c.Args).Should(ContainElement(fmt.Sprintf("--useforfsdax=%d", expected.Spec.PMEMPercentage)), "mismatched pmem percentage")
			}
		}
	}

	Expect(len(ds.Spec.Template.Spec.Containers)).Should(BeEquivalentTo(2), "daemon set container count")
	for _, c := range ds.Spec.Template.Spec.Containers {
		cpu := c.Resources.Limits.Cpu()
		memory := c.Resources.Limits.Memory()
		Expect(c.ImagePullPolicy).Should(BeEquivalentTo(expected.Spec.PullPolicy), "pmem-driver: mismatched image pull policy")
		Expect(cpu).Should(BeEquivalentTo(expected.Spec.NodeResources.Limits.Cpu()), "node cpu resource limit mismatch")
		Expect(memory).Should(BeEquivalentTo(expected.Spec.NodeResources.Limits.Memory()), "node memory resource limit mismatch")
		switch c.Name {
		case "pmem-driver":
			Expect(c.Image).Should(BeEquivalentTo(expected.Spec.Image), "mismatched driver image")
			Expect(c.Args).Should(ContainElement("-drivername="+expected.Spec.DriverName), "mismatched driver name")
			Expect(c.Args).Should(ContainElement("-deviceManager="+string(expected.Spec.DeviceMode)), "mismatched device manager")
			Expect(c.Args).Should(ContainElement(fmt.Sprintf("-v=%d", expected.Spec.LogLevel)), "mismatched logging level")
		case "driver-registrar":
			Expect(c.Image).Should(BeEquivalentTo(expected.Spec.NodeRegistrarImage), "mismatched driver-registrar image")
		default:
			Fail(fmt.Sprintf("Unknown container name %q in controller stateful set", c.Name))
		}
	}

	// should have a CSIDriver
	driver, err := f.ClientSet.StorageV1beta1().CSIDrivers().Get(expected.Spec.DriverName, metav1.GetOptions{})
	Expect(err).ShouldNot(HaveOccurred(), "expected instance of csi driver")
	lcModes := []storagev1beta1.VolumeLifecycleMode{
		storagev1beta1.VolumeLifecycleEphemeral,
		storagev1beta1.VolumeLifecyclePersistent,
	}
	By(fmt.Sprintf("Driver: %s Lifecycle Modes: %v", driver.Name, driver.Spec.VolumeLifecycleModes))
	Expect(driver.Spec.VolumeLifecycleModes).Should(ConsistOf(lcModes), "mismatched life cycle modes")

	svc, err := f.ClientSet.CoreV1().Services(d.Namespace).Get(svcName, metav1.GetOptions{})
	Expect(err).ShouldNot(HaveOccurred(), "missing controller service")
	Expect(len(svc.Spec.Ports)).ShouldNot(BeZero(), "controller service should have a port defined")

	svc, err = f.ClientSet.CoreV1().Services(d.Namespace).Get(expected.Name+"-metrics", metav1.GetOptions{})
	Expect(err).ShouldNot(HaveOccurred(), "missing metrics service")
	Expect(len(svc.Spec.Ports)).ShouldNot(BeZero(), "metrics service should have a port defined")

	// should have a service account
	sa, err := f.ClientSet.CoreV1().ServiceAccounts(d.Namespace).Get(saName, metav1.GetOptions{})
	Expect(err).ShouldNot(HaveOccurred(), "controller service account")
	Expect(len(sa.Secrets)).ShouldNot(BeZero(), "controller service account should have valid secrets")

	// should have defined Roles and Role bindings
	rb, err := f.ClientSet.RbacV1().RoleBindings(d.Namespace).Get(expected.Name, metav1.GetOptions{})
	Expect(err).ShouldNot(HaveOccurred(), "should have role binding instance")
	Expect(len(rb.Subjects)).ShouldNot(BeZero(), "role binding have a valid subject")
	Expect(rb.Subjects[0].Name).Should(BeEquivalentTo(saName), "rolbe binding should have a valid service account")

	_, err = f.ClientSet.RbacV1().Roles(d.Namespace).Get(rb.RoleRef.Name, metav1.GetOptions{})
	Expect(err).ShouldNot(HaveOccurred(), "roles should have been defined")

	crb, err := f.ClientSet.RbacV1().ClusterRoleBindings().Get(expected.Name, metav1.GetOptions{})
	Expect(err).ShouldNot(HaveOccurred(), "should have a cluster role binding instance")
	Expect(len(crb.Subjects)).ShouldNot(BeZero(), "cluster role binding should have a valid subject")
	Expect(crb.Subjects[0].Name).Should(BeEquivalentTo(saName), "cluster role binding should have a valid service account")
	_, err = f.ClientSet.RbacV1().Roles(d.Namespace).Get(rb.RoleRef.Name, metav1.GetOptions{})
	Expect(err).ShouldNot(HaveOccurred(), "roles should have been defined")
}

func validateDeploymentFailure(f *framework.Framework, name string) {
	deployment := &api.Deployment{}
	Eventually(func() bool {
		dep, err := f.DynamicClient.Resource(deploy.DeploymentResource).Get(name, metav1.GetOptions{})
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
