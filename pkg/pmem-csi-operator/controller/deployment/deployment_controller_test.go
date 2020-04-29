/*
Copyright 2019  Intel Corporation.

SPDX-License-Identifier: Apache-2.0
*/
package deployment_test

import (
	"context"
	"fmt"
	"testing"
	"time"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"

	"github.com/intel/pmem-csi/deploy"
	"github.com/intel/pmem-csi/pkg/apis"
	api "github.com/intel/pmem-csi/pkg/apis/pmemcsi/v1alpha1"
	pmemcontroller "github.com/intel/pmem-csi/pkg/pmem-csi-operator/controller"
	"github.com/intel/pmem-csi/pkg/pmem-csi-operator/controller/deployment"
	pmemtls "github.com/intel/pmem-csi/pkg/pmem-csi-operator/pmem-tls"
	"github.com/intel/pmem-csi/pkg/version"
	"github.com/intel/pmem-csi/test/e2e/operator/validate"

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

func TestDeploymentController(t *testing.T) {
	RegisterFailHandler(Fail)

	err := apis.AddToScheme(scheme.Scheme)
	Expect(err).Should(BeNil(), "Failed to add api schema")

	RunSpecs(t, "PMEM Operator API test suite")
}

type pmemDeployment struct {
	name                                                string
	deviceMode                                          string
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

func getDeployment(d *pmemDeployment, c client.Client) *api.Deployment {
	dep := &api.Deployment{
		TypeMeta: metav1.TypeMeta{
			Kind:       "Deployment",
			APIVersion: "pmem-csi.intel.com/v1alpha1",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name: d.name,
			UID:  types.UID("fake-uuid-" + d.name),
		},
	}

	if c != nil {
		// Retrieve existing object before updating it.
		err := c.Get(context.TODO(), types.NamespacedName{Name: d.name}, dep)
		Expect(err).Should(BeNil(), "failed to retrive existing deployment object")
	}

	// TODO (?): embed DeploymentSpec inside pmemDeployment instead of splitting it up into individual values.
	// The entire copying block below then collapses into a single line.

	dep.Spec = api.DeploymentSpec{}
	spec := &dep.Spec
	spec.DeviceMode = api.DeviceMode(d.deviceMode)
	spec.LogLevel = d.logLevel
	spec.Image = d.image
	spec.PullPolicy = corev1.PullPolicy(d.pullPolicy)
	spec.ProvisionerImage = d.provisionerImage
	spec.NodeRegistrarImage = d.registrarImage
	if d.controllerCPU != "" || d.controllerMemory != "" {
		spec.ControllerResources = &corev1.ResourceRequirements{
			Requests: corev1.ResourceList{
				corev1.ResourceCPU:    resource.MustParse(d.controllerCPU),
				corev1.ResourceMemory: resource.MustParse(d.controllerMemory),
			},
		}
	}
	if d.nodeCPU != "" || d.nodeMemory != "" {
		spec.NodeResources = &corev1.ResourceRequirements{
			Requests: corev1.ResourceList{
				corev1.ResourceCPU:    resource.MustParse(d.nodeCPU),
				corev1.ResourceMemory: resource.MustParse(d.nodeMemory),
			},
		}
	}
	spec.CACert = d.caCert
	spec.RegistryCert = d.regCert
	spec.RegistryPrivateKey = d.regKey
	spec.NodeControllerCert = d.ncCert
	spec.NodeControllerPrivateKey = d.ncKey

	return dep
}

func testDeploymentPhase(c client.Client, name string, expectedPhase api.DeploymentPhase) {
	depObject := &api.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name: name,
		},
	}
	err := c.Get(context.TODO(), namespacedNameWithOffset(3, depObject), depObject)
	ExpectWithOffset(2, err).Should(BeNil(), "failed to retrive deployment object")
	ExpectWithOffset(2, depObject.Status.Phase).Should(BeEquivalentTo(expectedPhase), "Unexpected status phase")
}

func testReconcile(rc reconcile.Reconciler, name string, expectErr bool, expectedRequeue bool) {
	req := reconcile.Request{
		NamespacedName: types.NamespacedName{
			Name: name,
		},
	}
	resp, err := rc.Reconcile(req)
	if expectErr {
		ExpectWithOffset(2, err).ShouldNot(BeNil(), "expected reconcile failure")
	} else {
		ExpectWithOffset(2, err).Should(BeNil(), "reconcile failed with error")
	}
	ExpectWithOffset(2, resp.Requeue).Should(BeEquivalentTo(expectedRequeue), "expected requeue reconcile")
}

func testReconcilePhase(rc reconcile.Reconciler, c client.Client, name string, expectErr bool, expectedRequeue bool, expectedPhase api.DeploymentPhase) {
	testReconcile(rc, name, expectErr, expectedRequeue)
	testDeploymentPhase(c, name, expectedPhase)
}

func namespacedName(obj runtime.Object) types.NamespacedName {
	return namespacedNameWithOffset(2, obj)
}

func namespacedNameWithOffset(offset int, obj runtime.Object) types.NamespacedName {
	metaObj, err := meta.Accessor(obj)
	ExpectWithOffset(offset, err).Should(BeNil(), "failed to get accessor")

	return types.NamespacedName{Name: metaObj.GetName(), Namespace: metaObj.GetNamespace()}
}

// objectKey creates the lookup key for an object with a name and optionally with a namespace.
func objectKey(name string, namespace ...string) client.ObjectKey {
	key := types.NamespacedName{
		Name: name,
	}
	if len(namespace) > 0 {
		key.Namespace = namespace[0]
	}
	return key
}

func deleteDeployment(c client.Client, name, ns string) error {
	dep := &api.Deployment{}
	key := objectKey(name)
	if err := c.Get(context.TODO(), key, dep); err != nil {
		return err
	}

	By(fmt.Sprintf("Deleting Deployment '%s'", dep.Name))
	if err := c.Delete(context.TODO(), dep); err != nil {
		return err
	}
	// Delete sub-objects created by this deployment which
	// are possible might conflicts(CSIDriver) with later part of test
	// This is supposed to handle by Kubernetes grabage collector
	// but couldn't provided by fake client the tets are using
	//
	driver := &storagev1beta1.CSIDriver{
		ObjectMeta: metav1.ObjectMeta{
			Name: dep.Name,
		},
	}
	By(fmt.Sprintf("Deleting csi driver '%s'", driver.Name))
	return c.Delete(context.TODO(), driver)
}

var _ = Describe("Operator", func() {
	testIt := func(testK8sVersion version.Version) {
		var c client.Client
		var rc reconcile.Reconciler
		var testNamespace = "test-namespace"
		var testDriverImage = "fake-driver-image"

		BeforeEach(func() {
			c = fake.NewFakeClient()
			var err error
			rc, err = deployment.NewReconcileDeployment(c, pmemcontroller.ControllerOptions{
				Namespace:   testNamespace,
				K8sVersion:  testK8sVersion,
				DriverImage: testDriverImage,
			})
			Expect(err).ShouldNot(HaveOccurred(), "create new reconciler")
		})

		validateDriver := func(dep *api.Deployment) {
			// We may have to fill in some defaults, so make a copy first.
			dep = dep.DeepCopyObject().(*api.Deployment)
			if dep.Spec.Image == "" {
				dep.Spec.Image = testDriverImage
			}

			ExpectWithOffset(1, validate.DriverDeployment(c, testK8sVersion, testNamespace, *dep)).ShouldNot(HaveOccurred(), "validate deployment")
		}

		It("shall allow deployment with defaults", func() {
			d := &pmemDeployment{
				name: "test-deployment",
			}

			dep := getDeployment(d, nil)

			err := c.Create(context.TODO(), dep)
			Expect(err).Should(BeNil(), "failed to create deployment")

			testReconcilePhase(rc, c, d.name, false, false, api.DeploymentPhaseRunning)
			validateDriver(dep)
		})

		It("shall allow deployment with explicit values", func() {
			d := &pmemDeployment{
				name:             "test-deployment",
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

			dep := getDeployment(d, nil)
			err := c.Create(context.TODO(), dep)
			Expect(err).Should(BeNil(), "failed to create deployment")

			// Reconcile now should change Phase to running
			testReconcilePhase(rc, c, d.name, false, false, api.DeploymentPhaseRunning)
			validateDriver(dep)
		})

		It("shall allow multiple deployments", func() {
			d1 := &pmemDeployment{
				name: "test-deployment1",
			}

			d2 := &pmemDeployment{
				name: "test-deployment2",
			}

			dep := getDeployment(d1, nil)
			err := c.Create(context.TODO(), dep)
			Expect(err).Should(BeNil(), "failed to create deployment1")

			dep = getDeployment(d2, nil)
			err = c.Create(context.TODO(), dep)
			Expect(err).Should(BeNil(), "failed to create deployment2")

			testReconcilePhase(rc, c, d1.name, false, false, api.DeploymentPhaseRunning)
			testReconcilePhase(rc, c, d2.name, false, false, api.DeploymentPhaseRunning)

		})

		It("shall not allow invalid device mode", func() {
			d := &pmemDeployment{
				name:       "test-driver-modes",
				deviceMode: "foobar",
			}

			dep := getDeployment(d, nil)

			err := c.Create(context.TODO(), dep)
			Expect(err).Should(BeNil(), "failed to create deployment")
			// Deployment should failed with an error
			testReconcilePhase(rc, c, d.name, true, false, api.DeploymentPhaseFailed)
		})

		It("shall use provided private keys", func() {
			// Generate private key
			regKey, err := pmemtls.NewPrivateKey()
			Expect(err).Should(BeNil(), "Failed to generate a private key: %v", err)

			encodedKey := pmemtls.EncodeKey(regKey)

			d := &pmemDeployment{
				name:   "test-deployment",
				regKey: encodedKey,
			}
			dep := getDeployment(d, nil)
			err = c.Create(context.TODO(), dep)
			Expect(err).Should(BeNil(), "failed to create deployment")

			// First deployment expected to be successful
			testReconcilePhase(rc, c, d.name, false, false, api.DeploymentPhaseRunning)
			validateDriver(dep)
		})

		It("shall use provided private keys and certificates", func() {
			ca, err := pmemtls.NewCA(nil, nil)
			Expect(err).Should(BeNil(), "faield to instantiate CA")

			regKey, err := pmemtls.NewPrivateKey()
			Expect(err).Should(BeNil(), "failed to generate a private key: %v", err)
			regCert, err := ca.GenerateCertificate("pmem-registry", regKey.Public())
			Expect(err).Should(BeNil(), "failed to sign registry key")

			ncKey, err := pmemtls.NewPrivateKey()
			Expect(err).Should(BeNil(), "failed to generate a private key: %v", err)
			ncCert, err := ca.GenerateCertificate("pmem-node-controller", ncKey.Public())
			Expect(err).Should(BeNil(), "failed to sign node controller key")

			d := &pmemDeployment{
				name:    "test-deployment",
				caCert:  ca.EncodedCertificate(),
				regKey:  pmemtls.EncodeKey(regKey),
				regCert: pmemtls.EncodeCert(regCert),
				ncKey:   pmemtls.EncodeKey(ncKey),
				ncCert:  pmemtls.EncodeCert(ncCert),
			}
			dep := getDeployment(d, nil)
			err = c.Create(context.TODO(), dep)
			Expect(err).Should(BeNil(), "failed to create deployment")

			// First deployment expected to be successful
			testReconcilePhase(rc, c, d.name, false, false, api.DeploymentPhaseRunning)
			validateDriver(dep)
		})

		It("shall detect invalid private keys and certificates", func() {
			ca, err := pmemtls.NewCA(nil, nil)
			Expect(err).Should(BeNil(), "faield to instantiate CA")

			regKey, err := pmemtls.NewPrivateKey()
			Expect(err).Should(BeNil(), "failed to generate a private key: %v", err)
			regCert, err := ca.GenerateCertificate("invalid-registry", regKey.Public())
			Expect(err).Should(BeNil(), "failed to sign registry key")

			ncKey, err := pmemtls.NewPrivateKey()
			Expect(err).Should(BeNil(), "failed to generate a private key: %v", err)
			ncCert, err := ca.GenerateCertificate("invalid-node-controller", ncKey.Public())
			Expect(err).Should(BeNil(), "failed to sign node key")

			d := &pmemDeployment{
				name:    "test-deployment-cert-invalid",
				caCert:  ca.EncodedCertificate(),
				regKey:  pmemtls.EncodeKey(regKey),
				regCert: pmemtls.EncodeCert(regCert),
				ncKey:   pmemtls.EncodeKey(ncKey),
				ncCert:  pmemtls.EncodeCert(ncCert),
			}
			dep := getDeployment(d, nil)
			err = c.Create(context.TODO(), dep)
			Expect(err).Should(BeNil(), "failed to create deployment")

			testReconcilePhase(rc, c, d.name, true, true, api.DeploymentPhaseFailed)
		})

		It("shall detect expired certificates", func() {
			oneDayAgo := time.Now().Add(-24 * time.Hour)
			oneMinuteAgo := time.Now().Add(-1 * time.Minute)

			ca, err := pmemtls.NewCA(nil, nil)
			Expect(err).Should(BeNil(), "faield to instantiate CA")

			regKey, err := pmemtls.NewPrivateKey()
			Expect(err).Should(BeNil(), "failed to generate a private key: %v", err)
			regCert, err := ca.GenerateCertificateWithDuration("pmem-registry", oneDayAgo, oneMinuteAgo, regKey.Public())
			Expect(err).Should(BeNil(), "failed to registry sign key")

			ncKey, err := pmemtls.NewPrivateKey()
			Expect(err).Should(BeNil(), "failed to generate a private key: %v", err)
			ncCert, err := ca.GenerateCertificateWithDuration("pmem-node-controller", oneDayAgo, oneMinuteAgo, ncKey.Public())
			Expect(err).Should(BeNil(), "failed to sign node controller key")

			d := &pmemDeployment{
				name:    "test-deployment-cert-expired",
				caCert:  ca.EncodedCertificate(),
				regKey:  pmemtls.EncodeKey(regKey),
				regCert: pmemtls.EncodeCert(regCert),
				ncKey:   pmemtls.EncodeKey(ncKey),
				ncCert:  pmemtls.EncodeCert(ncCert),
			}
			dep := getDeployment(d, nil)
			err = c.Create(context.TODO(), dep)
			Expect(err).Should(BeNil(), "failed to create deployment")

			testReconcilePhase(rc, c, d.name, true, true, api.DeploymentPhaseFailed)
		})

		It("shall allow to change container resources of a running deployment", func() {
			d := &pmemDeployment{
				name:             "test-update-resource",
				controllerCPU:    "100m",
				controllerMemory: "10Mi",
				nodeCPU:          "110m",
				nodeMemory:       "11Mi",
			}

			dep := getDeployment(d, nil)
			err := c.Create(context.TODO(), dep)
			Expect(err).Should(BeNil(), "failed to create deployment")

			testReconcilePhase(rc, c, d.name, false, false, api.DeploymentPhaseRunning)
			validateDriver(dep)

			// Reconcile now should keep phase as running
			testReconcilePhase(rc, c, d.name, false, false, api.DeploymentPhaseRunning)

			// Update deployment resources
			d.controllerCPU = "200m"
			d.controllerMemory = "20Mi"
			d.nodeCPU = "210m"
			d.nodeMemory = "21Mi"
			dep = getDeployment(d, c)
			err = c.Update(context.TODO(), dep)
			Expect(err).Should(BeNil(), "failed to update deployment")
			// Reconcile is expected not to fail
			testReconcilePhase(rc, c, d.name, false, false, api.DeploymentPhaseRunning)

			// Recheck the container resources are updated
			validateDriver(dep)
		})

		It("shall allow to change container images of a running deployment", func() {
			d := &pmemDeployment{
				name: "test-update-images",
			}

			dep := getDeployment(d, nil)
			err := c.Create(context.TODO(), dep)
			Expect(err).Should(BeNil(), "failed to create deployment")

			testReconcilePhase(rc, c, d.name, false, false, api.DeploymentPhaseRunning)
			validateDriver(dep)

			// Reconcile now should change keep phase as running
			testReconcilePhase(rc, c, d.name, false, false, api.DeploymentPhaseRunning)
			validateDriver(dep)

			// Update images
			d.image = "test-pmem-csi-image:v0.0.0"
			d.provisionerImage = "test-provisioner-image:v0.0.0"
			d.registrarImage = "test-registrar-image:v0.0.0"

			dep = getDeployment(d, c)
			Expect(c.Update(context.TODO(), dep)).Should(BeNil(), "failed to update deployment")
			testReconcilePhase(rc, c, d.name, false, false, api.DeploymentPhaseRunning)
			validateDriver(dep)
		})
	}

	// Validate for all supported Kubernetes versions.
	for _, yaml := range deploy.ListAll() {
		version := yaml.Kubernetes
		Context(fmt.Sprintf("Kubernetes %v", version), func() {
			testIt(version)
		})
	}
})
