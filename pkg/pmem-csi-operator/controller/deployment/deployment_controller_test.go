/*
Copyright 2019  Intel Corporation.

SPDX-License-Identifier: Apache-2.0
*/
package deployment_test

import (
	"fmt"
	"testing"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"

	"github.com/intel/pmem-csi/pkg/apis"
	api "github.com/intel/pmem-csi/pkg/apis/pmemcsi/v1alpha1"
	"github.com/intel/pmem-csi/pkg/pmem-csi-operator/controller/deployment"
	appsv1 "k8s.io/api/apps/v1"
	certv1beta1 "k8s.io/api/certificates/v1beta1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/klog"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

func TestDeploymentController(t *testing.T) {
	RegisterFailHandler(Fail)

	err := apis.AddToScheme(scheme.Scheme)
	Expect(err).Should(BeNil(), "Failed to add api schema")

	RunSpecs(t, "PMEM Operator API test suite")
}

type pmemDeployment struct {
	nsn                                                 types.NamespacedName
	driverName                                          string
	logLevel                                            uint16
	image, pullPolicy, provisionerImage, registrarImage string
	controllerCPU, controllerMemory                     string
	nodeCPU, nodeMemory                                 string
}

func decodeYaml(deploymentYaml string) runtime.Object {
	klog.Info(deploymentYaml)
	decode := scheme.Codecs.UniversalDeserializer().Decode

	obj, _, err := decode([]byte(deploymentYaml), nil, nil)
	Expect(err).Should(BeNil(), "Failed to parse deployment")
	Expect(obj).ShouldNot(BeNil(), "Nil deployment object")

	return obj
}

func getDeployment(d pmemDeployment) runtime.Object {
	yaml := fmt.Sprintf(`kind: Deployment
apiVersion: pmem-csi.intel.com/v1alpha1
metadata:
  name: %s
`, d.nsn.Name)
	if d.nsn.Namespace != "" {
		yaml += "  namespace: " + d.nsn.Namespace + "\n"
	}
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
	return decodeYaml(yaml)
}

func testDeploymentPhase(rc *deployment.ReconcileDeployment, nsn types.NamespacedName, expectedPhase api.DeploymentPhase) {
	depObject := &api.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      nsn.Name,
			Namespace: nsn.Namespace,
		},
	}
	err := rc.Get(depObject)
	Expect(err).Should(BeNil(), "failed to retrive deployment object")
	Expect(depObject.Status.Phase).Should(BeEquivalentTo(expectedPhase), "Unexpected status phase")
}

func testReconcile(rc *deployment.ReconcileDeployment, nsn types.NamespacedName, expectErr bool, expectedRequeue bool) {
	req := reconcile.Request{
		NamespacedName: types.NamespacedName{
			Name:      nsn.Name,
			Namespace: nsn.Namespace,
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

func testReconcilePhase(rc *deployment.ReconcileDeployment, nsn types.NamespacedName, expectErr bool, expectedRequeue bool, expectedPhase api.DeploymentPhase) {
	testReconcile(rc, nsn, expectErr, expectedRequeue)
	testDeploymentPhase(rc, nsn, expectedPhase)
}

func getReconciler(initObjects ...runtime.Object) *deployment.ReconcileDeployment {
	client := fake.NewFakeClientWithScheme(scheme.Scheme, initObjects...)
	Expect(client).ShouldNot(BeNil(), "Failed to initialize client")
	return deployment.NewReconcileDeployment(client, scheme.Scheme).(*deployment.ReconcileDeployment)
}

var _ = Describe("Operator", func() {

	Context("Deployment Controller", func() {
		It("shall allow deployment with defaults", func() {
			d := pmemDeployment{
				nsn: types.NamespacedName{
					Name:      "test-deployment",
					Namespace: "testnamespace",
				},
			}

			rc := getReconciler(getDeployment(d))
			testReconcilePhase(rc, d.nsn, false, true, api.DeploymentPhasePending)

			for _, name := range []string{"pmem-registry", "pmem-node-controller"} {
				csrName := d.nsn.Name + "-" + d.nsn.Namespace + "-" + name
				csr := &certv1beta1.CertificateSigningRequest{
					ObjectMeta: metav1.ObjectMeta{
						Name: csrName,
					},
				}
				err := rc.Get(csr)
				Expect(err).Should(BeNil(), "failed to get csr for %q", csrName)
				Expect(len(csr.Status.Conditions)).Should(BeEquivalentTo(0), "unexpected status condition")
			}

			// Reconcile loop should not change deployment state
			// with no certificate approval
			testReconcilePhase(rc, d.nsn, false, true, api.DeploymentPhasePending)

			// Approve certificates
			for _, name := range []string{"pmem-registry", "pmem-node-controller"} {
				csrName := d.nsn.Name + "-" + d.nsn.Namespace + "-" + name
				csr := &certv1beta1.CertificateSigningRequest{
					ObjectMeta: metav1.ObjectMeta{
						Name: csrName,
					},
				}
				err := rc.Get(csr)
				Expect(err).Should(BeNil(), "failed to get csr for %q", csrName)
				Expect(len(csr.Status.Conditions)).Should(BeEquivalentTo(0), "unexpected status condition")

				csr.Status.Conditions = append(csr.Status.Conditions, certv1beta1.CertificateSigningRequestCondition{
					Type:           certv1beta1.CertificateApproved,
					LastUpdateTime: metav1.Now(),
				})

				csr.Status.Certificate = []byte("Dummy certificate")

				err = rc.Update(csr)
				Expect(err).Should(BeNil(), "failed to approve CSR")
			}

			// Reconcile after approved certificates should change the status
			testReconcilePhase(rc, d.nsn, false, true, api.DeploymentPhaseInitializing)

			// Reconcile now should change Phase to running
			testReconcilePhase(rc, d.nsn, false, false, api.DeploymentPhaseRunning)

			ss := &appsv1.StatefulSet{}
			err := rc.Get(ss)
			Expect(err).Should(BeNil(), "controller stateful set is expected on a successful deployment")
			Expect(*ss.Spec.Replicas).Should(BeEquivalentTo(1), "controller stateful set replication count mismatch")
			Expect(ss.ObjectMeta.Namespace).Should(BeEquivalentTo(d.nsn.Namespace), "controller stateful set namespace mismatch")
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
			err = rc.Get(ds)
			Expect(err).Should(BeNil(), "node daemon set is expected on a successful deployment")
			Expect(ds.ObjectMeta.Namespace).Should(BeEquivalentTo(d.nsn.Namespace), "node daemon set namespace mismatch")
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
			d := pmemDeployment{
				nsn: types.NamespacedName{
					Name:      "test-deployment",
					Namespace: "testnamespace",
				},
				driverName:       "test-pmem-driver.intel.com",
				image:            "test-driver:v0.0.0",
				provisionerImage: "test-provisioner-iamge:v0.0.0",
				registrarImage:   "test-driver-registrar-image:v.0.0.0",
				pullPolicy:       "Never",
				logLevel:         10,
				controllerCPU:    "1500m",
				controllerMemory: "300Mi",
				nodeCPU:          "1000m",
				nodeMemory:       "500Mi",
			}

			rc := getReconciler(getDeployment(d))
			testReconcilePhase(rc, d.nsn, false, true, api.DeploymentPhasePending)

			for _, name := range []string{"pmem-registry", "pmem-node-controller"} {
				csrName := d.nsn.Name + "-" + d.nsn.Namespace + "-" + name
				csr := &certv1beta1.CertificateSigningRequest{
					ObjectMeta: metav1.ObjectMeta{
						Name: csrName,
					},
				}
				err := rc.Get(csr)
				Expect(err).Should(BeNil(), "failed to get csr for %q", csrName)
				Expect(len(csr.Status.Conditions)).Should(BeEquivalentTo(0), "unexpected status condition")
			}

			// Reconcile loop should not change deployment state
			// with no certificate approval
			testReconcilePhase(rc, d.nsn, false, true, api.DeploymentPhasePending)

			// Approve certificates
			for _, name := range []string{"pmem-registry", "pmem-node-controller"} {
				csrName := d.nsn.Name + "-" + d.nsn.Namespace + "-" + name
				csr := &certv1beta1.CertificateSigningRequest{
					ObjectMeta: metav1.ObjectMeta{
						Name: csrName,
					},
				}
				err := rc.Get(csr)
				Expect(err).Should(BeNil(), "failed to get csr for %q", csrName)
				Expect(len(csr.Status.Conditions)).Should(BeEquivalentTo(0), "unexpected status condition")

				csr.Status.Conditions = append(csr.Status.Conditions, certv1beta1.CertificateSigningRequestCondition{
					Type:           certv1beta1.CertificateApproved,
					LastUpdateTime: metav1.Now(),
				})

				csr.Status.Certificate = []byte("Dummy certificate")

				err = rc.Update(csr)
				Expect(err).Should(BeNil(), "failed to approve CSR")
			}

			// Reconcile after approved certificates should change the status
			testReconcilePhase(rc, d.nsn, false, true, api.DeploymentPhaseInitializing)

			// Reconcile now should change Phase to running
			testReconcilePhase(rc, d.nsn, false, false, api.DeploymentPhaseRunning)

			ss := &appsv1.StatefulSet{}
			err := rc.Get(ss)
			Expect(err).Should(BeNil(), "controller stateful set is expected on a successful deployment")
			Expect(*ss.Spec.Replicas).Should(BeEquivalentTo(1), "controller stateful set replication count mismatch")
			Expect(ss.ObjectMeta.Namespace).Should(BeEquivalentTo(d.nsn.Namespace), "controller stateful set namespace mismatch")
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
			err = rc.Get(ds)
			Expect(err).Should(BeNil(), "node daemon set is expected on a successful deployment")
			Expect(ds.ObjectMeta.Namespace).Should(BeEquivalentTo(d.nsn.Namespace), "node daemon set namespace mismatch")
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
			d1 := pmemDeployment{
				nsn: types.NamespacedName{
					Name:      "test-deployment1",
					Namespace: "testnamespace",
				},
				driverName: "test-pmem-driver1.intel.com",
			}

			d2 := pmemDeployment{
				nsn: types.NamespacedName{
					Name:      "test-deployment2",
					Namespace: "testnamespace",
				},
				driverName: "test-pmem-driver2.intel.com",
			}

			rc := getReconciler(getDeployment(d1), getDeployment(d2))
			testReconcilePhase(rc, d1.nsn, false, true, api.DeploymentPhasePending)

			testReconcilePhase(rc, d2.nsn, false, true, api.DeploymentPhasePending)

		})
		It("shall not allow multiple deployments with same driver name", func() {
			d1 := pmemDeployment{
				nsn: types.NamespacedName{
					Name:      "test-deployment1",
					Namespace: "testnamespace",
				},
				driverName: "test-pmem-driver.intel.com",
			}

			d2 := pmemDeployment{
				nsn: types.NamespacedName{
					Name:      "test-deployment2",
					Namespace: "testnamespace",
				},
				driverName: "test-pmem-driver.intel.com",
			}

			rc := getReconciler(getDeployment(d1), getDeployment(d2))
			// First deployment expected to be successful
			testReconcilePhase(rc, d1.nsn, false, true, api.DeploymentPhasePending)
			// Second deployment expected to fail as one more deployment is active with the
			// same driver name
			testReconcilePhase(rc, d2.nsn, true, true, api.DeploymentPhaseFailed)

			// Delete fist deployment
			dep1 := &api.Deployment{
				ObjectMeta: metav1.ObjectMeta{
					Name:      d1.nsn.Name,
					Namespace: d1.nsn.Namespace,
				},
			}
			err := rc.Delete(dep1)
			Expect(err).Should(BeNil(), "Failed to delete deployment in failed state")

			testReconcile(rc, d1.nsn, false, false)

			// After removing first deployment, sencond deployment should progress
			testReconcilePhase(rc, d2.nsn, false, true, api.DeploymentPhasePending)
		})
	})
})
