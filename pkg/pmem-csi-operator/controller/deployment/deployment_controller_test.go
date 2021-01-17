/*
Copyright 2019  Intel Corporation.

SPDX-License-Identifier: Apache-2.0
*/
package deployment_test

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	// "github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/intel/pmem-csi/deploy"
	"github.com/intel/pmem-csi/pkg/apis"
	api "github.com/intel/pmem-csi/pkg/apis/pmemcsi/v1beta1"
	"github.com/intel/pmem-csi/pkg/logger"
	"github.com/intel/pmem-csi/pkg/logger/testinglogger"
	pmemcontroller "github.com/intel/pmem-csi/pkg/pmem-csi-operator/controller"
	"github.com/intel/pmem-csi/pkg/pmem-csi-operator/controller/deployment"
	"github.com/intel/pmem-csi/pkg/pmem-csi-operator/controller/deployment/testcases"
	"github.com/intel/pmem-csi/pkg/version"
	"github.com/intel/pmem-csi/test/e2e/operator/validate"

	corev1 "k8s.io/api/core/v1"
	storagev1beta1 "k8s.io/api/storage/v1beta1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/watch"
	"k8s.io/client-go/kubernetes"
	cgfake "k8s.io/client-go/kubernetes/fake"
	"k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

type pmemDeployment struct {
	// input parameters for test
	name                                                string
	deviceMode                                          string
	logLevel                                            uint16
	logFormat                                           string
	image, pullPolicy, provisionerImage, registrarImage string
	controllerCPU, controllerMemory                     string
	nodeCPU, nodeMemory                                 string
	provisionerCPU, provisionerMemory                   string
	nodeRegistarCPU, nodeRegistrarMemory                string
	controllerTLSSecret                                 string
	mutatePods                                          api.MutatePods
	schedulerNodePort                                   int32
	kubeletDir                                          string

	objects []runtime.Object

	// expected result
	expectFailure bool
}

func getDeployment(d *pmemDeployment) *api.PmemCSIDeployment {
	dep := &api.PmemCSIDeployment{
		TypeMeta: metav1.TypeMeta{
			Kind:       "PmemCSIDeployment",
			APIVersion: api.SchemeGroupVersion.String(),
		},
		ObjectMeta: metav1.ObjectMeta{
			Name: d.name,
			UID:  types.UID("fake-uuid-" + d.name),
		},
	}

	// TODO (?): embed DeploymentSpec inside pmemDeployment instead of splitting it up into individual values.
	// The entire copying block below then collapses into a single line.

	dep.Spec = api.DeploymentSpec{
		DeviceMode:          api.DeviceMode(d.deviceMode),
		LogLevel:            d.logLevel,
		LogFormat:           api.LogFormat(d.logFormat),
		Image:               d.image,
		PullPolicy:          corev1.PullPolicy(d.pullPolicy),
		ProvisionerImage:    d.provisionerImage,
		NodeRegistrarImage:  d.registrarImage,
		ControllerTLSSecret: d.controllerTLSSecret,
		MutatePods:          d.mutatePods,
		SchedulerNodePort:   d.schedulerNodePort,
	}
	spec := &dep.Spec
	if d.controllerCPU != "" || d.controllerMemory != "" {
		spec.ControllerDriverResources = &corev1.ResourceRequirements{
			Requests: corev1.ResourceList{
				corev1.ResourceCPU:    resource.MustParse(d.controllerCPU),
				corev1.ResourceMemory: resource.MustParse(d.controllerMemory),
			},
		}
	}
	if d.nodeCPU != "" || d.nodeMemory != "" {
		spec.NodeDriverResources = &corev1.ResourceRequirements{
			Requests: corev1.ResourceList{
				corev1.ResourceCPU:    resource.MustParse(d.nodeCPU),
				corev1.ResourceMemory: resource.MustParse(d.nodeMemory),
			},
		}
	}
	if d.provisionerCPU != "" || d.provisionerMemory != "" {
		spec.ProvisionerResources = &corev1.ResourceRequirements{
			Requests: corev1.ResourceList{
				corev1.ResourceCPU:    resource.MustParse(d.provisionerCPU),
				corev1.ResourceMemory: resource.MustParse(d.provisionerMemory),
			},
		}
	}
	if d.nodeRegistarCPU != "" || d.nodeRegistrarMemory != "" {
		spec.NodeRegistrarResources = &corev1.ResourceRequirements{
			Requests: corev1.ResourceList{
				corev1.ResourceCPU:    resource.MustParse(d.nodeRegistarCPU),
				corev1.ResourceMemory: resource.MustParse(d.nodeRegistrarMemory),
			},
		}
	}
	if d.kubeletDir != "" {
		spec.KubeletDir = d.kubeletDir
	}

	return dep
}

func testDeploymentPhase(t *testing.T, c client.Client, name string, expectedPhase api.DeploymentPhase) {
	depObject := &api.PmemCSIDeployment{
		ObjectMeta: metav1.ObjectMeta{
			Name: name,
		},
	}
	err := c.Get(context.TODO(), namespacedNameWithOffset(t, 3, depObject), depObject)
	require.NoError(t, err, "failed to retrive deployment object")
	require.Equal(t, expectedPhase, depObject.Status.Phase, "Unexpected status phase")
}

func testReconcile(t *testing.T, rc reconcile.Reconciler, name string, expectErr bool, expectedRequeue bool) {
	req := reconcile.Request{
		NamespacedName: types.NamespacedName{
			Name: name,
		},
	}
	resp, err := rc.Reconcile(req)
	if expectErr {
		require.Error(t, err, "expected reconcile failure")
	} else {
		require.NoError(t, err, "reconcile failed with error")
	}
	require.Equal(t, expectedRequeue, resp.Requeue, "expected requeue reconcile")
}

func testReconcilePhase(t *testing.T, rc reconcile.Reconciler, c client.Client, name string, expectErr bool, expectedRequeue bool, expectedPhase api.DeploymentPhase) {
	testReconcile(t, rc, name, expectErr, expectedRequeue)
	testDeploymentPhase(t, c, name, expectedPhase)
}

func namespacedName(t *testing.T, obj runtime.Object) types.NamespacedName {
	return namespacedNameWithOffset(t, 2, obj)
}

func namespacedNameWithOffset(t *testing.T, offset int, obj runtime.Object) types.NamespacedName {
	metaObj, err := meta.Accessor(obj)
	require.NoError(t, err, "failed to get accessor")

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
	dep := &api.PmemCSIDeployment{}
	key := objectKey(name)
	if err := c.Get(context.TODO(), key, dep); err != nil {
		return err
	}

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
	return c.Delete(context.TODO(), driver)
}

const (
	testNamespace   = "test-namespace"
	testDriverImage = "fake-driver-image"
)

type testContext struct {
	ctx              context.Context
	t                *testing.T
	c                client.Client
	cs               kubernetes.Interface
	rc               reconcile.Reconciler
	evWatcher        watch.Interface
	resourceVersions map[string]string
	k8sVersion       version.Version

	eventsMutex sync.Mutex
	events      []*corev1.Event
}

func newTestContext(t *testing.T, k8sVersion version.Version, initObjs ...runtime.Object) *testContext {
	ctx := logger.Set(context.Background(), testinglogger.New(t))
	tc := &testContext{
		ctx:              ctx,
		t:                t,
		c:                newTestClient(initObjs...),
		cs:               cgfake.NewSimpleClientset(),
		resourceVersions: map[string]string{},
		k8sVersion:       k8sVersion,
	}

	tc.ResetReconciler()

	return tc
}

func (tc *testContext) ResetReconciler() {
	rc, err := deployment.NewReconcileDeployment(tc.ctx, tc.c, pmemcontroller.ControllerOptions{
		Namespace:    testNamespace,
		K8sVersion:   tc.k8sVersion,
		DriverImage:  testDriverImage,
		EventsClient: tc.cs.CoreV1().Events(metav1.NamespaceDefault),
	})
	require.NoError(tc.t, err, "create new reconciler")
	tc.rc = rc
	tc.UnsetEventWatcher()
	tc.evWatcher = rc.(*deployment.ReconcileDeployment).EventBroadcaster().StartEventWatcher(func(ev *corev1.Event) {
		tc.eventsMutex.Lock()
		defer tc.eventsMutex.Unlock()

		// Discard consecutive duplicate events, mimicking the EventAggregator behavior
		if len(tc.events) != 0 {
			lastEvent := tc.events[len(tc.events)-1]
			if lastEvent.Reason == ev.Reason && lastEvent.InvolvedObject.UID == ev.InvolvedObject.UID {
				return
			}
		}
		tc.events = append(tc.events, ev)
	})
}

func (tc *testContext) UnsetEventWatcher() {
	if tc != nil && tc.evWatcher != nil {
		tc.evWatcher.Stop()
	}
}

func createSecret(name, namespace string, data map[string][]byte) *corev1.Secret {
	return &corev1.Secret{
		TypeMeta: metav1.TypeMeta{
			APIVersion: corev1.SchemeGroupVersion.String(),
			Kind:       "Secret",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
		},
		Data: data,
	}
}

func TestDeploymentController(t *testing.T) {
	err := apis.AddToScheme(scheme.Scheme)
	require.NoError(t, err, "add api schema")

	testIt := func(t *testing.T, testK8sVersion version.Version) {
		setup := func(t *testing.T, initObjs ...runtime.Object) *testContext {
			return newTestContext(t, testK8sVersion, initObjs...)
		}

		teardown := func(tc *testContext) {
			tc.UnsetEventWatcher()
		}

		validateEvents := func(tc *testContext, dep *api.PmemCSIDeployment, expectedEvents []string) {
			require.Eventually(tc.t, func() bool {
				tc.eventsMutex.Lock()
				defer tc.eventsMutex.Unlock()

				if len(tc.events) < len(expectedEvents) {
					return false
				}
				events := []string{}
				for _, e := range tc.events {
					if e.InvolvedObject.UID == dep.GetUID() {
						events = append(events, e.Reason)
					}
				}
				require.ElementsMatch(tc.t, events, expectedEvents, "events must match")
				return true
			}, 30*time.Second, time.Second, "receive all expected events")
		}

		validateConditions := func(tc *testContext, name string, expected map[api.DeploymentConditionType]corev1.ConditionStatus) {
			dep := &api.PmemCSIDeployment{
				ObjectMeta: metav1.ObjectMeta{
					Name: name,
				},
			}
			err := tc.c.Get(tc.ctx, client.ObjectKey{Name: name}, dep)
			require.NoError(tc.t, err, "get deployment: %s", name)
			require.Equal(tc.t, len(expected), len(dep.Status.Conditions), "mismatched conditions(%+v)", dep.Status.Conditions)

			for _, c := range dep.Status.Conditions {
				require.Equal(tc.t, expected[c.Type], c.Status, "condition status")
			}
		}

		validateDriver := func(tc *testContext, dep *api.PmemCSIDeployment, expectedEvents []string, wasUpdated bool) {
			// We may have to fill in some defaults, so make a copy first.
			dep = dep.DeepCopyObject().(*api.PmemCSIDeployment)
			if dep.Spec.Image == "" {
				dep.Spec.Image = testDriverImage
			}

			// If the CR was not updated, then objects should still be the same as they were initially.
			rv := tc.resourceVersions
			if wasUpdated {
				rv = nil
			}
			_, err := validate.DriverDeployment(tc.c, testK8sVersion, testNamespace, *dep, rv)
			require.NoError(tc.t, err, "validate deployment")
			validateEvents(tc, dep, expectedEvents)
		}

		t.Parallel()

		dataOkay := map[string][]byte{
			api.TLSSecretCA:   []byte("ca"),
			api.TLSSecretKey:  []byte("key"),
			api.TLSSecretCert: []byte("cert"),
		}

		cases := map[string]pmemDeployment{
			"deployment with defaults": pmemDeployment{
				name: "test-deployment",
			},
			"deployment with explicit values": pmemDeployment{
				name:             "test-deployment",
				image:            "test-driver:v0.0.0",
				provisionerImage: "test-provisioner-image:v0.0.0",
				registrarImage:   "test-driver-registrar-image:v.0.0.0",
				pullPolicy:       "Never",
				logLevel:         10,
				logFormat:        "json",
				controllerCPU:    "1500m",
				controllerMemory: "300Mi",
				nodeCPU:          "1000m",
				nodeMemory:       "500Mi",
				kubeletDir:       "/some/directory",
			},
			"invalid device mode": pmemDeployment{
				name:          "test-driver-modes",
				deviceMode:    "foobar",
				expectFailure: true,
			},
			"LVM mode": pmemDeployment{
				name:       "test-driver-modes",
				deviceMode: "lvm",
			},
			"direct mode": pmemDeployment{
				name:       "test-driver-modes",
				deviceMode: "direct",
			},
			"with controller, no secret": pmemDeployment{
				name:                "test-controller",
				controllerTLSSecret: "controller-secret",
				expectFailure:       true,
			},
			"with controller, wrong secret content": pmemDeployment{
				name:                "test-controller",
				controllerTLSSecret: "controller-secret",
				objects:             []runtime.Object{createSecret("controller-secret", testNamespace, nil)},
				expectFailure:       true,
			},
			"with controller, secret okay": pmemDeployment{
				name:                "test-controller",
				controllerTLSSecret: "controller-secret",
				objects:             []runtime.Object{createSecret("controller-secret", testNamespace, dataOkay)},
			},
			"controller, no mutate": pmemDeployment{
				name:                "test-controller",
				controllerTLSSecret: "controller-secret",
				mutatePods:          api.MutatePodsNever,
				objects:             []runtime.Object{createSecret("controller-secret", testNamespace, dataOkay)},
			},
			"controller, try mutate": pmemDeployment{
				name:                "test-controller",
				controllerTLSSecret: "controller-secret",
				mutatePods:          api.MutatePodsTry,
				objects:             []runtime.Object{createSecret("controller-secret", testNamespace, dataOkay)},
			},
			"controller, always mutate": pmemDeployment{
				name:                "test-controller",
				controllerTLSSecret: "controller-secret",
				mutatePods:          api.MutatePodsAlways,
				objects:             []runtime.Object{createSecret("controller-secret", testNamespace, dataOkay)},
			},
			"controller, port 31000": pmemDeployment{
				name:                "test-controller",
				controllerTLSSecret: "controller-secret",
				schedulerNodePort:   31000,
				objects:             []runtime.Object{createSecret("controller-secret", testNamespace, dataOkay)},
			},
		}

		for name, d := range cases {
			d := d
			t.Run(name, func(t *testing.T) {
				tc := setup(t, d.objects...)
				defer teardown(tc)
				dep := getDeployment(&d)

				err := tc.c.Create(tc.ctx, dep)
				require.NoError(t, err, "failed to create deployment")

				if d.expectFailure {
					testReconcilePhase(t, tc.rc, tc.c, d.name, true, true, api.DeploymentPhaseFailed)
					validateEvents(tc, dep, []string{api.EventReasonNew, api.EventReasonFailed})
					validateConditions(tc, d.name, map[api.DeploymentConditionType]corev1.ConditionStatus{})
				} else {
					testReconcilePhase(t, tc.rc, tc.c, d.name, false, false, api.DeploymentPhaseRunning)
					validateDriver(tc, dep, []string{api.EventReasonNew, api.EventReasonRunning}, false)
					validateConditions(tc, d.name, map[api.DeploymentConditionType]corev1.ConditionStatus{
						api.DriverDeployed: corev1.ConditionTrue,
					})
				}
			})
		}

		t.Run("multiple deployments", func(t *testing.T) {
			tc := setup(t)
			defer teardown(tc)
			d1 := &pmemDeployment{
				name: "test-deployment1",
			}

			d2 := &pmemDeployment{
				name: "test-deployment2",
			}

			dep1 := getDeployment(d1)
			err := tc.c.Create(tc.ctx, dep1)
			require.NoError(t, err, "failed to create deployment1")

			dep2 := getDeployment(d2)
			err = tc.c.Create(tc.ctx, dep2)
			require.NoError(t, err, "failed to create deployment2")

			conditions := map[api.DeploymentConditionType]corev1.ConditionStatus{
				api.DriverDeployed: corev1.ConditionTrue,
			}

			testReconcilePhase(t, tc.rc, tc.c, d1.name, false, false, api.DeploymentPhaseRunning)
			validateDriver(tc, dep1, []string{api.EventReasonNew, api.EventReasonRunning}, false)
			validateConditions(tc, d1.name, conditions)
			testReconcilePhase(t, tc.rc, tc.c, d2.name, false, false, api.DeploymentPhaseRunning)
			validateDriver(tc, dep2, []string{api.EventReasonNew, api.EventReasonRunning}, false)
			validateConditions(tc, d2.name, conditions)
		})

		t.Run("modified deployment under reconcile", func(t *testing.T) {
			tc := setup(t)
			defer teardown(tc)

			d := &pmemDeployment{
				name: "modified-deployment",
			}

			var updatedDep *api.PmemCSIDeployment

			dep := getDeployment(d)
			err := tc.c.Create(tc.ctx, dep)
			require.NoError(t, err, "failed to create deployment")

			hook := func(d *api.PmemCSIDeployment) {
				updatedDep = d.DeepCopy()
				updatedDep.Spec.LogLevel++
				err := tc.c.Update(tc.ctx, updatedDep)
				require.NoError(t, err, "failed to update deployment")
			}
			tc.rc.(*deployment.ReconcileDeployment).AddHook(&hook)

			conditions := map[api.DeploymentConditionType]corev1.ConditionStatus{
				api.DriverDeployed: corev1.ConditionTrue,
			}

			testReconcilePhase(t, tc.rc, tc.c, d.name, false, false, api.DeploymentPhaseRunning)
			validateDriver(tc, dep, []string{api.EventReasonNew, api.EventReasonRunning}, false)
			validateConditions(tc, d.name, conditions)

			tc.rc.(*deployment.ReconcileDeployment).RemoveHook(&hook)

			// Next reconcile phase should catch the deployment changes
			testReconcilePhase(t, tc.rc, tc.c, d.name, false, false, api.DeploymentPhaseRunning)
			validateDriver(tc, updatedDep, []string{api.EventReasonNew, api.EventReasonRunning}, true)
			validateConditions(tc, d.name, conditions)
		})

		t.Run("updating", func(t *testing.T) {
			t.Parallel()
			for _, testcase := range testcases.UpdateTests() {
				testcase := testcase
				t.Run(testcase.Name, func(t *testing.T) {
					testIt := func(restart bool) {
						tc := setup(t)
						defer teardown(tc)
						dep := testcase.Deployment.DeepCopyObject().(*api.PmemCSIDeployment)

						// Assumption is that all the testcases are positive cases.
						conditions := map[api.DeploymentConditionType]corev1.ConditionStatus{
							api.DriverDeployed: corev1.ConditionTrue,
						}
						// When working with the fake client, we need to make up a UID.
						dep.UID = types.UID("fake-uid-" + dep.Name)

						err := tc.c.Create(tc.ctx, dep)
						require.NoError(t, err, "create deployment")

						testReconcilePhase(t, tc.rc, tc.c, dep.Name, false, false, api.DeploymentPhaseRunning)
						validateDriver(tc, dep, []string{api.EventReasonNew, api.EventReasonRunning}, false)

						// Reconcile now should keep phase as running.
						testReconcilePhase(t, tc.rc, tc.c, dep.Name, false, false, api.DeploymentPhaseRunning)
						validateDriver(tc, dep, []string{api.EventReasonNew, api.EventReasonRunning}, false)
						validateEvents(tc, dep, []string{api.EventReasonNew, api.EventReasonRunning})
						validateConditions(tc, dep.Name, conditions)

						// Retrieve existing object before updating it.
						err = tc.c.Get(tc.ctx, types.NamespacedName{Name: dep.Name}, dep)
						require.NoError(t, err, "retrive existing deployment object")

						if restart {
							// Simulate restarting the operator by creating a new instance.
							tc.ResetReconciler()
						}

						// Update.
						testcase.Mutate(dep)
						err = tc.c.Update(tc.ctx, dep)
						require.NoError(t, err, "update deployment")

						// Reconcile is expected to not fail.
						testReconcilePhase(t, tc.rc, tc.c, dep.Name, false, false, api.DeploymentPhaseRunning)

						// Recheck the container resources are updated
						validateDriver(tc, dep, []string{api.EventReasonNew, api.EventReasonRunning}, true)
					}

					t.Parallel()

					t.Run("while running", func(t *testing.T) {
						testIt(false)
					})

					t.Run("while stopped", func(t *testing.T) {
						testIt(true)
					})
				})
			}
		})

		t.Run("delete obsolete objects", func(t *testing.T) {
			tc := setup(t)
			defer teardown(tc)
			d := &pmemDeployment{
				name: "test-driver-upgrades",
			}

			dep := getDeployment(d)

			err := tc.c.Create(tc.ctx, dep)
			require.NoError(t, err, "failed to create deployment")
			testReconcilePhase(t, tc.rc, tc.c, d.name, false, false, api.DeploymentPhaseRunning)
			validateDriver(tc, dep, []string{api.EventReasonNew, api.EventReasonRunning}, false)
			validateConditions(tc, d.name, map[api.DeploymentConditionType]corev1.ConditionStatus{
				api.DriverDeployed: corev1.ConditionTrue,
			})

			err = tc.c.Get(tc.ctx, client.ObjectKey{Name: d.name}, dep)
			require.NoError(t, err, "get deployment")

			cm1 := &corev1.ConfigMap{
				TypeMeta: metav1.TypeMeta{
					Kind:       "ConfigMap",
					APIVersion: "v1",
				},
				ObjectMeta: metav1.ObjectMeta{
					Name: dep.GetHyphenedName(),
					OwnerReferences: []metav1.OwnerReference{
						dep.GetOwnerReference(),
					},
					Namespace: testNamespace,
				},
				Data: map[string]string{},
			}
			err = tc.c.Create(tc.ctx, cm1)
			require.NoError(t, err, "create configmap owned by deployment")

			cm2 := &corev1.ConfigMap{
				TypeMeta: metav1.TypeMeta{
					Kind:       "ConfigMap",
					APIVersion: "v1",
				},
				ObjectMeta: metav1.ObjectMeta{
					Name:      "unrelated-cm",
					Namespace: testNamespace,
				},
				Data: map[string]string{},
			}
			err = tc.c.Create(tc.ctx, cm2)
			require.NoError(t, err, "create configmap: %s", cm2.Name)

			// Use a fresh reconciler to mimic operator restart
			tc.ResetReconciler()

			// A fresh reconcile should delete the newly created above ConfigMap
			testReconcilePhase(t, tc.rc, tc.c, d.name, false, false, api.DeploymentPhaseRunning)
			err = tc.c.Get(tc.ctx, client.ObjectKey{Name: d.name}, dep)
			require.NoError(t, err, "get deployment")
			// It is debatable whether the operator should update all objects after
			// a restart. Currently it does.
			validateDriver(tc, dep, []string{api.EventReasonNew, api.EventReasonRunning}, true)

			cm := &corev1.ConfigMap{}
			err = tc.c.Get(tc.ctx, client.ObjectKey{Name: cm1.Name, Namespace: testNamespace}, cm)
			require.Errorf(t, err, "get '%s' config map after reconcile", cm1.Name)
			require.True(t, errors.IsNotFound(err), "config map not found after reconcile")

			// operator should not delete the objects unrelated to any deployment
			err = tc.c.Get(tc.ctx, client.ObjectKey{Name: cm2.Name, Namespace: testNamespace}, cm)
			require.NoErrorf(t, err, "get '%s' config map after reconcile", cm2.Name)
		})

		t.Run("recover from unexpected shutdown", func(t *testing.T) {
			tc := setup(t)
			defer teardown(tc)

			for _, obj := range deployment.CurrentObjects() {
				gvk := obj.GetObjectKind().GroupVersionKind()
				tc.c.(*testClient).InjectPanicOn(&gvk)

				d := &pmemDeployment{
					name: "test-panic-" + strings.ToLower(gvk.Kind),
				}
				dep := getDeployment(d)
				err := tc.c.Create(tc.ctx, dep)
				require.NoError(t, err, "create deployment")

				req := reconcile.Request{
					NamespacedName: types.NamespacedName{
						Name: d.name,
					},
				}

				defer func() {
					r := recover()
					require.NotNil(t, r, "expected to recover from panic")

					tc.c.(*testClient).InjectPanicOn(nil)
					// mimic operator restart
					tc.ResetReconciler()
					testReconcilePhase(t, tc.rc, tc.c, d.name, false, false, api.DeploymentPhaseRunning)
					validateDriver(tc, dep, []string{api.EventReasonNew, api.EventReasonRunning}, false)
				}()

				tc.rc.Reconcile(req)
			}
		})
	}

	t.Parallel()

	// Validate for all supported Kubernetes versions.
	versions := map[version.Version]bool{}
	for _, yaml := range deploy.ListAll() {
		versions[yaml.Kubernetes] = true
	}
	for version := range versions {
		version := version
		t.Run(fmt.Sprintf("Kubernetes %v", version), func(t *testing.T) {
			testIt(t, version)
		})
	}
}

type testClient struct {
	client.Client
	assertOn *schema.GroupVersionKind
}

func newTestClient(initObjs ...runtime.Object) client.Client {
	return &testClient{Client: fake.NewFakeClient(initObjs...)}
}

func (t *testClient) InjectPanicOn(gvk *schema.GroupVersionKind) {
	t.assertOn = gvk
}

// Create adds given obj to its object tracking list.
// It panics if the object type matches with the type of 'assertOn'
// that was previously set using InjectPanicOn()
func (t *testClient) Create(ctx context.Context, obj runtime.Object, opts ...client.CreateOption) error {
	if t.assertOn != nil && obj.GetObjectKind().GroupVersionKind() == *t.assertOn {
		panic(fmt.Sprintf("assert: %v", obj.GetObjectKind()))
	}
	return t.Client.Create(ctx, obj, opts...)
}
