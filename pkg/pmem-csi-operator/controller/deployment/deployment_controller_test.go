/*
Copyright 2019  Intel Corporation.

SPDX-License-Identifier: Apache-2.0
*/
package deployment_test

import (
	"context"
	"encoding/base64"
	"fmt"
	"testing"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"

	"github.com/intel/pmem-csi/pkg/apis"
	api "github.com/intel/pmem-csi/pkg/apis/pmemcsi/v1alpha1"
	pmemcontroller "github.com/intel/pmem-csi/pkg/pmem-csi-operator/controller"
	"github.com/intel/pmem-csi/pkg/pmem-csi-operator/controller/deployment"
	pmemtls "github.com/intel/pmem-csi/pkg/pmem-csi-operator/pmem-tls"
	"github.com/intel/pmem-csi/pkg/pmem-csi-operator/version"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	storagev1beta1 "k8s.io/api/storage/v1beta1"
	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/klog"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

var (
	k8sVersion_116 = version.NewVersion(1, 16)
	k8sVersion_115 = version.NewVersion(1, 15)
)

func TestDeploymentController(t *testing.T) {
	RegisterFailHandler(Fail)

	err := apis.AddToScheme(scheme.Scheme)
	Expect(err).Should(BeNil(), "Failed to add api schema")

	RunSpecs(t, "PMEM Operator API test suite")
}

type pmemDeployment struct {
	name                                                string
	driverName                                          string
	logLevel                                            uint16
	image, pullPolicy, provisionerImage, registrarImage string
	controllerCPU, controllerMemory                     string
	nodeCPU, nodeMemory                                 string
	caCert, regCert, regKey, ncCert, ncKey              []byte
}

func decodeYaml(deploymentYaml string) runtime.Object {
	klog.Info(deploymentYaml)
	decode := scheme.Codecs.UniversalDeserializer().Decode

	obj, _, err := decode([]byte(deploymentYaml), nil, nil)
	Expect(err).Should(BeNil(), "Failed to parse deployment")
	Expect(obj).ShouldNot(BeNil(), "Nil deployment object")

	return obj
}

func getDeployment(d *pmemDeployment) runtime.Object {
	yaml := fmt.Sprintf(`kind: Deployment
apiVersion: pmem-csi.intel.com/v1alpha1
metadata:
  name: %s
`, d.name)
	yaml += "spec:\n"
	if d.driverName != "" {
		yaml += "  driverName: " + d.driverName + "\n"
	}
	if d.logLevel != 0 {
		yaml += fmt.Sprintf("  logLevel: %d\n", d.logLevel)
	}
	if d.image != "" {
		yaml += "  image: " + d.image + "\n"
	}
	if d.pullPolicy != "" {
		yaml += "  imagePullPolicy: " + d.pullPolicy + "\n"
	}
	if d.provisionerImage != "" {
		yaml += "  provisionerImage: " + d.provisionerImage + "\n"
	}
	if d.registrarImage != "" {
		yaml += "  nodeRegistrarImage: " + d.registrarImage + "\n"
	}
	if d.controllerCPU != "" || d.controllerMemory != "" {
		yaml += "  controllerResources:\n"
		yaml += "    requests:\n"
		if d.controllerCPU != "" {
			yaml += "      cpu: " + d.controllerCPU + "\n"
		}
		if d.controllerMemory != "" {
			yaml += "      memory: " + d.controllerMemory + "\n"
		}
	}

	if d.nodeCPU != "" || d.nodeMemory != "" {
		yaml += "  nodeResources:\n"
		yaml += "    requests:\n"
		if d.controllerCPU != "" {
			yaml += "      cpu: " + d.nodeCPU + "\n"
		}
		if d.controllerMemory != "" {
			yaml += "      memory: " + d.nodeMemory + "\n"
		}
	}

	if d.caCert != nil {
		yaml += "  caCert: " + base64.StdEncoding.EncodeToString(d.caCert) + "\n"
	}
	if d.regCert != nil {
		yaml += "  registryCert: " + base64.StdEncoding.EncodeToString(d.regCert) + "\n"
	}
	if d.regKey != nil {
		yaml += "  registryKey: " + base64.StdEncoding.EncodeToString(d.regKey) + "\n"
	}
	if d.ncCert != nil {
		yaml += "  nodeControllerCert: " + base64.StdEncoding.EncodeToString(d.ncCert) + "\n"
	}
	if d.ncKey != nil {
		yaml += "  nodeControllerKey: " + base64.StdEncoding.EncodeToString(d.ncKey) + "\n"
	}

	return decodeYaml(yaml)
}

func testDeploymentPhase(c client.Client, name string, expectedPhase api.DeploymentPhase) {
	depObject := &api.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name: name,
		},
	}
	err := c.Get(context.TODO(), namespacedName(depObject), depObject)
	Expect(err).Should(BeNil(), "failed to retrive deployment object")
	Expect(depObject.Status.Phase).Should(BeEquivalentTo(expectedPhase), "Unexpected status phase")
}

func testReconcile(rc reconcile.Reconciler, name string, expectErr bool, expectedRequeue bool) {
	req := reconcile.Request{
		NamespacedName: types.NamespacedName{
			Name: name,
		},
	}
	resp, err := rc.Reconcile(req)
	if expectErr {
		Expect(err).ShouldNot(BeNil(), "expected reconcile failure")
	} else {
		Expect(err).Should(BeNil(), "reconcile failed with error")
	}
	Expect(resp.Requeue).Should(BeEquivalentTo(expectedRequeue), "expected requeue reconcile")
}

func testReconcilePhase(rc reconcile.Reconciler, c client.Client, name string, expectErr bool, expectedRequeue bool, expectedPhase api.DeploymentPhase) {
	testReconcile(rc, name, expectErr, expectedRequeue)
	testDeploymentPhase(c, name, expectedPhase)
}

func validateSecrets(c client.Client, d *pmemDeployment) {
	for _, name := range []string{"pmem-registry", "pmem-node-controller", "pmem-ca"} {
		secretName := d.name + "-" + name
		s := &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{
				Name: secretName,
			},
		}
		err := c.Get(context.TODO(), namespacedName(s), s)
		Expect(err).Should(BeNil(), "failed to get secret for %q", secretName)
		Expect(s.Data[corev1.TLSCertKey]).ShouldNot(BeNil(), "certificate not present in secret %s", secretName)
		if name != "pmem-ca" {
			Expect(s.Data[corev1.TLSPrivateKeyKey]).ShouldNot(BeNil(), "private key not present in secret %s", secretName)
		}
	}
}

func validateCSIDriver(c client.Client, driverName string, k8sVersion *version.Version) {
	driver := &storagev1beta1.CSIDriver{
		ObjectMeta: metav1.ObjectMeta{
			Name: driverName,
		},
	}

	err := c.Get(context.TODO(), namespacedName(driver), driver)
	Expect(err).Should(BeNil(), "could not find csidriver object")

	if k8sVersion.Compare(1, 16) >= 0 {
		Expect(len(driver.Spec.VolumeLifecycleModes)).Should(Equal(2), "mismatched lifecycle modes")
		Expect(driver.Spec.VolumeLifecycleModes).Should(ContainElement(storagev1beta1.VolumeLifecyclePersistent), "Persisten volume mode not preset")
		Expect(driver.Spec.VolumeLifecycleModes).Should(ContainElement(storagev1beta1.VolumeLifecycleEphemeral), "Ephemeral volume mode not preset")
	}
}

func namespacedName(obj runtime.Object) types.NamespacedName {
	metaObj, err := meta.Accessor(obj)
	Expect(err).Should(BeNil(), "failed to get accessor")

	return types.NamespacedName{Name: metaObj.GetName(), Namespace: metaObj.GetNamespace()}
}

var _ = Describe("Operator", func() {
	Context("Deployment Controller", func() {
		var c client.Client
		var rc reconcile.Reconciler
		var testNamespace = "test-namespace"
		var testK8sVersion = k8sVersion_116

		BeforeEach(func() {
			c = fake.NewFakeClient()

			rc = deployment.NewReconcileDeployment(c, pmemcontroller.ControllerOptions{
				Namespace:  testNamespace,
				K8sVersion: k8sVersion_116,
			})
		})

		AfterEach(func() {
		})

		It("shall allow deployment with defaults", func() {
			d := &pmemDeployment{
				name: "test-deployment",
			}

			dep := getDeployment(d)

			err := c.Create(context.TODO(), dep)
			Expect(err).Should(BeNil(), "failed to create deployment")

			testReconcilePhase(rc, c, d.name, false, true, api.DeploymentPhaseInitializing)

			validateSecrets(c, d)

			// Reconcile now should change Phase to running
			testReconcilePhase(rc, c, d.name, false, false, api.DeploymentPhaseRunning)

			validateCSIDriver(c, api.DefaultDriverName, testK8sVersion)

			ss := &appsv1.StatefulSet{}
			err = c.Get(context.TODO(), namespacedName(ss), ss)
			Expect(err).Should(BeNil(), "controller stateful set is expected on a successful deployment")
			Expect(*ss.Spec.Replicas).Should(BeEquivalentTo(1), "controller stateful set replication count mismatch")
			Expect(ss.ObjectMeta.Namespace).Should(BeEquivalentTo(testNamespace), "controller stateful set namespace mismatch")
			containers := ss.Spec.Template.Spec.Containers
			Expect(len(containers)).Should(BeEquivalentTo(2), "controller stateful set container count mismatch")
			for _, c := range containers {
				Expect(c.ImagePullPolicy).Should(BeEquivalentTo(api.DefaultImagePullPolicy), "pmem-driver: mismatched image pull policy")
				Expect(c.Resources.Limits.Cpu().String()).Should(BeEquivalentTo(api.DefaultControllerResourceCPU), "controller cpu resource limit mismatch")
				Expect(c.Resources.Limits.Memory().String()).Should(BeEquivalentTo(api.DefaultControllerResourceMemory), "controller memory resource limit mismatch")
				switch c.Name {
				case "pmem-driver":
					Expect(c.Image).Should(BeEquivalentTo(api.DefaultDriverImage), "mismatched driver image")
					Expect(c.Args).Should(ContainElement("-drivername="+api.DefaultDriverName), "mismatched driver name")
					Expect(c.Args).Should(ContainElement(fmt.Sprintf("-v=%d", api.DefaultLogLevel)), "mismatched logging level")
				case "provisioner":
					Expect(c.Image).Should(BeEquivalentTo(api.DefaultProvisionerImage), "mismatched provisioner image")
				default:
					Fail(fmt.Sprintf("Unknown container name %q in controller stateful set", c.Name))
				}
			}

			ds := &appsv1.DaemonSet{}
			err = c.Get(context.TODO(), namespacedName(ds), ds)

			Expect(err).Should(BeNil(), "node daemon set is expected on a successful deployment")
			Expect(ds.ObjectMeta.Namespace).Should(BeEquivalentTo(testNamespace), "node daemon set namespace mismatch")
			containers = ds.Spec.Template.Spec.Containers
			Expect(len(containers)).Should(BeEquivalentTo(2), "node daemon set container count mismatch")
			for _, c := range containers {
				Expect(c.ImagePullPolicy).Should(BeEquivalentTo(api.DefaultImagePullPolicy), "pmem-driver: mismatched image pull policy")
				Expect(c.Resources.Limits.Cpu().String()).Should(BeEquivalentTo(api.DefaultNodeResourceCPU), "node cpu resource limit mismatch")
				Expect(c.Resources.Limits.Memory().String()).Should(BeEquivalentTo(api.DefaultNodeResourceMemory), "node memory resource limit mismatch")
				switch c.Name {
				case "pmem-driver":
					Expect(c.Image).Should(BeEquivalentTo(api.DefaultDriverImage), "mismatched driver image")
					Expect(c.Args).Should(ContainElement("-drivername="+api.DefaultDriverName), "mismatched driver name")
					Expect(c.Args).Should(ContainElement(fmt.Sprintf("-v=%d", api.DefaultLogLevel)), "mismatched logging level")
				case "driver-registrar":
					Expect(c.Image).Should(BeEquivalentTo(api.DefaultRegistrarImage), "mismatched driver-registrar image")
				default:
					Fail(fmt.Sprintf("Unknown container name %q in controller stateful set", c.Name))
				}
			}
		})

		It("shall allow deployment with explicit values", func() {
			d := &pmemDeployment{
				name:             "test-deployment",
				driverName:       "test-pmem-driver.intel.com",
				image:            "test-driver:v0.0.0",
				provisionerImage: "test-provisioner-image:v0.0.0",
				registrarImage:   "test-driver-registrar-image:v.0.0.0",
				pullPolicy:       "Never",
				logLevel:         10,
				controllerCPU:    "1500m",
				controllerMemory: "300Mi",
				nodeCPU:          "1000m",
				nodeMemory:       "500Mi",
			}

			dep := getDeployment(d)
			err := c.Create(context.TODO(), dep)
			Expect(err).Should(BeNil(), "failed to create deployment")

			testReconcilePhase(rc, c, d.name, false, true, api.DeploymentPhaseInitializing)

			validateSecrets(c, d)

			// Reconcile now should change Phase to running
			testReconcilePhase(rc, c, d.name, false, false, api.DeploymentPhaseRunning)

			validateCSIDriver(c, d.driverName, testK8sVersion)

			ss := &appsv1.StatefulSet{}
			err = c.Get(context.TODO(), namespacedName(ss), ss)

			Expect(err).Should(BeNil(), "controller stateful set is expected on a successful deployment")
			Expect(*ss.Spec.Replicas).Should(BeEquivalentTo(1), "controller stateful set replication count mismatch")
			Expect(ss.ObjectMeta.Namespace).Should(BeEquivalentTo(testNamespace), "controller stateful set namespace mismatch")
			containers := ss.Spec.Template.Spec.Containers
			Expect(len(containers)).Should(BeEquivalentTo(2), "controller stateful set container count mismatch")
			for _, c := range containers {
				Expect(c.ImagePullPolicy).Should(BeEquivalentTo(d.pullPolicy), "pmem-driver: mismatched image pull policy")
				Expect(c.Resources.Requests.Cpu().Cmp(resource.MustParse(d.controllerCPU))).Should(BeZero(), "controller cpu resource limit mismatch")
				Expect(c.Resources.Requests.Memory().Cmp(resource.MustParse(d.controllerMemory))).Should(BeZero(), "controller memory resource limit mismatch")
				switch c.Name {
				case "pmem-driver":
					Expect(c.Image).Should(BeEquivalentTo(d.image), "mismatched driver image")
					Expect(c.Args).Should(ContainElement("-drivername="+d.driverName), "mismatched driver name")
					Expect(c.Args).Should(ContainElement(fmt.Sprintf("-v=%d", d.logLevel)), "mismatched logging level")
				case "provisioner":
					Expect(c.Image).Should(BeEquivalentTo(d.provisionerImage), "mismatched provisioner image")
				default:
					Fail(fmt.Sprintf("Unknown container name %q in controller stateful set", c.Name))
				}
			}

			ds := &appsv1.DaemonSet{}
			err = c.Get(context.TODO(), namespacedName(ds), ds)

			Expect(err).Should(BeNil(), "node daemon set is expected on a successful deployment")
			Expect(ds.ObjectMeta.Namespace).Should(BeEquivalentTo(testNamespace), "node daemon set namespace mismatch")
			containers = ds.Spec.Template.Spec.Containers
			Expect(len(containers)).Should(BeEquivalentTo(2), "node daemon set container count mismatch")
			for _, c := range containers {
				Expect(c.ImagePullPolicy).Should(BeEquivalentTo(d.pullPolicy), "pmem-driver: mismatched image pull policy")
				Expect(c.Resources.Requests.Cpu().Cmp(resource.MustParse(d.nodeCPU))).Should(BeZero(), "node cpu resource limit mismatch")
				Expect(c.Resources.Requests.Memory().Cmp(resource.MustParse(d.nodeMemory))).Should(BeZero(), "node memory resource limit mismatch")
				switch c.Name {
				case "pmem-driver":
					Expect(c.Image).Should(BeEquivalentTo(d.image), "mismatched driver image")
					Expect(c.Args).Should(ContainElement("-drivername="+d.driverName), "mismatched driver name")
					Expect(c.Args).Should(ContainElement(fmt.Sprintf("-v=%d", d.logLevel)), "mismatched logging level")
				case "driver-registrar":
					Expect(c.Image).Should(BeEquivalentTo(d.registrarImage), "mismatched driver-registrar image")
				default:
					Fail(fmt.Sprintf("Unknown container name %q in controller stateful set", c.Name))
				}
			}
		})

		It("shall allow multiple deployments", func() {
			d1 := &pmemDeployment{
				name:       "test-deployment1",
				driverName: "test-pmem-driver1.intel.com",
			}

			d2 := &pmemDeployment{
				name:       "test-deployment2",
				driverName: "test-pmem-driver2.intel.com",
			}

			dep := getDeployment(d1)
			err := c.Create(context.TODO(), dep)
			Expect(err).Should(BeNil(), "failed to create deployment1")

			dep = getDeployment(d2)
			err = c.Create(context.TODO(), dep)
			Expect(err).Should(BeNil(), "failed to create deployment2")

			testReconcilePhase(rc, c, d1.name, false, true, api.DeploymentPhaseInitializing)

			testReconcilePhase(rc, c, d2.name, false, true, api.DeploymentPhaseInitializing)

		})

		It("shall not allow multiple deployments with same driver name", func() {
			d1 := &pmemDeployment{
				name:       "test-deployment1",
				driverName: "test-pmem-driver.intel.com",
			}

			d2 := &pmemDeployment{
				name:       "test-deployment2",
				driverName: "test-pmem-driver.intel.com",
			}

			dep := getDeployment(d1)
			err := c.Create(context.TODO(), dep)
			Expect(err).Should(BeNil(), "failed to create deployment1")

			dep = getDeployment(d2)
			err = c.Create(context.TODO(), dep)
			Expect(err).Should(BeNil(), "failed to create deployment2")

			// First deployment expected to be successful
			testReconcilePhase(rc, c, d1.name, false, true, api.DeploymentPhaseInitializing)
			// Second deployment expected to fail as one more deployment is active with the
			// same driver name
			testReconcilePhase(rc, c, d2.name, true, true, api.DeploymentPhaseFailed)

			// Delete fist deployment
			dep1 := &api.Deployment{
				ObjectMeta: metav1.ObjectMeta{
					Name: d1.name,
				},
			}
			err = c.Delete(context.TODO(), dep1)

			Expect(err).Should(BeNil(), "Failed to delete deployment in failed state")

			testReconcile(rc, d1.name, false, false)

			// After removing first deployment, sencond deployment should progress
			testReconcilePhase(rc, c, d2.name, false, true, api.DeploymentPhaseInitializing)
			testReconcilePhase(rc, c, d2.name, false, false, api.DeploymentPhaseRunning)

			validateCSIDriver(c, d2.driverName, testK8sVersion)
		})

		It("shall use provided private keys", func() {
			// Generate private key
			regKey, err := pmemtls.NewPrivateKey()
			Expect(err).Should(BeNil(), "Failed to generate a private key: %v", err)

			encodedKey := pmemtls.EncodeKey(regKey)

			d := &pmemDeployment{
				name:       "test-deployment",
				driverName: "test-pmem-driver.intel.com",
				regKey:     encodedKey,
			}
			dep := getDeployment(d)
			err = c.Create(context.TODO(), dep)
			Expect(err).Should(BeNil(), "failed to create deployment")

			// First deployment expected to be successful
			testReconcilePhase(rc, c, d.name, false, true, api.DeploymentPhaseInitializing)

			validateSecrets(c, d)

			s := &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name: d.name + "-pmem-registry",
				},
			}
			err = c.Get(context.TODO(), namespacedName(s), s)
			Expect(err).Should(BeNil(), "failed to get registry secret: %v", err)
			Expect(s.Data[corev1.TLSPrivateKeyKey]).Should(Equal(encodedKey), "mismatched private key")
		})

		It("shall allow to change running deployment name", func() {
			d := &pmemDeployment{
				name: "test-deployment",
			}

			err := c.Create(context.TODO(), getDeployment(d))
			Expect(err).Should(BeNil(), "failed to create deployment")

			testReconcilePhase(rc, c, d.name, false, true, api.DeploymentPhaseInitializing)

			validateSecrets(c, d)

			// Reconcile now should change Phase to running
			testReconcilePhase(rc, c, d.name, false, false, api.DeploymentPhaseRunning)

			validateCSIDriver(c, api.DefaultDriverName, testK8sVersion)

			// Ensure both deaemonset and statefulset have default driver name
			// before editing deployment
			ss := &appsv1.StatefulSet{}
			err = c.Get(context.TODO(), namespacedName(ss), ss)
			Expect(err).Should(BeNil(), "controller stateful set is expected on a successful deployment")
			for _, c := range ss.Spec.Template.Spec.Containers {
				if c.Name == "pmem-driver" {
					Expect(c.Args).Should(ContainElement("-drivername="+api.DefaultDriverName), "mismatched driver name")
					break
				}
			}
			ds := &appsv1.DaemonSet{}
			err = c.Get(context.TODO(), namespacedName(ds), ds)
			Expect(err).Should(BeNil(), "node daemon set is expected on a successful deployment")
			for _, c := range ds.Spec.Template.Spec.Containers {
				if c.Name == "pmem-driver" {
					Expect(c.Args).Should(ContainElement("-drivername="+api.DefaultDriverName), "mismatched driver name")
					break
				}
			}

			// Edit deployment name
			dep := &api.Deployment{
				ObjectMeta: metav1.ObjectMeta{
					Name: d.name,
				},
			}
			err = c.Get(context.TODO(), namespacedName(dep), dep)
			Expect(err).Should(BeNil(), "failed to retrieve deployment")

			dep.Spec.DriverName = "test-driver-name"
			err = c.Update(context.TODO(), dep)
			Expect(err).Should(BeNil(), "failed to update deployment")
			testReconcilePhase(rc, c, d.name, false, false, api.DeploymentPhaseRunning)

			// Ensure the new driver name effective in sub resources
			ss = &appsv1.StatefulSet{}
			err = c.Get(context.TODO(), namespacedName(ss), ss)
			Expect(err).Should(BeNil(), "controller stateful set is expected on a successful deployment")
			for _, c := range ss.Spec.Template.Spec.Containers {
				if c.Name == "pmem-driver" {
					Expect(c.Args).Should(ContainElement("-drivername="+dep.Spec.DriverName), "mismatched driver name")
					break
				}
			}
			ds = &appsv1.DaemonSet{}
			err = c.Get(context.TODO(), namespacedName(ds), ds)
			Expect(err).Should(BeNil(), "node daemon set is expected on a successful deployment")
			for _, c := range ds.Spec.Template.Spec.Containers {
				if c.Name == "pmem-driver" {
					Expect(c.Args).Should(ContainElement("-drivername="+dep.Spec.DriverName), "mismatched driver name")
					break
				}
			}
		})

		It("shall not allow duplicate driver name via update deployment", func() {
			d1 := &pmemDeployment{
				name:       "test-deployment1",
				driverName: "test-driver1",
			}

			d2 := &pmemDeployment{
				name:       "test-deployment2",
				driverName: "test-driver2",
			}

			err := c.Create(context.TODO(), getDeployment(d1))
			Expect(err).Should(BeNil(), "failed to create deployment1")

			err = c.Create(context.TODO(), getDeployment(d2))
			Expect(err).Should(BeNil(), "failed to create deployment2")

			// Ensure that both the deployments are in initializing phase
			testReconcilePhase(rc, c, d1.name, false, true, api.DeploymentPhaseInitializing)
			testReconcilePhase(rc, c, d2.name, false, true, api.DeploymentPhaseInitializing)

			// Try to introdue duplicate name when the driver is in Initializing phase
			// Rertrieve deployment1
			dep := &api.Deployment{
				ObjectMeta: metav1.ObjectMeta{
					Name: d1.name,
				},
			}
			err = c.Get(context.TODO(), namespacedName(dep), dep)
			Expect(err).Should(BeNil(), "failed to retrieve deployment")

			// Update it's driver name with the name of deployment2, which should
			// result in failure to move the reconcile phase
			dep.Spec.DriverName = d2.driverName
			err = c.Update(context.TODO(), dep)
			Expect(err).Should(BeNil(), "failed to update deployment")
			testReconcilePhase(rc, c, d1.name, true, true, api.DeploymentPhaseInitializing)

			// Move the deployment to Running phase
			testReconcilePhase(rc, c, d1.name, false, false, api.DeploymentPhaseRunning)

			validateCSIDriver(c, d1.driverName, testK8sVersion)

			// Try to introdue duplicate name when the driver is in Running phase
			dep.Spec.DriverName = d2.driverName
			err = c.Update(context.TODO(), dep)
			Expect(err).Should(BeNil(), "failed to update deployment")
			testReconcilePhase(rc, c, d1.name, true, false, api.DeploymentPhaseRunning)
		})

		It("shall not allow to change running deployment device mode", func() {
			d := &pmemDeployment{
				name: "test-deployment",
			}

			err := c.Create(context.TODO(), getDeployment(d))
			Expect(err).Should(BeNil(), "failed to create deployment")

			testReconcilePhase(rc, c, d.name, false, true, api.DeploymentPhaseInitializing)

			validateSecrets(c, d)

			// Reconcile now should change Phase to running
			testReconcilePhase(rc, c, d.name, false, false, api.DeploymentPhaseRunning)

			// Ensure both deaemonset and statefulset have default driver name
			// before editing deployment
			ds := &appsv1.DaemonSet{}
			err = c.Get(context.TODO(), namespacedName(ds), ds)
			Expect(err).Should(BeNil(), "node daemon set is expected on a successful deployment")
			for _, c := range ds.Spec.Template.Spec.Containers {
				if c.Name == "pmem-driver" {
					arg := fmt.Sprintf("-deviceManager=%s", api.DefaultDeviceMode)
					Expect(c.Args).Should(ContainElement(arg), "mismatched driver device mode")
					break
				}
			}

			// Edit deployment name
			dep := &api.Deployment{
				ObjectMeta: metav1.ObjectMeta{
					Name: d.name,
				},
			}
			err = c.Get(context.TODO(), namespacedName(dep), dep)
			Expect(err).Should(BeNil(), "failed to retrieve deployment")

			dep.Spec.DeviceMode = api.DeviceModeDirect
			err = c.Update(context.TODO(), dep)
			Expect(err).Should(BeNil(), "failed to update deployment")
			// Reconcile is expected to fail
			testReconcilePhase(rc, c, d.name, true, false, api.DeploymentPhaseRunning)
		})

		It("shall allow to change container resources of a running deployment", func() {
			d := &pmemDeployment{
				name:             "test-update-resource",
				controllerCPU:    "100m",
				controllerMemory: "10Mi",
				nodeCPU:          "110m",
				nodeMemory:       "11Mi",
			}

			err := c.Create(context.TODO(), getDeployment(d))
			Expect(err).Should(BeNil(), "failed to create deployment")

			testReconcilePhase(rc, c, d.name, false, true, api.DeploymentPhaseInitializing)

			validateSecrets(c, d)

			// Reconcile now should change Phase to running
			testReconcilePhase(rc, c, d.name, false, false, api.DeploymentPhaseRunning)

			ss := &appsv1.StatefulSet{}
			err = c.Get(context.TODO(), namespacedName(ss), ss)
			Expect(err).Should(BeNil(), "node daemon set is expected on a successful deployment")
			for _, c := range ss.Spec.Template.Spec.Containers {
				Expect(c.Resources.Requests.Cpu().Cmp(resource.MustParse(d.controllerCPU))).Should(BeZero(), "controller cpu resource requests mismatch")
				Expect(c.Resources.Requests.Memory().Cmp(resource.MustParse(d.controllerMemory))).Should(BeZero(), "controller memory resource requests mismatch")
			}

			ds := &appsv1.DaemonSet{}
			err = c.Get(context.TODO(), namespacedName(ds), ds)
			Expect(err).Should(BeNil(), "node daemon set is expected on a successful deployment")
			for _, c := range ds.Spec.Template.Spec.Containers {
				Expect(c.Resources.Requests.Cpu().Cmp(resource.MustParse(d.nodeCPU))).Should(BeZero(), "node cpu resource requests mismatch")
				Expect(c.Resources.Requests.Memory().Cmp(resource.MustParse(d.nodeMemory))).Should(BeZero(), "node memory resource requests mismatch")
			}

			// Update deployment resources
			d.controllerCPU = "200m"
			d.controllerMemory = "20Mi"
			d.nodeCPU = "210m"
			d.nodeMemory = "21Mi"
			dep := getDeployment(d)
			err = c.Update(context.TODO(), dep)
			Expect(err).Should(BeNil(), "failed to update deployment")
			// Reconcile is expected not to fail
			testReconcilePhase(rc, c, d.name, false, false, api.DeploymentPhaseRunning)

			// Recheck the container resources are updated
			ss = &appsv1.StatefulSet{}
			err = c.Get(context.TODO(), namespacedName(ss), ss)
			Expect(err).Should(BeNil(), "node daemon set is expected on a successful deployment")
			for _, c := range ss.Spec.Template.Spec.Containers {
				Expect(c.Resources.Requests.Cpu().Cmp(resource.MustParse(d.controllerCPU))).Should(BeZero(), "controller cpu resource requests mismatch")
				Expect(c.Resources.Requests.Memory().Cmp(resource.MustParse(d.controllerMemory))).Should(BeZero(), "controller memory resource requests mismatch")
			}

			ds = &appsv1.DaemonSet{}
			err = c.Get(context.TODO(), namespacedName(ds), ds)
			Expect(err).Should(BeNil(), "node daemon set is expected on a successful deployment")
			for _, c := range ds.Spec.Template.Spec.Containers {
				Expect(c.Resources.Requests.Cpu().Cmp(resource.MustParse(d.nodeCPU))).Should(BeZero(), "node cpu resource requests mismatch")
				Expect(c.Resources.Requests.Memory().Cmp(resource.MustParse(d.nodeMemory))).Should(BeZero(), "node memory resource requests mismatch")
			}
		})

		It("shall allow to change container images of a running deployment", func() {
			d := &pmemDeployment{
				name: "test-update-images",
			}

			err := c.Create(context.TODO(), getDeployment(d))
			Expect(err).Should(BeNil(), "failed to create deployment")

			testReconcilePhase(rc, c, d.name, false, true, api.DeploymentPhaseInitializing)

			validateSecrets(c, d)

			// Reconcile now should change Phase to running
			testReconcilePhase(rc, c, d.name, false, false, api.DeploymentPhaseRunning)

			ss := &appsv1.StatefulSet{}
			Expect(c.Get(context.TODO(), namespacedName(ss), ss)).Should(BeNil(), "stateful set object is expected on a successful deployment")
			ds := &appsv1.DaemonSet{}
			Expect(c.Get(context.TODO(), namespacedName(ds), ds)).Should(BeNil(), "node daemon set object is expected on a successful deployment")

			containers := ss.Spec.Template.Spec.Containers
			containers = append(containers, ds.Spec.Template.Spec.Containers...)
			for _, c := range containers {
				switch c.Name {
				case "pmem-driver":
					Expect(c.Image).Should(BeEquivalentTo(api.DefaultDriverImage), "mismatched pmem-csi driver image")
				case "provisioner":
					Expect(c.Image).Should(BeEquivalentTo(api.DefaultProvisionerImage), "mismatched provisioner image")
				case "driver-registar":
					Expect(c.Image).Should(BeEquivalentTo(api.DefaultRegistrarImage), "mismatched node registart image")
				}
			}

			// Update images
			d.image = "test-pmem-csi-image:v0.0.0"
			d.provisionerImage = "test-provisioner-image:v0.0.0"
			d.registrarImage = "test-registrar-image:v0.0.0"

			dep := getDeployment(d)
			Expect(c.Update(context.TODO(), dep)).Should(BeNil(), "failed to update deployment")
			testReconcilePhase(rc, c, d.name, false, false, api.DeploymentPhaseRunning)

			ss = &appsv1.StatefulSet{}
			Expect(c.Get(context.TODO(), namespacedName(ss), ss)).Should(BeNil(), "stateful set object is expected on a successful deployment")
			ds = &appsv1.DaemonSet{}
			Expect(c.Get(context.TODO(), namespacedName(ds), ds)).Should(BeNil(), "node daemon set is expected on a successful deployment")
			// Recheck if the images are updated
			containers = ss.Spec.Template.Spec.Containers
			containers = append(containers, ds.Spec.Template.Spec.Containers...)
			for _, c := range containers {
				switch c.Name {
				case "pmem-driver":
					Expect(c.Image).Should(BeEquivalentTo(d.image), "mismatched pmem-csi driver image")
				case "provisioner":
					Expect(c.Image).Should(BeEquivalentTo(d.provisionerImage), "mismatched provisioner image")
				case "driver-registar":
					Expect(c.Image).Should(BeEquivalentTo(d.registrarImage), "mismatched node registart image")
				}
			}
		})
	})
})
