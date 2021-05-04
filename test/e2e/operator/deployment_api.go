/*
Copyright 2019 Intel Corporation.

SPDX-License-Identifier: Apache-2.0
*/

package operator

import (
	"context"
	"fmt"
	"os"
	"strings"
	"sync"
	"time"

	api "github.com/intel/pmem-csi/pkg/apis/pmemcsi/v1beta1"
	"github.com/intel/pmem-csi/pkg/exec"
	"github.com/intel/pmem-csi/pkg/k8sutil"
	"github.com/intel/pmem-csi/pkg/pmem-csi-operator/controller/deployment/testcases"
	"github.com/intel/pmem-csi/pkg/version"
	"github.com/intel/pmem-csi/test/e2e/deploy"
	"github.com/intel/pmem-csi/test/e2e/operator/validate"
	"github.com/intel/pmem-csi/test/e2e/pod"

	admissionregistrationv1 "k8s.io/api/admissionregistration/v1"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	storagev1 "k8s.io/api/storage/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/apimachinery/pkg/watch"
	"k8s.io/kubernetes/test/e2e/framework"
	e2epv "k8s.io/kubernetes/test/e2e/framework/pv"
	runtime "sigs.k8s.io/controller-runtime/pkg/client"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
)

// We intentionally use this non-existing container registry for driver image
// because these tests do not actually need a running driver.
const dummyImage = "127.0.0.1:270/unexisting/pmem-csi-driver"

func getDeployment(name string) api.PmemCSIDeployment {
	return api.PmemCSIDeployment{
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
		c                  *deploy.Cluster
		ctx                context.Context
		cancel             func()
		client             runtime.Client
		k8sver             version.Version
		evWatcher          watch.Interface
		evWait             sync.WaitGroup
		evCaptured         map[types.UID]map[string]struct{}
		metricsURL         string
		lastReconcileCount float64
	)

	f := framework.NewDefaultFramework("operator")
	// test/e2e/deploy.go is using default namespace for deploying operator.
	// So we could skip default namespace creation/deletion steps
	f.SkipNamespaceCreation = true

	BeforeEach(func() {
		Expect(f).ShouldNot(BeNil(), "framework init")
		cluster, err := deploy.NewCluster(f.ClientSet, f.DynamicClient, f.ClientConfig())
		Expect(err).ShouldNot(HaveOccurred(), "new cluster")
		c = cluster

		client, err = runtime.New(f.ClientConfig(), runtime.Options{})
		Expect(err).ShouldNot(HaveOccurred(), "new operator runtime client")

		ver, err := k8sutil.GetKubernetesVersion(f.ClientConfig())
		Expect(err).ShouldNot(HaveOccurred(), "get Kubernetes version")
		k8sver = *ver

		evWatcher, err = f.ClientSet.CoreV1().Events("").Watch(context.TODO(), metav1.ListOptions{})
		Expect(err).ShouldNot(HaveOccurred(), "get events watcher")
		evCaptured = map[types.UID]map[string]struct{}{}

		evWait.Add(1)
		go func() {
			defer evWait.Done()
			for watchEvent := range evWatcher.ResultChan() {
				ev, ok := watchEvent.Object.(*corev1.Event)
				if !ok || ev.Source.Component != "pmem-csi-operator" {
					continue
				}
				if _, ok := evCaptured[ev.InvolvedObject.UID]; !ok {
					evCaptured[ev.InvolvedObject.UID] = map[string]struct{}{}
				}
				evCaptured[ev.InvolvedObject.UID][ev.Reason] = struct{}{}
			}
		}()

		// All tests are expected to complete in 5 minutes.
		// We need to set up the global variables indirectly to avoid a warning about cancel not being called.
		ctx2, cancel2 := context.WithTimeout(context.Background(), 5*time.Minute)
		ctx, cancel = ctx2, cancel2

		url, err := deploy.GetOperatorMetricsURL(ctx, c, d)
		ExpectWithOffset(1, err).ShouldNot(HaveOccurred(), "get metrics url")
		metricsURL = url

		lastReconcileCount = 0
	})

	AfterEach(func() {
		evWatcher.Stop()
		// Wait till completion of event handler before
		// making evCapture map to nil
		evWait.Wait()
		evCaptured = nil
		cancel()
	})

	validateDriver := func(deployment api.PmemCSIDeployment, expectUpdates []runtime.Object, what ...interface{}) {
		framework.Logf("waiting for expected driver deployment %s", deployment.Name)
		if what == nil {
			what = []interface{}{"validate driver"}
		}
		var err error
		lastReconcileCount, err = validate.DriverDeploymentEventually(ctx, c, client, k8sver, metricsURL, d.Namespace, deployment, expectUpdates, lastReconcileCount)
		framework.ExpectNoErrorWithOffset(1, err, what...)
		framework.Logf("got expected driver deployment %s", deployment.Name)
	}

	validateConditions := func(depName string, expected map[api.DeploymentConditionType]corev1.ConditionStatus, what ...interface{}) {
		if what == nil {
			what = []interface{}{"validate driver(%s) status conditions", depName}
		}
		dep := deploy.GetDeploymentCR(f, depName)
		actual := dep.Status.Conditions
		ExpectWithOffset(1, len(actual)).Should(BeEquivalentTo(len(expected)), what...)
		for _, c := range actual {
			ExpectWithOffset(2, expected[c.Type]).Should(BeEquivalentTo(c.Status))
		}
	}

	validateEvents := func(dep *api.PmemCSIDeployment, expectedEvents []string, what ...interface{}) {
		if what == nil {
			what = []interface{}{"validate events"}
		}
		expected := map[string]struct{}{}
		for _, r := range expectedEvents {
			expected[r] = struct{}{}
		}
		Eventually(func() bool {
			reasons := map[string]struct{}{}
			ok := false
			if reasons, ok = evCaptured[dep.UID]; !ok {
				// No event captured for this object
				return false
			}
			for r := range reasons {
				if _, ok := expected[r]; ok {
					delete(expected, r)
				}
			}
			return len(expected) == 0
		}, 2*time.Minute, time.Second, what, ": ", expected)
	}

	ensureObjectRecovered := func(obj runtime.Object) {
		framework.Logf("Waiting for deleted object recovered %T/%s", obj, obj.GetName())
		key := runtime.ObjectKey{Name: obj.GetName(), Namespace: obj.GetNamespace()}
		Eventually(func() error {
			return client.Get(context.TODO(), key, obj)
		}, "2m", "1s").ShouldNot(HaveOccurred(), "failed to recover object")
		framework.Logf("Object %T/%s recovered", obj, obj.GetName())
	}

	Context("deployment", func() {

		tests := map[string]api.PmemCSIDeployment{
			"with defaults": getDeployment("test-deployment-with-defaults"),
			"with explicit values": {
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-deployment-with-explicit",
				},
				Spec: api.DeploymentSpec{
					DeviceMode: api.DeviceModeDirect,
					PullPolicy: corev1.PullNever,
					Image:      dummyImage,
					ControllerDriverResources: &corev1.ResourceRequirements{
						Limits: corev1.ResourceList{
							corev1.ResourceCPU:    resource.MustParse("200m"),
							corev1.ResourceMemory: resource.MustParse("100Mi"),
						},
					},
					NodeDriverResources: &corev1.ResourceRequirements{
						Limits: corev1.ResourceList{
							corev1.ResourceCPU:    resource.MustParse("500m"),
							corev1.ResourceMemory: resource.MustParse("500Mi"),
						},
					},
					ProvisionerResources: &corev1.ResourceRequirements{
						Limits: corev1.ResourceList{
							corev1.ResourceCPU:    resource.MustParse("210m"),
							corev1.ResourceMemory: resource.MustParse("110Mi"),
						},
					},
					NodeRegistrarResources: &corev1.ResourceRequirements{
						Limits: corev1.ResourceList{
							corev1.ResourceCPU:    resource.MustParse("300m"),
							corev1.ResourceMemory: resource.MustParse("100Mi"),
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
				validateDriver(deployment, nil)
				validateConditions(deployment.Name, map[api.DeploymentConditionType]corev1.ConditionStatus{
					api.DriverDeployed: corev1.ConditionTrue,
				})
				validateEvents(&deployment, []string{api.EventReasonNew, api.EventReasonRunning})
			})
		}

		It("get deployment shall list expected fields", func() {
			lblKey := "storage"
			lblValue := "unknown-node"
			deployment := getDeployment("test-get-deployment-fields")
			// Only values that are visible in Deployment CR are shown in `kubectl get`
			// but, not the default values chosen by the operator.
			// So provide the values that are expected to list.
			deployment.Spec.DeviceMode = api.DeviceModeDirect
			deployment.Spec.PullPolicy = corev1.PullNever
			deployment.Spec.Image = dummyImage
			deployment.Spec.NodeSelector = map[string]string{
				lblKey: lblValue,
			}

			deployment = deploy.CreateDeploymentCR(f, deployment)
			defer deploy.DeleteDeploymentCR(f, deployment.Name)
			validateDriver(deployment, nil)

			d := deploy.GetDeploymentCR(f, deployment.Name)

			// Run in-cluster kubectl from master node
			ssh := os.Getenv("REPO_ROOT") + "/_work/" + os.Getenv("CLUSTER") + "/ssh.0"
			out, err := exec.RunCommand(ssh, "kubectl", "get", "pmemcsideployments.pmem-csi.intel.com", "--no-headers")
			Expect(err).ShouldNot(HaveOccurred(), "kubectl get: %v", out)
			Expect(out).Should(MatchRegexp(`%s\s+%s\s+.*"?%s"?:"?%s"?.*\s+%s\s+%s\s+[0-9]+(s|m)`,
				d.Name, d.Spec.DeviceMode, lblKey, lblValue, d.Spec.Image, d.Status.Phase), "fields mismatch")
		})

		It("driver image shall default to operator image", func() {
			deployment := getDeployment("test-deployment-driver-image")
			deployment.Spec.Image = ""
			deployment.Spec.PMEMPercentage = 50

			deployment = deploy.CreateDeploymentCR(f, deployment)
			defer deploy.DeleteDeploymentCR(f, deployment.Name)

			operatorPod := deploy.WaitForOperator(c, d.Namespace)

			// operator image should be the driver image
			deployment.Spec.Image = operatorPod.Spec.Containers[0].Image
			validateDriver(deployment, nil)
		})

		Context("logging format", func() {
			cases := map[string]api.LogFormat{
				"default": "",
				"text":    api.LogFormatText,
				"json":    api.LogFormatJSON,
			}
			for name, format := range cases {
				format := format
				It(name, func() {
					deployment := getDeployment("test-logging-format-" + name)
					deployment.Spec.Image = ""
					deployment.Spec.PMEMPercentage = 50
					deployment.Spec.LogFormat = format
					deployment.Spec.ControllerTLSSecret = "pmem-csi-intel-com-controller-secret"

					deployment = deploy.CreateDeploymentCR(f, deployment)
					defer deploy.DeleteDeploymentCR(f, deployment.Name)

					validateDriver(deployment, nil)

					controllerPodName := deployment.Name + "-controller-0"
					controllerContainerName := "pmem-driver"

					deadline, cancel := context.WithTimeout(ctx, 5*time.Minute)
					defer cancel()
					output, err := pod.Logs(deadline, f.ClientSet, d.Namespace, controllerPodName, controllerContainerName)
					framework.ExpectNoError(err, "get pod logs for running controller-0")

					if format == api.LogFormatJSON {
						Expect(output).To(MatchRegexp(`\{"ts":\d+.\d+,"msg":"Version:`), "PMEM-CSI driver output in JSON")
					} else {
						Expect(output).To(MatchRegexp(`I.*main.go:.*Version: `), "PMEM-CSI driver output in glog format")
					}
					framework.Logf("got pod log: %s", output)
				})
			}
		})

		It("shall be able to edit running deployment", func() {
			deployment := getDeployment("test-deployment-update")

			deployment = deploy.CreateDeploymentCR(f, deployment)
			defer deploy.DeleteDeploymentCR(f, deployment.Name)
			validateDriver(deployment, nil, "validate driver before editing")

			// We have to get a fresh copy before updating it because the
			// operator should have modified the status.
			deployment = deploy.GetDeploymentCR(f, deployment.Name)

			// Update fields.
			spec := &deployment.Spec
			spec.LogLevel++
			spec.Image = "test-driver-image"
			spec.PullPolicy = corev1.PullNever
			spec.ProvisionerImage = "test-provisioner"
			spec.ControllerDriverResources = &corev1.ResourceRequirements{
				Limits: corev1.ResourceList{
					corev1.ResourceCPU:    resource.MustParse("200m"),
					corev1.ResourceMemory: resource.MustParse("100Mi"),
				},
			}
			spec.NodeDriverResources = &corev1.ResourceRequirements{
				Limits: corev1.ResourceList{
					corev1.ResourceCPU:    resource.MustParse("500m"),
					corev1.ResourceMemory: resource.MustParse("500Mi"),
				},
			}
			spec.ProvisionerResources = &corev1.ResourceRequirements{
				Limits: corev1.ResourceList{
					corev1.ResourceCPU:    resource.MustParse("300m"),
					corev1.ResourceMemory: resource.MustParse("300Mi"),
				},
			}
			spec.NodeRegistrarResources = &corev1.ResourceRequirements{
				Limits: corev1.ResourceList{
					corev1.ResourceCPU:    resource.MustParse("100m"),
					corev1.ResourceMemory: resource.MustParse("200Mi"),
				},
			}

			// Expected sub-resource updates while reconcile the deployment changes
			nodeSetupDS := appsv1.DaemonSet{}
			err := client.Get(ctx, runtime.ObjectKey{
				Name:      deployment.NodeSetupName(),
				Namespace: d.Namespace,
			}, &nodeSetupDS)
			framework.ExpectNoError(err, "get node setup deemonset")
			nodeSetupDS.TypeMeta = metav1.TypeMeta{APIVersion: "apps/v1", Kind: "DaemonSet"}

			nodeDS := appsv1.DaemonSet{}
			err = client.Get(ctx, runtime.ObjectKey{
				Name:      deployment.NodeDriverName(),
				Namespace: d.Namespace,
			}, &nodeDS)
			framework.ExpectNoError(err, "get node driver deemonset")
			nodeDS.TypeMeta = metav1.TypeMeta{APIVersion: "apps/v1", Kind: "DaemonSet"}

			controllerSS := appsv1.StatefulSet{}
			err = client.Get(ctx, runtime.ObjectKey{
				Name:      deployment.ControllerDriverName(),
				Namespace: d.Namespace,
			}, &controllerSS)
			framework.ExpectNoError(err, "get controller driver daemonset")
			controllerSS.TypeMeta = metav1.TypeMeta{APIVersion: "apps/v1", Kind: "StatefulSet"}
			deployment = deploy.UpdateDeploymentCR(f, deployment)
			validateDriver(deployment, []runtime.Object{&nodeSetupDS, &nodeDS, &controllerSS}, "validate driver after editing")
		})

		It("shall allow multiple deployments", func() {
			deployment1 := getDeployment("test-deployment-1")
			deployment2 := getDeployment("test-deployment-2")

			deployment1 = deploy.CreateDeploymentCR(f, deployment1)
			defer deploy.DeleteDeploymentCR(f, deployment1.Name)
			validateDriver(deployment1, nil, "validate driver #1")
			validateEvents(&deployment1, []string{api.EventReasonNew, api.EventReasonRunning})

			deployment2 = deploy.CreateDeploymentCR(f, deployment2)
			defer deploy.DeleteDeploymentCR(f, deployment2.Name)
			// reset reconcile count for the new deployment
			lastReconcileCount = 0
			validateDriver(deployment2, nil, "validate driver #2")
			validateEvents(&deployment2, []string{api.EventReasonNew, api.EventReasonRunning})
		})

		It("shall support dots in the name", func() {
			deployment1 := getDeployment("test.deployment.example.org")

			deployment1 = deploy.CreateDeploymentCR(f, deployment1)
			defer deploy.DeleteDeploymentCR(f, deployment1.Name)
			validateDriver(deployment1, nil)
		})

		It("shall be able to use custom CA certificates", func() {
			deployment := getDeployment("test-deployment-with-certificates")
			deployment = deploy.CreateDeploymentCR(f, deployment)
			defer deploy.DeleteDeploymentCR(f, deployment.Name)
			validateDriver(deployment, nil)
			validateConditions(deployment.Name, map[api.DeploymentConditionType]corev1.ConditionStatus{
				api.DriverDeployed: corev1.ConditionTrue,
			})
			validateEvents(&deployment, []string{api.EventReasonNew, api.EventReasonRunning})
		})

		It("driver deployment shall be running even after operator exit", func() {
			deployment := getDeployment("test-deployment-operator-exit")

			deployment = deploy.CreateDeploymentCR(f, deployment)

			defer deploy.DeleteDeploymentCR(f, deployment.Name)
			validateDriver(deployment, nil, "before operator restart")
			validateConditions(deployment.Name, map[api.DeploymentConditionType]corev1.ConditionStatus{
				api.DriverDeployed: corev1.ConditionTrue,
			})

			// Stop the operator
			stopOperator(c, d)
			// restore the deployment so that next test should  not effect
			defer func() {
				startOperator(c, d)
				url, err := deploy.GetOperatorMetricsURL(ctx, c, d)
				Expect(err).ShouldNot(HaveOccurred(), "get metrics url after operator restart")
				metricsURL = url
			}()

			// Ensure that the driver is running consistently
			err := validate.DriverDeployment(ctx, client, k8sver, d.Namespace, deployment)
			Expect(err).ShouldNot(HaveOccurred(), "after operator restart")
		})

		It("shall recover from conflicts", func() {
			deployment := getDeployment("test-recover-from-conflicts")
			deployment.Spec.ControllerTLSSecret = "pmem-csi-intel-com-controller-secret"
			se := &corev1.Service{
				ObjectMeta: metav1.ObjectMeta{
					Name:      deployment.GetHyphenedName() + "-scheduler",
					Namespace: d.Namespace,
				},
				Spec: corev1.ServiceSpec{
					Selector: map[string]string{"app": "foobar"},
					Type:     corev1.ServiceTypeClusterIP,
					Ports: []corev1.ServicePort{
						{Port: 433},
					},
				},
			}
			deleteService := func() {
				Eventually(func() error {
					err := f.ClientSet.CoreV1().Services(d.Namespace).Delete(context.Background(), se.Name, metav1.DeleteOptions{})
					deploy.LogError(err, "Delete service error: %v, will retry...", err)
					if errors.IsNotFound(err) {
						return nil
					}
					return err
				}, "3m", "1s").ShouldNot(HaveOccurred(), "delete service %s", se.Name)
			}
			Eventually(func() error {
				_, err := f.ClientSet.CoreV1().Services(d.Namespace).Create(context.Background(), se, metav1.CreateOptions{})
				deploy.LogError(err, "create service error: %v, will retry...", err)
				return err
			}, "3m", "1s").ShouldNot(HaveOccurred(), "create service %s", se.Name)
			defer deleteService()

			deployment = deploy.CreateDeploymentCR(f, deployment)
			defer deploy.DeleteDeploymentCR(f, deployment.Name)

			// The deployment should fail to create the required service as it already
			// exists and is owned by others.
			Eventually(func() bool {
				out := deploy.GetDeploymentCR(f, deployment.Name)
				return out.Status.Phase == api.DeploymentPhaseFailed
			}, "3m", "1s").Should(BeTrue(), "deployment should fail %q", deployment.Name)
			validateEvents(&deployment, []string{api.EventReasonNew, api.EventReasonFailed})

			// Deleting the existing service should make the deployment succeed.
			deleteService()
			Eventually(func() bool {
				out := deploy.GetDeploymentCR(f, deployment.Name)
				return out.Status.Phase == api.DeploymentPhaseRunning
			}, "3m", "1s").Should(BeTrue(), "after resolved conflicts")
			validateEvents(&deployment, []string{api.EventReasonNew, api.EventReasonRunning})

			err := validate.DriverDeployment(ctx, client, k8sver, d.Namespace, deployment)
			framework.ExpectNoError(err, "validate driver after resolved conflicts")
		})

		It("shall recover from missing secret", func() {
			deployment := getDeployment("test-recover-from-missing-secret")
			sec := &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "my-controller-secret",
					Namespace: d.Namespace,
				},
				Type: corev1.SecretTypeTLS,
				Data: map[string][]byte{
					"ca.crt":  []byte("fake ca"),
					"tls.key": []byte("fake key"),
					"tls.crt": []byte("fake crt"),
				},
			}
			deployment.Spec.ControllerTLSSecret = sec.Name
			deployment = deploy.CreateDeploymentCR(f, deployment)
			defer deploy.DeleteDeploymentCR(f, deployment.Name)

			// The deployment should fail because the required secret is missing.
			Eventually(func() bool {
				out := deploy.GetDeploymentCR(f, deployment.Name)
				return out.Status.Phase == api.DeploymentPhaseFailed
			}, "3m", "1s").Should(BeTrue(), "deployment should fail %q", deployment.Name)
			validateEvents(&deployment, []string{api.EventReasonNew, api.EventReasonFailed})

			// Creating the secret should make the deployment succeed.
			deleteSecret := func() {
				Eventually(func() error {
					err := f.ClientSet.CoreV1().Secrets(d.Namespace).Delete(context.Background(), sec.Name, metav1.DeleteOptions{})
					deploy.LogError(err, "Delete secret error: %v, will retry...", err)
					if errors.IsNotFound(err) {
						return nil
					}
					return err
				}, "3m", "1s").ShouldNot(HaveOccurred(), "delete secret %s", sec.Name)
			}
			Eventually(func() error {
				_, err := f.ClientSet.CoreV1().Secrets(d.Namespace).Create(context.Background(), sec, metav1.CreateOptions{})
				deploy.LogError(err, "create secret error: %v, will retry...", err)
				return err
			}, "3m", "1s").ShouldNot(HaveOccurred(), "create secret %s", sec.Name)
			defer deleteSecret()
			// Now as the missing secrets are there in place, the deployment should
			// move ahead and get succeed
			Eventually(func() bool {
				deployment := deploy.GetDeploymentCR(f, deployment.Name)
				return deployment.Status.Phase == api.DeploymentPhaseRunning
			}, "3m", "1s").Should(BeTrue(), "deployment should not fail %q", deployment.Name)
			validateEvents(&deployment, []string{api.EventReasonNew, api.EventReasonRunning})
			err := validate.DriverDeployment(context.Background(), client, k8sver, d.Namespace, deployment)
			Expect(err).ShouldNot(HaveOccurred(), "validate driver after secret creation")
		})
	})

	Context("switch device mode", func() {
		startPod := func(f *framework.Framework, pvc *corev1.PersistentVolumeClaim) *corev1.Pod {
			// Now try using the volume
			app := &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "switch-mode-app",
					Namespace: corev1.NamespaceDefault,
				},
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{
						{
							Name:            "test-driver",
							Image:           os.Getenv("PMEM_CSI_IMAGE"),
							ImagePullPolicy: corev1.PullIfNotPresent,
							Command:         []string{"sleep", "180"},
						},
					},
					Volumes: []corev1.Volume{
						{
							Name: "pmem-volume",
							VolumeSource: corev1.VolumeSource{
								PersistentVolumeClaim: &corev1.PersistentVolumeClaimVolumeSource{
									ClaimName: pvc.Name,
								},
							},
						},
					},
				},
			}

			By(fmt.Sprintf("Starting application pod '%s'", app.Name))
			Eventually(func() error {
				_, err := f.ClientSet.CoreV1().Pods(app.Namespace).Create(context.Background(), app, metav1.CreateOptions{})
				deploy.LogError(err, "create pod %q error: %v, will retry...", app.Name, err)
				return err
			}, "3m", "1s").ShouldNot(HaveOccurred(), "create pod %q", app.Name)

			return app
		}

		stopPod := func(p *corev1.Pod) {
			By(fmt.Sprintf("Stopping application pod '%s'", p.Name))
			Eventually(func() error {
				err := f.ClientSet.CoreV1().Pods(p.Namespace).Delete(context.Background(), p.Name, metav1.DeleteOptions{})
				if err != nil && errors.IsNotFound(err) {
					return nil
				}
				deploy.LogError(err, "delete pod %q error: %v, will retry...", p.Name, err)
				return err
			}, "3m", "1s").ShouldNot(HaveOccurred(), "delete pod %q", p.Name)
		}

		checkIfPodRunning := func(p *corev1.Pod) {
			By(fmt.Sprintf("Ensure application pod '%s' is running", p.Name))
			Eventually(func() error {
				pod, err := f.ClientSet.CoreV1().Pods(p.Namespace).Get(context.Background(), p.Name, metav1.GetOptions{})
				if err != nil {
					return err
				}
				if pod.Status.Phase != corev1.PodRunning {
					return fmt.Errorf("%s: status %v", pod.Name, pod.Status.Phase)
				}
				return nil
			}, "3m", "1s").ShouldNot(HaveOccurred(), "pod read %q", p.Name)
		}

		postSwitchFuncs := map[string]func(from, to api.DeviceMode, depName string, pvc *corev1.PersistentVolumeClaim){
			"delete volume": func(from, to api.DeviceMode, depName string, pvc *corev1.PersistentVolumeClaim) {
				// Delete Volume created in `from` device mode
				deletePVC(f, pvc.Namespace, pvc.Name)
			},
			"use volume": func(from, to api.DeviceMode, depName string, pvc *corev1.PersistentVolumeClaim) {
				// Switch back to original device mode
				switchDeploymentMode(c, f, depName, d.Namespace, from)

				p := startPod(f, pvc)
				defer stopPod(p)
				checkIfPodRunning(p)
			},
			"use volume in different mode": func(from, to api.DeviceMode, depName string, pvc *corev1.PersistentVolumeClaim) {
				// Application should able to use the volume created by the driver in diffrent mode
				p := startPod(f, pvc)
				defer stopPod(p)
				checkIfPodRunning(p)
			},
		}

		defineSwitchModeTests := func(ctx string, from, to api.DeviceMode) {
			for name, postSwitch := range postSwitchFuncs {
				Context(ctx, func() {
					name := name
					postSwitch := postSwitch
					It(name, func() {
						driverName := ctx + "-" + strings.Replace(name, " ", "-", -1)
						deployment := api.PmemCSIDeployment{
							ObjectMeta: metav1.ObjectMeta{
								Name: driverName,
							},
							Spec: api.DeploymentSpec{
								DeviceMode:     from,
								PMEMPercentage: 50,
								NodeSelector: map[string]string{
									// Provided by NFD.
									"feature.node.kubernetes.io/memory-nv.dax": "true",
								},
							},
						}

						deployment = deploy.CreateDeploymentCR(f, deployment)
						defer deploy.DeleteDeploymentCR(f, deployment.Name)
						validateDriver(deployment, nil, "validate driver")

						// NOTE(avalluri): As the current operator does not support deploying
						// the driver in 'testing' mode, we cannot directely access CSI
						// interface of it. Hence, using SC/PVC for creating volumes.
						//
						// Once we add "-testing" support we could simplify the code
						// by using controller's CSI interface to create/delete/publish
						// the test volume.

						sc := createStorageClass(f, "switch-mode-sc", driverName)
						defer deleteStorageClass(f, sc.Name)

						pvc := createPVC(f, corev1.NamespaceDefault, "switch-mode-pvc", sc.Name)
						defer deletePVC(f, pvc.Namespace, pvc.Name)

						// Wait till a volume get provisioned for this claim
						err := e2epv.WaitForPersistentVolumeClaimPhase(corev1.ClaimBound, f.ClientSet, pvc.Namespace, pvc.Name, framework.Poll, framework.ClaimProvisionTimeout)
						Expect(err).NotTo(HaveOccurred(), "Persistent volume claim bound failure")

						pvc, err = f.ClientSet.CoreV1().PersistentVolumeClaims(pvc.Namespace).Get(context.Background(), pvc.Name, metav1.GetOptions{})
						Expect(err).NotTo(HaveOccurred(), "failed to get updated volume claim: %q", pvc.Name)
						framework.Logf("PVC '%s', Volume Ref: %s", pvc.Name, pvc.Spec.VolumeName)

						// Switch device mode
						deployment = switchDeploymentMode(c, f, deployment.Name, d.Namespace, to)

						postSwitch(from, to, driverName, pvc)

						deletePVC(f, pvc.Namespace, pvc.Name)
					})
				})
			}
		}

		defineSwitchModeTests("lvm-to-direct", api.DeviceModeLVM, api.DeviceModeDirect)
		defineSwitchModeTests("direct-to-lvm", api.DeviceModeDirect, api.DeviceModeLVM)
	})

	Context("updating", func() {
		for _, testcase := range testcases.UpdateTests() {
			testcase := testcase
			Context(testcase.Name, func() {
				testIt := func(restart bool) {
					deployment := *testcase.Deployment.DeepCopyObject().(*api.PmemCSIDeployment)

					// Use fake images to prevent pods from actually starting.
					deployment.Spec.Image = dummyImage
					deployment.Spec.NodeRegistrarImage = dummyImage
					deployment.Spec.ProvisionerImage = dummyImage

					deployment = deploy.CreateDeploymentCR(f, deployment)
					defer deploy.DeleteDeploymentCR(f, deployment.Name)
					validateDriver(deployment, nil, "validate driver before update")

					// We have to get a fresh copy before updating it because the
					// operator should have modified the status, and only the status.
					modifiedDeployment := deploy.GetDeploymentCR(f, deployment.Name)
					Expect(modifiedDeployment.Spec).To(Equal(deployment.Spec), "spec unmodified")
					Expect(modifiedDeployment.Status.Phase).To(Equal(api.DeploymentPhaseRunning), "deployment phase")

					resetMetricsURL := func() {
						url, err := deploy.GetOperatorMetricsURL(ctx, c, d)
						Expect(err).ShouldNot(HaveOccurred(), "get metrics url after operator restart")
						metricsURL = url
					}
					restored := false
					if restart {
						stopOperator(c, d)
						defer func() {
							if !restored {
								startOperator(c, d)
								resetMetricsURL()
							}
						}()
					}

					testcase.Mutate(&modifiedDeployment)
					deployment = deploy.UpdateDeploymentCR(f, modifiedDeployment)

					reconcileCount := lastReconcileCount
					if restart {
						startOperator(c, d)
						resetMetricsURL()
						restored = true
						reconcileCount = float64(0)
					}

					_, err := validate.WaitForDeploymentReconciled(ctx, c, metricsURL, deployment, reconcileCount)
					Expect(err).ShouldNot(HaveOccurred(), "reconcile after operator restart")
					err = validate.DriverDeployment(ctx, client, k8sver, d.Namespace, deployment)
					Expect(err).ShouldNot(HaveOccurred(), "validate driver after update and restart")
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

	Context("recover", func() {
		Context("deleted sub-resources", func() {
			tests := map[string]func(*api.PmemCSIDeployment) runtime.Object{
				"provisioner service account": func(dep *api.PmemCSIDeployment) runtime.Object {
					return &corev1.ServiceAccount{
						ObjectMeta: metav1.ObjectMeta{Name: dep.ProvisionerServiceAccountName(), Namespace: d.Namespace},
					}
				},
				"webhooks service account": func(dep *api.PmemCSIDeployment) runtime.Object {
					return &corev1.ServiceAccount{
						ObjectMeta: metav1.ObjectMeta{Name: dep.WebhooksServiceAccountName(), Namespace: d.Namespace},
					}
				},
				"scheduler service": func(dep *api.PmemCSIDeployment) runtime.Object {
					return &corev1.Service{
						ObjectMeta: metav1.ObjectMeta{Name: dep.SchedulerServiceName(), Namespace: d.Namespace},
					}
				},
				"controller service": func(dep *api.PmemCSIDeployment) runtime.Object {
					return &corev1.Service{
						ObjectMeta: metav1.ObjectMeta{Name: dep.ControllerServiceName(), Namespace: d.Namespace},
					}
				},
				"metrics service": func(dep *api.PmemCSIDeployment) runtime.Object {
					return &corev1.Service{
						ObjectMeta: metav1.ObjectMeta{Name: dep.MetricsServiceName(), Namespace: d.Namespace},
					}
				},
				"webhooks role": func(dep *api.PmemCSIDeployment) runtime.Object {
					return &rbacv1.Role{
						ObjectMeta: metav1.ObjectMeta{Name: dep.WebhooksRoleName(), Namespace: d.Namespace},
					}
				},
				"webhooks role binding": func(dep *api.PmemCSIDeployment) runtime.Object {
					return &rbacv1.RoleBinding{
						ObjectMeta: metav1.ObjectMeta{Name: dep.WebhooksRoleBindingName(), Namespace: d.Namespace},
					}
				},
				"webhooks cluster role": func(dep *api.PmemCSIDeployment) runtime.Object {
					return &rbacv1.ClusterRole{
						ObjectMeta: metav1.ObjectMeta{Name: dep.WebhooksClusterRoleName()},
					}
				},
				"webhooks cluster role binding": func(dep *api.PmemCSIDeployment) runtime.Object {
					return &rbacv1.ClusterRoleBinding{
						ObjectMeta: metav1.ObjectMeta{Name: dep.WebhooksClusterRoleBindingName()},
					}
				},
				"mutating webhook config": func(dep *api.PmemCSIDeployment) runtime.Object {
					return &admissionregistrationv1.MutatingWebhookConfiguration{
						ObjectMeta: metav1.ObjectMeta{Name: dep.MutatingWebhookName()},
					}
				},
				"provisioner role": func(dep *api.PmemCSIDeployment) runtime.Object {
					return &rbacv1.Role{
						ObjectMeta: metav1.ObjectMeta{Name: dep.ProvisionerRoleName(), Namespace: d.Namespace},
					}
				},
				"provisioner role binding": func(dep *api.PmemCSIDeployment) runtime.Object {
					return &rbacv1.RoleBinding{
						ObjectMeta: metav1.ObjectMeta{Name: dep.ProvisionerRoleBindingName(), Namespace: d.Namespace},
					}
				},
				"provisioner cluster role": func(dep *api.PmemCSIDeployment) runtime.Object {
					return &rbacv1.ClusterRole{
						ObjectMeta: metav1.ObjectMeta{Name: dep.ProvisionerClusterRoleName()},
					}
				},
				"provisioner cluster role binding": func(dep *api.PmemCSIDeployment) runtime.Object {
					return &rbacv1.ClusterRoleBinding{
						ObjectMeta: metav1.ObjectMeta{Name: dep.ProvisionerClusterRoleBindingName()},
					}
				},
				"csi driver": func(dep *api.PmemCSIDeployment) runtime.Object {
					return &storagev1.CSIDriver{
						ObjectMeta: metav1.ObjectMeta{Name: dep.GetName()},
					}
				},
				"controller driver": func(dep *api.PmemCSIDeployment) runtime.Object {
					return &appsv1.StatefulSet{
						ObjectMeta: metav1.ObjectMeta{Name: dep.ControllerDriverName(), Namespace: d.Namespace},
					}
				},
				"node driver": func(dep *api.PmemCSIDeployment) runtime.Object {
					return &appsv1.DaemonSet{
						ObjectMeta: metav1.ObjectMeta{Name: dep.NodeDriverName(), Namespace: d.Namespace},
					}
				},
			}

			delete := func(obj runtime.Object) {
				Eventually(func() error {
					err := client.Delete(context.TODO(), obj)
					if err == nil || errors.IsNotFound(err) {
						return nil
					}
					return err
				}, "3m", "1s").ShouldNot(HaveOccurred(), "delete object '%T/%s", obj, obj.GetName())
				framework.Logf("Deleted object %T/%s", obj, obj.GetName())
			}
			for name, getter := range tests {
				name, getter := name, getter
				It(name, func() {
					// Create a deployment with controller and webhook config.
					dep := getDeployment("recover-" + strings.ReplaceAll(name, " ", "-"))
					dep.Spec.ControllerTLSSecret = "pmem-csi-intel-com-controller-secret"
					dep.Spec.MutatePods = api.MutatePodsAlways
					deployment := deploy.CreateDeploymentCR(f, dep)
					defer deploy.DeleteDeploymentCR(f, dep.Name)
					validateDriver(deployment, nil)

					obj := getter(&dep)
					delete(obj)
					ensureObjectRecovered(obj)
					validateDriver(deployment, nil, "restore deleted registry secret")
				})
			}
		})

		Context("conflicting update", func() {
			tests := map[string]func(dep *api.PmemCSIDeployment) runtime.Object{
				"controller": func(dep *api.PmemCSIDeployment) runtime.Object {
					obj := &appsv1.StatefulSet{}
					key := runtime.ObjectKey{Name: dep.ControllerDriverName(), Namespace: d.Namespace}
					EventuallyWithOffset(1, func() error {
						return client.Get(context.TODO(), key, obj)
					}, "2m", "1s").ShouldNot(HaveOccurred(), "get stateful set")

					for i, container := range obj.Spec.Template.Spec.Containers {
						if container.Name == "pmem-driver" {
							obj.Spec.Template.Spec.Containers[i].Command = []string{"malformed", "options"}
							break
						}
					}
					return obj
				},
				"node driver": func(dep *api.PmemCSIDeployment) runtime.Object {
					obj := &appsv1.DaemonSet{}
					key := runtime.ObjectKey{Name: dep.NodeDriverName(), Namespace: d.Namespace}
					EventuallyWithOffset(1, func() error {
						return client.Get(context.TODO(), key, obj)
					}, "2m", "1s").ShouldNot(HaveOccurred(), "get daemon set")

					for i, container := range obj.Spec.Template.Spec.Containers {
						if container.Name == "pmem-driver" {
							obj.Spec.Template.Spec.Containers[i].Command = []string{"malformed", "options"}
							break
						}
					}
					return obj
				},
				"metrics service": func(dep *api.PmemCSIDeployment) runtime.Object {
					obj := &corev1.Service{}
					key := runtime.ObjectKey{Name: dep.MetricsServiceName(), Namespace: d.Namespace}
					EventuallyWithOffset(1, func() error {
						return client.Get(context.TODO(), key, obj)
					}, "2m", "1s").ShouldNot(HaveOccurred(), "get metrics service set")
					obj.Spec.Ports = []corev1.ServicePort{
						{
							Port: 1111,
							TargetPort: intstr.IntOrString{
								IntVal: 1111,
							},
						},
					}
					return obj
				},
				"controller service": func(dep *api.PmemCSIDeployment) runtime.Object {
					obj := &corev1.Service{}
					key := runtime.ObjectKey{Name: dep.ControllerServiceName(), Namespace: d.Namespace}
					EventuallyWithOffset(1, func() error {
						return client.Get(context.TODO(), key, obj)
					}, "2m", "1s").ShouldNot(HaveOccurred(), "get metrics service set")

					obj.Spec.Ports = []corev1.ServicePort{
						{
							Port: 1111,
							TargetPort: intstr.IntOrString{
								IntVal: 1111,
							},
						},
					}
					return obj
				},
			}
			for name, mutate := range tests {
				name, mutate := name, mutate
				It(name, func() {
					dep := getDeployment("recover-" + strings.ReplaceAll(name, " ", "-"))
					dep.Spec.ControllerTLSSecret = "pmem-csi-intel-com-controller-secret"
					deployment := deploy.CreateDeploymentCR(f, dep)
					defer deploy.DeleteDeploymentCR(f, dep.Name)
					validateDriver(deployment, nil, "validate before conflicts")

					obj := mutate(&deployment)

					Eventually(func() error {
						err := client.Update(context.TODO(), obj)
						if err != nil && errors.IsConflict(err) {
							obj = mutate(&deployment)
						}
						return err
					}, "2m", "1s").ShouldNot(HaveOccurred(), "update: %s", name)

					validateDriver(deployment, []runtime.Object{obj}, fmt.Sprintf("recovered %s", name))
				})
			}
		})
	})

	Context("validation", func() {
		versions := map[string]schema.GroupVersionResource{
			"v1beta1": deploy.DeploymentResource,
		}
		for version, gvr := range versions {
			gvr := gvr
			Context(version, func() {
				toUnstructured := func(in interface{}) *unstructured.Unstructured {
					var out unstructured.Unstructured
					err := deploy.Scheme.Convert(in, &out, nil)
					framework.ExpectNoError(err, "convert to unstructured deployment")
					gvk := schema.GroupVersionKind{
						Group:   gvr.Group,
						Version: gvr.Version,
						Kind:    "Deployment",
					}
					if version == "v1beta1" {
						gvk.Kind = "PmemCSIDeployment"
					}
					out.SetGroupVersionKind(gvk)
					return &out
				}

				Context("PMEMPercentage", func() {
					cases := map[string]uint16{
						"okay":                      50,
						"must be greater than 0":    0,
						"must be less or equal 100": 101,
					}
					for name, percentage := range cases {
						percentage := percentage
						It(name, func() {
							dep := getDeployment("percentage")
							dep.Spec.PMEMPercentage = percentage
							unstructured := toUnstructured(&dep)
							_, err := f.DynamicClient.Resource(gvr).Create(context.Background(), unstructured, metav1.CreateOptions{})
							if err == nil {
								defer deploy.DeleteDeploymentCR(f, dep.Name)
							}
							// Zero is actually a valid value: it is treated as "unset" because of
							// the JSON omitempty and thus means "I want the default".
							valid := percentage >= 0 && percentage <= 100
							if valid {
								framework.ExpectNoError(err, "valid percentage should work")
							} else {
								framework.ExpectError(err, "invalid percentage should be rejected")
							}
						})
					}
				})

				Context("DeviceMode", func() {
					cases := map[string]bool{
						string(api.DeviceModeLVM):    true,
						string(api.DeviceModeDirect): true,
						"noSuchMode":                 false,
					}
					for mode, valid := range cases {
						mode, valid := mode, valid
						It(mode, func() {
							dep := getDeployment("mode")
							dep.Spec.DeviceMode = api.DeviceMode(mode)
							unstructured := toUnstructured(&dep)
							_, err := f.DynamicClient.Resource(gvr).Create(context.Background(), unstructured, metav1.CreateOptions{})
							if err == nil {
								defer deploy.DeleteDeploymentCR(f, dep.Name)
							}
							if valid {
								framework.ExpectNoError(err, "valid device mode should work")
							} else {
								framework.ExpectError(err, "invalid device mode should be rejected")
							}
						})
					}
				})

				Context("LogFormat", func() {
					cases := map[string]bool{
						string(api.LogFormatText): true,
						string(api.LogFormatJSON): true,
						"noSuchMode":              false,
					}
					for format, valid := range cases {
						format, valid := format, valid
						It(format, func() {
							dep := getDeployment("format")
							dep.Spec.LogFormat = api.LogFormat(format)
							unstructured := toUnstructured(&dep)
							_, err := f.DynamicClient.Resource(gvr).Create(context.Background(), unstructured, metav1.CreateOptions{})
							if err == nil {
								defer deploy.DeleteDeploymentCR(f, dep.Name)
							}
							if valid {
								framework.ExpectNoError(err, "valid log format should work")
							} else {
								framework.ExpectError(err, "invalid log format should be rejected")
							}
						})
					}
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
		_, err := c.GetAppInstance(ctx, labels.Set{"app": "pmem-csi-operator"}, "", d.Namespace)
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

	framework.Logf("Ensure operator pod is ready.")
	deploy.WaitForOperator(c, d.Namespace)
	framework.Logf("Operator is restored!")
}

func switchDeploymentMode(c *deploy.Cluster, f *framework.Framework, depName, ns string, mode api.DeviceMode) api.PmemCSIDeployment {
	podNames := []string{}

	for i := 1; i < c.NumNodes(); i++ {
		Eventually(func() error {
			pod, err := c.GetAppInstance(context.Background(),
				labels.Set{"app.kubernetes.io/name": "pmem-csi-node",
					"app.kubernetes.io/instance": depName},
				c.NodeIP(i), ns)
			if err != nil {
				return err
			}
			podNames = append(podNames, pod.Name)
			return nil
		}, "3m", "1s").ShouldNot(HaveOccurred(), "Get daemonset pods")
	}
	By(fmt.Sprintf("Switching driver mode to '%s'", mode))
	deployment := deploy.GetDeploymentCR(f, depName)
	deployment.Spec.DeviceMode = mode
	deployment = deploy.UpdateDeploymentCR(f, deployment)

	// Wait till all the existing daemonset pods restarted
	for _, pod := range podNames {
		Eventually(func() bool {
			_, err := f.ClientSet.CoreV1().Pods(ns).Get(context.Background(), pod, metav1.GetOptions{})
			if err != nil && errors.IsNotFound(err) {
				return true
			}
			deploy.LogError(err, "Failed to fetch daemon set: %v", err)
			return false
		}, "3m", "1s").Should(BeTrue(), "Pod restart '%s'", pod)
	}

	deploy.WaitForPMEMDriver(c,
		&deploy.Deployment{
			Namespace:  ns,
			DriverName: depName,
		})

	return deployment
}

func createStorageClass(f *framework.Framework, name, provisioner string) *storagev1.StorageClass {
	reclaim := corev1.PersistentVolumeReclaimDelete
	immediate := storagev1.VolumeBindingImmediate

	// Create storage class
	sc := &storagev1.StorageClass{
		ObjectMeta: metav1.ObjectMeta{
			Name: name,
		},
		Provisioner:       provisioner,
		ReclaimPolicy:     &reclaim,
		VolumeBindingMode: &immediate,
		Parameters: map[string]string{
			"eraseafter": "false",
		},
	}

	EventuallyWithOffset(1, func() error {
		_, err := f.ClientSet.StorageV1().StorageClasses().Create(context.Background(), sc, metav1.CreateOptions{})
		if err == nil || errors.IsAlreadyExists(err) {
			return nil
		}
		deploy.LogError(err, "create storage class error: %v, will retry...", err)
		return err
	}, "3m", "1s").ShouldNot(HaveOccurred(), "create storage class %q", sc.Name)
	framework.Logf("Created storage class %q", sc.Name)

	return sc
}

func deleteStorageClass(f *framework.Framework, name string) {
	EventuallyWithOffset(1, func() error {
		framework.Logf("deleting storage class %q", name)
		err := f.ClientSet.StorageV1().StorageClasses().Delete(context.Background(), name, metav1.DeleteOptions{})
		if err != nil && errors.IsNotFound(err) {
			return nil
		}
		deploy.LogError(err, "delete storage class error: %v, will retry...", err)
		return err
	}, "3m", "1s").ShouldNot(HaveOccurred(), "delete storage class %q", name)
}

func createPVC(f *framework.Framework, namespace, name, storageClassName string) *corev1.PersistentVolumeClaim {
	// Create a volume
	pvc := &corev1.PersistentVolumeClaim{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "switch-mode-pvc",
			Namespace: namespace,
		},
		Spec: corev1.PersistentVolumeClaimSpec{
			StorageClassName: &storageClassName,
			AccessModes: []corev1.PersistentVolumeAccessMode{
				corev1.ReadWriteOnce,
			},
			Resources: corev1.ResourceRequirements{
				Requests: corev1.ResourceList{
					corev1.ResourceStorage: resource.MustParse("2Gi"),
				},
			},
		},
	}
	EventuallyWithOffset(1, func() error {
		_, err := f.ClientSet.CoreV1().PersistentVolumeClaims(pvc.Namespace).Create(context.Background(), pvc, metav1.CreateOptions{})
		deploy.LogError(err, "create pvc %q error: %v, will retry...", pvc.Name, err)
		return err
	}, "3m", "1s").ShouldNot(HaveOccurred(), "create pvc %q", pvc.Name)
	framework.Logf("Created pvc %q", pvc.Name)

	return pvc
}

func deletePVC(f *framework.Framework, namespace, name string) {
	framework.Logf("Deleting PVC %q", name)
	pvc, err := f.ClientSet.CoreV1().PersistentVolumeClaims(namespace).Get(context.Background(), name, metav1.GetOptions{})
	if err != nil {
		ExpectWithOffset(1, errors.IsNotFound(err)).Should(BeTrue(), "Get PVC '%s'", name)
	}

	pvName := pvc.Spec.VolumeName
	framework.Logf("Pv %q bound for PVC %q", pvName, name)

	EventuallyWithOffset(1, func() error {
		err := f.ClientSet.CoreV1().PersistentVolumeClaims(namespace).Delete(context.Background(), name, metav1.DeleteOptions{})
		if err != nil && errors.IsNotFound(err) {
			return nil
		}
		deploy.LogError(err, "delete pvc error: %v, will retry...", err)
		return err
	}, "3m", "1s").ShouldNot(HaveOccurred(), "delete pvc %q", name)
	framework.Logf("PVC deleted %q", name)

	if pvName == "" {
		return
	}
	// Wait till the underlined volume get deleted
	// as we use the reclaim policy delete
	framework.Logf("Waiting for PV %q get deleted", pvName)
	EventuallyWithOffset(1, func() bool {
		_, err := f.ClientSet.CoreV1().PersistentVolumes().Get(context.Background(), pvName, metav1.GetOptions{})
		deploy.LogError(err, "Get PV '%s' error: %v, will retry...", pvName, err)
		if err != nil && errors.IsNotFound(err) {
			return true
		}
		return false
	}, "3m", "1s").Should(BeTrue(), "Get PV '%s'", pvName)
}
