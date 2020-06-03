/*
Copyright 2019 Intel Corporation.

SPDX-License-Identifier: Apache-2.0
*/

package operator

import (
	"context"
	"strings"
	"time"

	api "github.com/intel/pmem-csi/pkg/apis/pmemcsi/v1alpha1"
	"github.com/intel/pmem-csi/pkg/k8sutil"
	"github.com/intel/pmem-csi/pkg/pmem-csi-operator/controller/deployment/testcases"
	"github.com/intel/pmem-csi/pkg/version"
	"github.com/intel/pmem-csi/test/e2e/deploy"
	"github.com/intel/pmem-csi/test/e2e/operator/validate"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/kubernetes/test/e2e/framework"
	runtime "sigs.k8s.io/controller-runtime/pkg/client"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
)

// We use intentionally use this non-existing driver image
// because these tests do not actually need a running driver.
const dummyImage = "unexisting/pmem-csi-driver"

func getDeployment(name string) api.Deployment {
	return api.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name: name,
		},
		Spec: api.DeploymentSpec{
			Image: dummyImage,
		},
	}
}

var _ = deploy.DescribeForSome("API", func(d *deploy.Deployment) bool {
	// Run these tests for all bare operator deployments, i.e.
	// those which did not already install the driver.
	return d.HasOperator && !d.HasDriver
}, func(d *deploy.Deployment) {
	var (
		c      *deploy.Cluster
		ctx    context.Context
		cancel func()
		client runtime.Client
		k8sver version.Version
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

		client, err = runtime.New(f.ClientConfig(), runtime.Options{})
		Expect(err).ShouldNot(HaveOccurred(), "new operator runtime client")

		ver, err := k8sutil.GetKubernetesVersion(f.ClientConfig())
		Expect(err).ShouldNot(HaveOccurred(), "get Kubernetes version")
		k8sver = *ver

		// All tests are expected to complete in 5 minutes.
		// We need to set up the global variables indirectly to avoid a watning about cancel not being called.
		ctx2, cancel2 := context.WithTimeout(context.Background(), 5*time.Minute)
		ctx, cancel = ctx2, cancel2
	})

	AfterEach(func() {
		cancel()
	})

	validateDriver := func(deployment api.Deployment, what ...interface{}) {
		framework.Logf("waiting for expectecd driver deployment %s", deployment.Name)
		if what == nil {
			what = []interface{}{"validate driver"}
		}
		framework.ExpectNoErrorWithOffset(1, validate.DriverDeploymentEventually(ctx, client, k8sver, d.Namespace, deployment), what...)
		framework.Logf("got expectecd driver deployment %s", deployment.Name)
	}

	Context("deployment", func() {

		tests := map[string]api.Deployment{
			"with defaults": getDeployment("test-deployment-with-defaults"),
			"with explicit values": api.Deployment{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-deployment-with-explicit",
				},
				Spec: api.DeploymentSpec{
					DeviceMode: api.DeviceModeDirect,
					PullPolicy: corev1.PullNever,
					Image:      dummyImage,
					ControllerResources: &corev1.ResourceRequirements{
						Limits: corev1.ResourceList{
							corev1.ResourceCPU:    resource.MustParse("200m"),
							corev1.ResourceMemory: resource.MustParse("100Mi"),
						},
					},
					NodeResources: &corev1.ResourceRequirements{
						Limits: corev1.ResourceList{
							corev1.ResourceCPU:    resource.MustParse("500m"),
							corev1.ResourceMemory: resource.MustParse("500Mi"),
						},
					},
				},
			},
		}

		for name, deployment := range tests {
			deployment := deployment
			It(name, func() {
				deployment = deploy.CreateDeploymentCR(f, deployment)
				defer deploy.DeleteDeploymentCR(f, deployment.Name)
				validateDriver(deployment)
			})
		}

		It("driver image shall default to operator image", func() {
			deployment := getDeployment("test-deployment-driver-image")
			deployment.Spec.Image = ""
			deployment.Spec.PMEMPercentage = 50

			deployment = deploy.CreateDeploymentCR(f, deployment)
			defer deploy.DeleteDeploymentCR(f, deployment.Name)

			operatorPod, err := findOperatorPod(c, d)
			Expect(err).ShouldNot(HaveOccurred(), "find operator deployment")

			// operator image should be the driver image
			deployment.Spec.Image = operatorPod.Spec.Containers[0].Image
			validateDriver(deployment)
		})

		It("shall be able to edit running deployment", func() {
			deployment := getDeployment("test-deployment-update")

			deployment = deploy.CreateDeploymentCR(f, deployment)
			defer deploy.DeleteDeploymentCR(f, deployment.Name)
			validateDriver(deployment, "validate driver before editing")

			// We have to get a fresh copy before updating it because the
			// operator should have modified the status.
			deployment = deploy.GetDeploymentCR(f, deployment.Name)

			// Update fields.
			spec := &deployment.Spec
			spec.LogLevel++
			spec.Image = "test-driver-image"
			spec.PullPolicy = corev1.PullNever
			spec.ProvisionerImage = "test-provisioner"
			spec.ControllerResources = &corev1.ResourceRequirements{
				Limits: corev1.ResourceList{
					corev1.ResourceCPU:    resource.MustParse("200m"),
					corev1.ResourceMemory: resource.MustParse("100Mi"),
				},
			}
			spec.NodeResources = &corev1.ResourceRequirements{
				Limits: corev1.ResourceList{
					corev1.ResourceCPU:    resource.MustParse("500m"),
					corev1.ResourceMemory: resource.MustParse("500Mi"),
				},
			}
			testcases.SetTLSOrDie(spec)

			deployment = deploy.UpdateDeploymentCR(f, deployment)

			validateDriver(deployment, "validate driver after editing")
		})

		It("shall allow multiple deployments", func() {
			deployment1 := getDeployment("test-deployment-1")
			deployment2 := getDeployment("test-deployment-2")

			deployment1 = deploy.CreateDeploymentCR(f, deployment1)
			defer deploy.DeleteDeploymentCR(f, deployment1.Name)
			validateDriver(deployment1, "validate driver #1")

			deployment2 = deploy.CreateDeploymentCR(f, deployment2)
			defer deploy.DeleteDeploymentCR(f, deployment2.Name)
			validateDriver(deployment1 /* TODO 2 */, "validate driver #2")
		})

		It("shall be able to use custom CA certificates", func() {
			deployment := getDeployment("test-deployment-with-certificates")
			testcases.SetTLSOrDie(&deployment.Spec)

			deployment = deploy.CreateDeploymentCR(f, deployment)
			defer deploy.DeleteDeploymentCR(f, deployment.Name)
			validateDriver(deployment)
		})

		It("driver deployment shall be running even after operator exit", func() {
			deployment := getDeployment("test-deployment-operator-exit")

			deployment = deploy.CreateDeploymentCR(f, deployment)

			defer deploy.DeleteDeploymentCR(f, deployment.Name)
			validateDriver(deployment)

			// Stop the operator
			stopOperator(c, d)
			// restore the deployment so that next test should  not effect
			defer startOperator(c, d)

			// Ensure that the driver is running consistently
			Consistently(func() error {
				return validate.DriverDeployment(client, k8sver, d.Namespace, deployment)
			}, "1m", "20s").ShouldNot(HaveOccurred(), "driver validation failure after restarting")
		})
	})

	Context("updating", func() {
		for _, testcase := range testcases.UpdateTests() {
			testcase := testcase
			Context(testcase.Name, func() {
				testIt := func(restart bool) {
					deployment := *testcase.Deployment.DeepCopyObject().(*api.Deployment)

					// Use fake images to prevent pods from actually starting.
					deployment.Spec.Image = dummyImage
					deployment.Spec.NodeRegistrarImage = dummyImage
					deployment.Spec.ProvisionerImage = dummyImage

					deployment = deploy.CreateDeploymentCR(f, deployment)
					defer deploy.DeleteDeploymentCR(f, deployment.Name)
					validateDriver(deployment, "validate driver before update")

					// We have to get a fresh copy before updating it because the
					// operator should have modified the status.
					deployment = deploy.GetDeploymentCR(f, deployment.Name)

					restored := false
					if restart {
						stopOperator(c, d)
						defer func() {
							if !restored {
								startOperator(c, d)
							}
						}()
					}

					testcase.Mutate(&deployment)
					deployment = deploy.UpdateDeploymentCR(f, deployment)

					if restart {
						startOperator(c, d)
						restored = true
					}

					validateDriver(deployment, "validate driver after update and restart")
				}

				It("while running", func() {
					testIt(false)
				})

				It("while stopped", func() {
					testIt(true)
				})
			})
		}
	})
})

func validateDeploymentFailure(f *framework.Framework, name string) {
	Eventually(func() bool {
		dep, err := f.DynamicClient.Resource(deploy.DeploymentResource).Get(context.Background(), name, metav1.GetOptions{})
		if err != nil {
			return false
		}

		deployment := deploy.DeploymentFromUnstructured(dep)
		framework.Logf("Deployment %q is in %q phase", deployment.Name, deployment.Status.Phase)
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
	framework.Logf("Decrease operator deployment replicas to 0")
	Eventually(func() bool {
		dep, err := c.ClientSet().AppsV1().Deployments(d.Namespace).Get(context.Background(), "pmem-csi-operator", metav1.GetOptions{})
		if err != nil && !errors.IsNotFound(err) {
			return false
		}

		if *dep.Spec.Replicas == 0 {
			return true
		}

		*dep.Spec.Replicas = 0
		_, err = c.ClientSet().AppsV1().Deployments(dep.Namespace).Update(context.Background(), dep, metav1.UpdateOptions{})
		deploy.LogError(err, "failed update operator's replica counter: %v", err)
		return false
	}, "3m", "1s").Should(BeTrue(), "set operator deployment replicas to 0")

	framework.Logf("Ensure the operator pod got deleted.")

	Eventually(func() bool {
		ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
		defer cancel()
		_, err := c.GetAppInstance(ctx, "pmem-csi-operator", "", d.Namespace)
		deploy.LogError(err, "get operator error: %v, will retry...", err)
		return err != nil && strings.HasPrefix(err.Error(), "no app")
	}, "3m", "1s").Should(BeTrue(), "delete operator pod")

	framework.Logf("Operator pod got deleted!")

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

		if *dep.Spec.Replicas == 1 {
			return true
		}

		*dep.Spec.Replicas = 1
		_, err = c.ClientSet().AppsV1().Deployments(dep.Namespace).Update(context.Background(), dep, metav1.UpdateOptions{})
		deploy.LogError(err, "failed update operator's replication counter: %v", err)

		return false
	}, "3m", "1s").Should(BeTrue(), "increase operator deployment replicas to 1")

	framework.Logf("Ensure operator pod is eady.")
	deploy.WaitForOperator(c, d.Namespace)
	framework.Logf("Operator is restored!")
}
