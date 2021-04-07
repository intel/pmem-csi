/*
Copyright 2020 The Kubernetes Authors.

SPDX-License-Identifier: Apache-2.0
*/

package deployment

import (
	"context"
	"fmt"
	"reflect"

	api "github.com/intel/pmem-csi/pkg/apis/pmemcsi/v1beta1"
	"github.com/intel/pmem-csi/pkg/logger"
	"github.com/intel/pmem-csi/pkg/types"
	"github.com/intel/pmem-csi/pkg/version"

	admissionregistrationv1 "k8s.io/api/admissionregistration/v1"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	storagev1 "k8s.io/api/storage/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	apiruntime "k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/klog/v2"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

const (
	controllerServicePort  = 10000
	controllerMetricsPort  = 10010
	nodeControllerPort     = 10001
	nodeMetricsPort        = 10010
	provisionerMetricsPort = 10011
	schedulerPort          = 8000
)

func typeMeta(gv schema.GroupVersion, kind string) metav1.TypeMeta {
	return metav1.TypeMeta{
		APIVersion: gv.String(),
		Kind:       kind,
	}
}

// A list of all currently created objects. This must be kept in sync
// with the code in Reconcile(). When removing a type here, it must be
// copied to obsoleteObjects below.
//
// The RBAC rules in deploy/kustomize/operator/operator.yaml must
// allow all of the operations (creation, patching, etc.).
var currentObjects = []client.Object{
	&rbacv1.ClusterRole{TypeMeta: typeMeta(rbacv1.SchemeGroupVersion, "ClusterRole")},
	&rbacv1.ClusterRoleBinding{TypeMeta: typeMeta(rbacv1.SchemeGroupVersion, "ClusterRoleBinding")},
	&storagev1.CSIDriver{TypeMeta: typeMeta(storagev1.SchemeGroupVersion, "CSIDriver")},
	&appsv1.DaemonSet{TypeMeta: typeMeta(appsv1.SchemeGroupVersion, "DaemonSet")},
	&rbacv1.Role{TypeMeta: typeMeta(rbacv1.SchemeGroupVersion, "Role")},
	&rbacv1.RoleBinding{TypeMeta: typeMeta(rbacv1.SchemeGroupVersion, "RoleBinding")},
	&corev1.Secret{TypeMeta: typeMeta(corev1.SchemeGroupVersion, "Secret")},
	&corev1.Service{TypeMeta: typeMeta(corev1.SchemeGroupVersion, "Service")},
	&corev1.ServiceAccount{TypeMeta: typeMeta(corev1.SchemeGroupVersion, "ServiceAccount")},
	&appsv1.StatefulSet{TypeMeta: typeMeta(appsv1.SchemeGroupVersion, "StatefulSet")},
	&admissionregistrationv1.MutatingWebhookConfiguration{TypeMeta: typeMeta(admissionregistrationv1.SchemeGroupVersion, "MutatingWebhookConfiguration")},
}

func cloneObject(from client.Object) (client.Object, error) {
	switch t := from.(type) {
	case *rbacv1.ClusterRole:
		return t.DeepCopyObject().(*rbacv1.ClusterRole), nil
	case *rbacv1.ClusterRoleBinding:
		return t.DeepCopyObject().(*rbacv1.ClusterRoleBinding), nil
	case *storagev1.CSIDriver:
		return t.DeepCopyObject().(*storagev1.CSIDriver), nil
	case *appsv1.DaemonSet:
		return t.DeepCopyObject().(*appsv1.DaemonSet), nil
	case *rbacv1.Role:
		return t.DeepCopyObject().(*rbacv1.Role), nil
	case *rbacv1.RoleBinding:
		return t.DeepCopyObject().(*rbacv1.RoleBinding), nil
	case *corev1.Secret:
		return t.DeepCopyObject().(*corev1.Secret), nil
	case *corev1.Service:
		return t.DeepCopyObject().(*corev1.Service), nil
	case *corev1.ServiceAccount:
		return t.DeepCopyObject().(*corev1.ServiceAccount), nil
	case *appsv1.StatefulSet:
		return t.DeepCopyObject().(*appsv1.StatefulSet), nil
	case *admissionregistrationv1.MutatingWebhookConfiguration:
		return t.DeepCopyObject().(*admissionregistrationv1.MutatingWebhookConfiguration), nil
	default:
		return nil, fmt.Errorf("cannot clone client.Object of type %T", from)
	}
}

// CurrentObjects returns the active sub-object types used by the operator
// for a driver deployment.
func CurrentObjects() []client.Object {
	return currentObjects
}

// A list of objects that may have been created by a previous release
// of the operator. This is relevant when updating from such an older
// release to the current one, because the current one must remove
// obsolete objects.
//
// The RBAC rules in deploy/kustomize/operator/operator.yaml must
// allow listing and removing of these objects.
var obsoleteObjects = []client.Object{
	&corev1.ConfigMap{TypeMeta: typeMeta(corev1.SchemeGroupVersion, "ConfigMap")}, // included only for testing purposes
}

// A list of all object types potentially created by the operator,
// in this or any previous release. In other words, this list may grow,
// but never shrink.
var allObjects = append(currentObjects[:], obsoleteObjects...)

// Returns a slice with a new unstructured.UnstructuredList for each object
// in allObjects.
func AllObjectLists() []*unstructured.UnstructuredList {
	var lists []*unstructured.UnstructuredList
	for _, obj := range allObjects {
		gvk := obj.GetObjectKind().GroupVersionKind()
		gvk.Kind += "List"
		list := &unstructured.UnstructuredList{}
		list.SetGroupVersionKind(gvk)
		lists = append(lists, list)
	}
	return lists
}

// pmemCSIDeployment represents the desired state of a PMEM-CSI driver
// deployment. Conditions in the embedded PmemCSIDeployment will get updated.
type pmemCSIDeployment struct {
	*api.PmemCSIDeployment
	// operator's namespace used for creating sub-resources
	namespace  string
	k8sVersion version.Version

	controllerCABundle []byte
}

// Reconcile reconciles the driver deployment. When adding new
// objects, extend also currentObjects above and the RBAC rules in
// deploy/kustomize/operator/operator.yaml.
func (d *pmemCSIDeployment) reconcile(ctx context.Context, r *ReconcileDeployment) error {
	l := logger.Get(ctx).WithName("reconcile")
	l.V(3).Info("start", "deployment", d.Name, "phase", d.Status.Phase)
	var allObjects []apiruntime.Object
	redeployAll := func() error {
		for name, handler := range subObjectHandlers {
			if handler.enabled != nil && !handler.enabled(d) {
				continue
			}
			o, err := d.redeploy(ctx, r, handler)
			if err != nil {
				return fmt.Errorf("failed to update %s: %v", name, err)
			}
			allObjects = append(allObjects, o)
		}
		return nil
	}

	if err := redeployAll(); err != nil {
		d.SetCondition(api.DriverDeployed, corev1.ConditionFalse, err.Error())
		return err
	}

	d.SetCondition(api.DriverDeployed, corev1.ConditionTrue, "Driver deployed successfully.")

	l.V(3).Info("deployed", "numObjects", len(allObjects))
	// FIXME(avalluri): Limit the obsolete object deletion either only on version upgrades
	// or on operator restart.
	if err := d.deleteObsoleteObjects(ctx, r, allObjects); err != nil {
		return fmt.Errorf("Delete obsolete objects failed with error: %v", err)
	}

	return nil
}

// getSubObject retrieves the latest revision of given object type from the API server
// And checks if that object is owned by the current deployment CR
func (d *pmemCSIDeployment) getSubObject(ctx context.Context, r *ReconcileDeployment, obj client.Object) error {
	objMeta, err := meta.Accessor(obj)
	if err != nil {
		return fmt.Errorf("internal error %T: %v", obj, err)
	}
	l := logger.Get(ctx).WithName("getSubObject")

	l.V(3).Info("get", "object", logger.KObjWithType(objMeta))
	if err := r.Get(obj); err != nil {
		if errors.IsNotFound(err) {
			l.V(3).Info("not found", logger.KObjWithType(objMeta))
			return nil
		}
		return err
	}
	ownerRef := d.GetOwnerReference()
	if !isOwnedBy(objMeta, &ownerRef) {
		return fmt.Errorf("'%s' of type %T is not owned by '%s'", objMeta.GetName(), obj, ownerRef.Name)
	}

	return nil
}

type redeployObject struct {
	objType    reflect.Type
	enabled    func(*pmemCSIDeployment) bool
	object     func(*pmemCSIDeployment) client.Object
	modify     func(*pmemCSIDeployment, client.Object) error
	postUpdate func(*pmemCSIDeployment, client.Object) error
}

// redeploy creates or patches one sub-object so that it matches
// the PmemCSIDeployment spec.
//  1.
//  2. Retrieve the latest data saved at APIServer for that object.
//  3. Create an objectPatch for that object to record the changes from this point.
//  4. Call ro.modify() to modify the object's data.
//  5. Call objectPatch.Apply() to submit the chanages to the APIServer.
//  6. If the update in step-5 was success, then call the ro.postUpdate() callback
//     to run any post update steps.
func (d *pmemCSIDeployment) redeploy(ctx context.Context, r *ReconcileDeployment, ro redeployObject) (finalObj client.Object, finalErr error) {
	l := logger.Get(ctx).WithName("redeploy")

	// Get an instance with right type and meta data, prepare for logging.
	o := ro.object(d)
	if o == nil {
		return nil, fmt.Errorf("nil object")
	}
	l = l.WithValues("object", klog.KObj(o))
	ctx = logger.Set(ctx, l)

	// Retrieve actual object from APIserver, it it exists.
	if err := d.getSubObject(ctx, r, o); err != nil {
		return nil, err
	}

	// Prepare for patching by remembering the base object.
	patch := client.MergeFrom(o.DeepCopyObject())

	// Now set all values that we care about...
	if err := ro.modify(d, o); err != nil {
		return nil, err
	}

	// ... and also the labels.
	labels := o.GetLabels()
	if labels == nil {
		labels = map[string]string{}
	}
	for key, value := range d.Spec.Labels {
		labels[key] = value
	}
	o.SetLabels(labels)

	// Now create or patch the object. If we have a resource
	// version, then the object was retrieved from the apiserver
	// and can be patched.
	if o.GetResourceVersion() != "" {
		// Patch.
		data, err := patch.Data(o)
		if err != nil {
			return nil, fmt.Errorf("generate patch: %v", err)
		}
		// Check whether we really need to patch.
		if string(data) != "{}" && len(data) >= 0 {
			// Patch() will modify the object, which is an object that was
			// generated from our PmemCSIDeployment object and shares some
			// data structure with it. We don't want those to be modified,
			// so here we have to do a deep copy first.
			copy, err := cloneObject(o)
			if err != nil {
				return nil, fmt.Errorf("internal error: %v", err)
			}
			l.V(3).Info("update", "patch", string(data))
			if err := r.client.Patch(ctx, copy, patch); err != nil {
				return nil, fmt.Errorf("patch object: %v", err)
			}
		}
	} else {
		// For unknown reason client.Create() clearing off the
		// GVK on obj, so restore it manually.
		gvk := o.GetObjectKind().GroupVersionKind()
		l.V(3).Info("create")
		if err := r.client.Create(ctx, o); err != nil {
			return nil, fmt.Errorf("create object: %v", err)
		}
		o.GetObjectKind().SetGroupVersionKind(gvk)
	}

	// Final per-object changes, like emitting events or setting status.
	if ro.postUpdate != nil {
		if err := ro.postUpdate(d, o); err != nil {
			return nil, err
		}
	}
	return o, nil
}

func mutatingWebhookEnabled(d *pmemCSIDeployment) bool {
	return d.Spec.ControllerTLSSecret != "" && d.Spec.MutatePods != api.MutatePodsNever
}

var subObjectHandlers = map[string]redeployObject{
	"node driver": {
		objType: reflect.TypeOf(&appsv1.DaemonSet{}),
		object: func(d *pmemCSIDeployment) client.Object {
			return &appsv1.DaemonSet{
				TypeMeta:   metav1.TypeMeta{Kind: "DaemonSet", APIVersion: "apps/v1"},
				ObjectMeta: d.getObjectMeta(d.NodeDriverName(), false),
			}
		},
		modify: func(d *pmemCSIDeployment, o client.Object) error {
			d.getNodeDaemonSet(o.(*appsv1.DaemonSet))
			return nil
		},
		postUpdate: func(d *pmemCSIDeployment, o client.Object) error {
			ds := o.(*appsv1.DaemonSet)
			// Update node driver status is status object
			status := "NotReady"
			reason := "Unknown"
			if ds.Status.NumberAvailable == 0 {
				reason = "Node daemon set has not started yet."
			} else if ds.Status.NumberReady == ds.Status.NumberAvailable {
				status = "Ready"
				reason = fmt.Sprintf("All %d node driver pod(s) running successfully", ds.Status.NumberAvailable)
			} else {
				reason = fmt.Sprintf("%d out of %d driver pods are ready", ds.Status.NumberReady, ds.Status.NumberAvailable)
			}
			d.SetDriverStatus(api.NodeDriver, status, reason)
			return nil
		},
	},
	"controller driver": {
		objType: reflect.TypeOf(&appsv1.StatefulSet{}),
		object: func(d *pmemCSIDeployment) client.Object {
			return &appsv1.StatefulSet{
				TypeMeta:   metav1.TypeMeta{Kind: "StatefulSet", APIVersion: "apps/v1"},
				ObjectMeta: d.getObjectMeta(d.ControllerDriverName(), false),
			}
		},
		modify: func(d *pmemCSIDeployment, o client.Object) error {
			d.getControllerStatefulSet(o.(*appsv1.StatefulSet))
			return nil
		},
		postUpdate: func(d *pmemCSIDeployment, o client.Object) error {
			ss := o.(*appsv1.StatefulSet)
			// Update controller status is status object
			status := "NotReady"
			reason := "Unknown"
			if ss.Status.Replicas == 0 {
				reason = "Controller stateful set has not started yet."
			} else if ss.Status.ReadyReplicas == ss.Status.Replicas {
				status = "Ready"
				reason = fmt.Sprintf("%d instance(s) of controller driver is running successfully", ss.Status.ReadyReplicas)
			} else {
				reason = fmt.Sprintf("Waiting for stateful set to be ready: %d of %d replicas are ready",
					ss.Status.ReadyReplicas, ss.Status.Replicas)
			}
			d.SetDriverStatus(api.ControllerDriver, status, reason)
			return nil
		},
	},
	"controller service": {
		objType: reflect.TypeOf(&corev1.Service{}),
		object: func(d *pmemCSIDeployment) client.Object {
			return &corev1.Service{
				TypeMeta:   metav1.TypeMeta{Kind: "Service", APIVersion: "v1"},
				ObjectMeta: d.getObjectMeta(d.ControllerServiceName(), false),
			}
		},
		modify: func(d *pmemCSIDeployment, o client.Object) error {
			d.getControllerService(o.(*corev1.Service))
			return nil
		},
	},
	"metrics service": {
		objType: reflect.TypeOf(&corev1.Service{}),
		object: func(d *pmemCSIDeployment) client.Object {
			return &corev1.Service{
				TypeMeta:   metav1.TypeMeta{Kind: "Service", APIVersion: "v1"},
				ObjectMeta: d.getObjectMeta(d.MetricsServiceName(), false),
			}
		},
		modify: func(d *pmemCSIDeployment, o client.Object) error {
			d.getMetricsService(o.(*corev1.Service))
			return nil
		},
	},
	"CSIDriver": {
		objType: reflect.TypeOf(&storagev1.CSIDriver{}),
		object: func(d *pmemCSIDeployment) client.Object {
			return &storagev1.CSIDriver{
				TypeMeta:   metav1.TypeMeta{Kind: "CSIDriver", APIVersion: "storage.k8s.io/v1"},
				ObjectMeta: d.getObjectMeta(d.CSIDriverName(), true),
			}
		},
		modify: func(d *pmemCSIDeployment, o client.Object) error {
			d.getCSIDriver(o.(*storagev1.CSIDriver))
			return nil
		},
	},
	"webhooks role": {
		objType: reflect.TypeOf(&rbacv1.Role{}),
		object: func(d *pmemCSIDeployment) client.Object {
			return &rbacv1.Role{
				TypeMeta:   metav1.TypeMeta{Kind: "Role", APIVersion: "rbac.authorization.k8s.io/v1"},
				ObjectMeta: d.getObjectMeta(d.WebhooksRoleName(), false),
			}
		},
		modify: func(d *pmemCSIDeployment, o client.Object) error {
			d.getWebhooksRole(o.(*rbacv1.Role))
			return nil
		},
	},
	"webhooks role binding": {
		objType: reflect.TypeOf(&rbacv1.RoleBinding{}),
		object: func(d *pmemCSIDeployment) client.Object {
			return &rbacv1.RoleBinding{
				TypeMeta:   metav1.TypeMeta{Kind: "RoleBinding", APIVersion: "rbac.authorization.k8s.io/v1"},
				ObjectMeta: d.getObjectMeta(d.WebhooksRoleBindingName(), false),
			}
		},
		modify: func(d *pmemCSIDeployment, o client.Object) error {
			d.getWebhooksRoleBinding(o.(*rbacv1.RoleBinding))
			return nil
		},
	},
	"webhooks cluster role": {
		objType: reflect.TypeOf(&rbacv1.ClusterRole{}),
		object: func(d *pmemCSIDeployment) client.Object {
			return &rbacv1.ClusterRole{
				TypeMeta:   metav1.TypeMeta{Kind: "ClusterRole", APIVersion: "rbac.authorization.k8s.io/v1"},
				ObjectMeta: d.getObjectMeta(d.WebhooksClusterRoleName(), true),
			}
		},
		modify: func(d *pmemCSIDeployment, o client.Object) error {
			d.getWebhooksClusterRole(o.(*rbacv1.ClusterRole))
			return nil
		},
	},
	"webhooks cluster role binding": {
		objType: reflect.TypeOf(&rbacv1.ClusterRoleBinding{}),
		object: func(d *pmemCSIDeployment) client.Object {
			return &rbacv1.ClusterRoleBinding{
				TypeMeta:   metav1.TypeMeta{Kind: "ClusterRoleBinding", APIVersion: "rbac.authorization.k8s.io/v1"},
				ObjectMeta: d.getObjectMeta(d.WebhooksClusterRoleBindingName(), true),
			}
		},
		modify: func(d *pmemCSIDeployment, o client.Object) error {
			d.getWebhooksClusterRoleBinding(o.(*rbacv1.ClusterRoleBinding))
			return nil
		},
	},
	"webhooks service account": {
		objType: reflect.TypeOf(&corev1.ServiceAccount{}),
		object: func(d *pmemCSIDeployment) client.Object {
			return &corev1.ServiceAccount{
				TypeMeta:   metav1.TypeMeta{Kind: "ServiceAccount", APIVersion: "v1"},
				ObjectMeta: d.getObjectMeta(d.WebhooksServiceAccountName(), false),
			}
		},
		modify: func(d *pmemCSIDeployment, o client.Object) error {
			// nothing to customize for service account
			return nil
		},
	},
	"mutating webhook configuration": {
		objType: reflect.TypeOf(&admissionregistrationv1.MutatingWebhookConfiguration{}),
		enabled: mutatingWebhookEnabled,
		object: func(d *pmemCSIDeployment) client.Object {
			return &admissionregistrationv1.MutatingWebhookConfiguration{
				TypeMeta:   metav1.TypeMeta{Kind: "MutatingWebhookConfiguration", APIVersion: "admissionregistration.k8s.io/v1"},
				ObjectMeta: d.getObjectMeta(d.MutatingWebhookName(), true),
			}
		},
		modify: func(d *pmemCSIDeployment, o client.Object) error {
			d.getMutatingWebhookConfig(o.(*admissionregistrationv1.MutatingWebhookConfiguration))
			return nil
		},
	},
	"scheduler service": {
		objType: reflect.TypeOf(&corev1.Service{}),
		object: func(d *pmemCSIDeployment) client.Object {
			return &corev1.Service{
				TypeMeta:   metav1.TypeMeta{Kind: "Service", APIVersion: "v1"},
				ObjectMeta: d.getObjectMeta(d.SchedulerServiceName(), false),
			}
		},
		modify: func(d *pmemCSIDeployment, o client.Object) error {
			d.getSchedulerService(o.(*corev1.Service))
			return nil
		},
	},
	"provisioner role": {
		objType: reflect.TypeOf(&rbacv1.Role{}),
		object: func(d *pmemCSIDeployment) client.Object {
			return &rbacv1.Role{
				TypeMeta:   metav1.TypeMeta{Kind: "Role", APIVersion: "rbac.authorization.k8s.io/v1"},
				ObjectMeta: d.getObjectMeta(d.ProvisionerRoleName(), false),
			}
		},
		modify: func(d *pmemCSIDeployment, o client.Object) error {
			d.getControllerProvisionerRole(o.(*rbacv1.Role))
			return nil
		},
	},
	"provisioner role binding": {
		objType: reflect.TypeOf(&rbacv1.RoleBinding{}),
		object: func(d *pmemCSIDeployment) client.Object {
			return &rbacv1.RoleBinding{
				TypeMeta:   metav1.TypeMeta{Kind: "RoleBinding", APIVersion: "rbac.authorization.k8s.io/v1"},
				ObjectMeta: d.getObjectMeta(d.ProvisionerRoleBindingName(), false),
			}
		},
		modify: func(d *pmemCSIDeployment, o client.Object) error {
			d.getControllerProvisionerRoleBinding(o.(*rbacv1.RoleBinding))
			return nil
		},
	},
	"provisioner cluster role": {
		objType: reflect.TypeOf(&rbacv1.ClusterRole{}),
		object: func(d *pmemCSIDeployment) client.Object {
			return &rbacv1.ClusterRole{
				TypeMeta:   metav1.TypeMeta{Kind: "ClusterRole", APIVersion: "rbac.authorization.k8s.io/v1"},
				ObjectMeta: d.getObjectMeta(d.ProvisionerClusterRoleName(), true),
			}
		},
		modify: func(d *pmemCSIDeployment, o client.Object) error {
			d.getControllerProvisionerClusterRole(o.(*rbacv1.ClusterRole))
			return nil
		},
	},
	"provisioner cluster role binding": {
		objType: reflect.TypeOf(&rbacv1.ClusterRoleBinding{}),
		object: func(d *pmemCSIDeployment) client.Object {
			return &rbacv1.ClusterRoleBinding{
				TypeMeta:   metav1.TypeMeta{Kind: "ClusterRoleBinding", APIVersion: "rbac.authorization.k8s.io/v1"},
				ObjectMeta: d.getObjectMeta(d.ProvisionerClusterRoleBindingName(), true),
			}
		},
		modify: func(d *pmemCSIDeployment, o client.Object) error {
			d.getControllerProvisionerClusterRoleBinding(o.(*rbacv1.ClusterRoleBinding))
			return nil
		},
	},
	"provisioner service account": {
		objType: reflect.TypeOf(&corev1.ServiceAccount{}),
		object: func(d *pmemCSIDeployment) client.Object {
			return &corev1.ServiceAccount{
				TypeMeta:   metav1.TypeMeta{Kind: "ServiceAccount", APIVersion: "v1"},
				ObjectMeta: d.getObjectMeta(d.ProvisionerServiceAccountName(), false),
			}
		},
		modify: func(d *pmemCSIDeployment, o client.Object) error {
			// nothing to customize for service account
			return nil
		},
	},

	"node setup cluster role": {
		objType: reflect.TypeOf(&rbacv1.ClusterRole{}),
		object: func(d *pmemCSIDeployment) client.Object {
			return &rbacv1.ClusterRole{
				TypeMeta:   metav1.TypeMeta{Kind: "ClusterRole", APIVersion: "rbac.authorization.k8s.io/v1"},
				ObjectMeta: d.getObjectMeta(d.NodeSetupClusterRoleName(), true),
			}
		},
		modify: func(d *pmemCSIDeployment, o client.Object) error {
			d.getNodeSetupClusterRole(o.(*rbacv1.ClusterRole))
			return nil
		},
	},
	"node setup cluster role binding": {
		objType: reflect.TypeOf(&rbacv1.ClusterRoleBinding{}),
		object: func(d *pmemCSIDeployment) client.Object {
			return &rbacv1.ClusterRoleBinding{
				TypeMeta:   metav1.TypeMeta{Kind: "ClusterRoleBinding", APIVersion: "rbac.authorization.k8s.io/v1"},
				ObjectMeta: d.getObjectMeta(d.NodeSetupClusterRoleBindingName(), true),
			}
		},
		modify: func(d *pmemCSIDeployment, o client.Object) error {
			d.getNodeSetupClusterRoleBinding(o.(*rbacv1.ClusterRoleBinding))
			return nil
		},
	},
	"node setup service account": {
		objType: reflect.TypeOf(&corev1.ServiceAccount{}),
		object: func(d *pmemCSIDeployment) client.Object {
			return &corev1.ServiceAccount{
				TypeMeta:   metav1.TypeMeta{Kind: "ServiceAccount", APIVersion: "v1"},
				ObjectMeta: d.getObjectMeta(d.NodeSetupServiceAccountName(), false),
			}
		},
		modify: func(d *pmemCSIDeployment, o client.Object) error {
			// nothing to customize for service account
			return nil
		},
	},
	"node setup driver": {
		objType: reflect.TypeOf(&appsv1.DaemonSet{}),
		object: func(d *pmemCSIDeployment) client.Object {
			return &appsv1.DaemonSet{
				TypeMeta:   metav1.TypeMeta{Kind: "DaemonSet", APIVersion: "apps/v1"},
				ObjectMeta: d.getObjectMeta(d.NodeSetupName(), false),
			}
		},
		modify: func(d *pmemCSIDeployment, o client.Object) error {
			d.getNodeSetupDaemonSet(o.(*appsv1.DaemonSet))
			return nil
		},
	},
}

var v1SecretPtr = reflect.TypeOf(&corev1.Secret{})

// HandleEvent handles the delete/update events received on sub-objects. It ensures that any undesirable change
// is reverted.
func (d *pmemCSIDeployment) handleEvent(ctx context.Context, metaData metav1.Object, obj apiruntime.Object, r *ReconcileDeployment) error {
	objType := reflect.TypeOf(obj)
	l := logger.Get(ctx).WithName("deployment/event")
	l.V(5).Info("start", "object", logger.KObjWithType(metaData), "type", objType)

	objName := metaData.GetName()
	for name, handler := range subObjectHandlers {
		if handler.enabled != nil && !handler.enabled(d) {
			continue
		}
		if objType != handler.objType {
			continue
		}
		metaObj, _ := meta.Accessor(handler.object(d))
		if objName != metaObj.GetName() {
			continue
		}
		l.V(3).Info("redeploying", "name", name, "object", logger.KObjWithType(metaData))
		org := d.DeepCopy()
		if _, err := d.redeploy(ctx, r, handler); err != nil {
			return fmt.Errorf("failed to redeploy %s: %v", name, err)
		}
		if err := r.patchDeploymentStatus(d.PmemCSIDeployment, client.MergeFrom(org)); err != nil {
			return fmt.Errorf("failed to update deployment CR status: %v", err)
		}
	}

	return nil
}

func objectIsObsolete(ctx context.Context, objList []apiruntime.Object, toFind unstructured.Unstructured) (bool, error) {
	l := logger.Get(ctx)
	l.V(5).Info("checking for obsolete object", "name", toFind.GetName(), "gkv", toFind.GetObjectKind().GroupVersionKind())
	for i := range objList {
		metaObj, err := meta.Accessor(objList[i])
		if err != nil {
			return false, err
		}
		if metaObj.GetName() == toFind.GetName() &&
			objList[i].GetObjectKind().GroupVersionKind() == toFind.GetObjectKind().GroupVersionKind() {
			return false, nil
		}
	}

	return true, nil
}

func (d *pmemCSIDeployment) isOwnerOf(obj unstructured.Unstructured) bool {
	for _, owner := range obj.GetOwnerReferences() {
		if owner.UID == d.GetUID() {
			return true
		}
	}

	return false
}

func (d *pmemCSIDeployment) deleteObsoleteObjects(ctx context.Context, r *ReconcileDeployment, newObjects []apiruntime.Object) error {
	l := logger.Get(ctx).WithName("deleteObsoleteObjects")
	for _, obj := range newObjects {
		metaObj, _ := meta.Accessor(obj)
		l.V(5).Info("new object", "name", metaObj.GetName(), "gkv", obj.GetObjectKind().GroupVersionKind())
	}

	for _, list := range AllObjectLists() {
		opts := &client.ListOptions{
			Namespace: d.namespace,
		}

		l.V(5).Info("fetching objects", "gkv", list.GetObjectKind(), "options", opts.Namespace)
		if err := r.client.List(ctx, list, opts); err != nil {
			return err
		}

		for _, obj := range list.Items {
			if !d.isOwnerOf(obj) {
				continue
			}
			obsolete, err := objectIsObsolete(ctx, newObjects, obj)
			if err != nil {
				return err
			}
			if !obsolete {
				continue
			}
			l.V(3).Info("deleting obsolete object", "name", obj.GetName(), "gkv", obj.GetObjectKind().GroupVersionKind())
			if err := r.Delete(&obj); err != nil && !errors.IsNotFound(err) {
				return err
			}
		}
	}
	return nil
}

func (d *pmemCSIDeployment) getCSIDriver(csiDriver *storagev1.CSIDriver) {
	attachRequired := false
	podInfoOnMount := true

	csiDriver.Spec = storagev1.CSIDriverSpec{
		AttachRequired: &attachRequired,
		PodInfoOnMount: &podInfoOnMount,
	}

	// Volume lifecycle modes are supported only after k8s v1.16
	if d.k8sVersion.Compare(1, 16) >= 0 {
		csiDriver.Spec.VolumeLifecycleModes = []storagev1.VolumeLifecycleMode{
			storagev1.VolumeLifecyclePersistent,
			storagev1.VolumeLifecycleEphemeral,
		}
	}
}

func (d *pmemCSIDeployment) getService(service *corev1.Service, t corev1.ServiceType, port int32) {
	service.Spec.Type = t
	if service.Spec.Ports == nil {
		service.Spec.Ports = append(service.Spec.Ports, corev1.ServicePort{})
	}
	service.Spec.Ports[0].Port = port
	service.Spec.Ports[0].TargetPort = intstr.IntOrString{
		IntVal: port,
	}
	service.Spec.Selector = map[string]string{
		"app.kubernetes.io/name":     "pmem-csi-controller",
		"app.kubernetes.io/instance": d.Name,
	}
}

func (d *pmemCSIDeployment) getControllerService(service *corev1.Service) {
	d.getService(service, corev1.ServiceTypeClusterIP, controllerServicePort)
}

func (d *pmemCSIDeployment) getMetricsService(service *corev1.Service) {
	d.getService(service, corev1.ServiceTypeNodePort, controllerMetricsPort)
}

func (d *pmemCSIDeployment) getWebhooksRole(role *rbacv1.Role) {
	role.Rules = []rbacv1.PolicyRule{
		{
			APIGroups: []string{""},
			Resources: []string{"pods"},
			Verbs: []string{
				"get", "watch", "list",
			},
		},
	}
}

func (d *pmemCSIDeployment) getWebhooksRoleBinding(rb *rbacv1.RoleBinding) {
	rb.Subjects = []rbacv1.Subject{
		{
			Kind:      "ServiceAccount",
			Name:      d.WebhooksServiceAccountName(),
			Namespace: d.namespace,
		},
	}
	rb.RoleRef = rbacv1.RoleRef{
		APIGroup: "rbac.authorization.k8s.io",
		Kind:     "Role",
		Name:     d.WebhooksRoleName(),
	}
}

func (d *pmemCSIDeployment) getWebhooksClusterRole(cr *rbacv1.ClusterRole) {
	cr.Rules = []rbacv1.PolicyRule{
		{
			APIGroups: []string{""},
			Resources: []string{"persistentvolumes", "nodes"},
			Verbs: []string{
				"get", "list", "watch",
			},
		},
		{
			APIGroups: []string{""},
			Resources: []string{"persistentvolumeclaims"},
			Verbs: []string{
				"get", "list", "watch", "patch", "update",
			},
		},
		{
			APIGroups: []string{""},
			Resources: []string{"events"},
			Verbs: []string{
				"get", "list", "watch", "patch", "update", "create",
			},
		},
		{
			APIGroups: []string{"storage.k8s.io"},
			Resources: []string{"storageclasses", "csinodes"},
			Verbs: []string{
				"get", "list", "watch",
			},
		},
	}
}

func (d *pmemCSIDeployment) getWebhooksClusterRoleBinding(crb *rbacv1.ClusterRoleBinding) {
	crb.Subjects = []rbacv1.Subject{
		{
			Kind:      "ServiceAccount",
			Name:      d.WebhooksServiceAccountName(),
			Namespace: d.namespace,
		},
	}
	crb.RoleRef = rbacv1.RoleRef{
		APIGroup: "rbac.authorization.k8s.io",
		Kind:     "ClusterRole",
		Name:     d.WebhooksClusterRoleName(),
	}
}

func (d *pmemCSIDeployment) getMutatingWebhookConfig(hook *admissionregistrationv1.MutatingWebhookConfiguration) {
	selector := &metav1.LabelSelector{
		MatchExpressions: []metav1.LabelSelectorRequirement{
			{
				Key:      "pmem-csi.intel.com/webhook",
				Operator: metav1.LabelSelectorOpNotIn,
				Values:   []string{"ignore"},
			},
		},
	}
	failurePolicy := admissionregistrationv1.Ignore
	if d.Spec.MutatePods == api.MutatePodsAlways {
		failurePolicy = admissionregistrationv1.Fail
	}
	path := "/pod/mutate"
	none := admissionregistrationv1.SideEffectClassNone
	if len(d.controllerCABundle) == 0 {
		panic("controller CA bundle empty, should have been loaded")
	}
	hook.Webhooks = []admissionregistrationv1.MutatingWebhook{
		{
			// Name must be "fully-qualified" (i.e. with domain) but not unique, so
			// here "pmem-csi.intel.com" is not the default driver name.
			// https://pkg.go.dev/k8s.io/api/admissionregistration/v1#MutatingWebhook
			Name:              "pod-hook.pmem-csi.intel.com",
			NamespaceSelector: selector,
			ObjectSelector:    selector,
			FailurePolicy:     &failurePolicy,
			ClientConfig: admissionregistrationv1.WebhookClientConfig{
				Service: &admissionregistrationv1.ServiceReference{
					Name:      d.SchedulerServiceName(),
					Namespace: d.namespace,
					Path:      &path,
				},
				CABundle: d.controllerCABundle, // loaded earlier in reconcile()
			},
			Rules: []admissionregistrationv1.RuleWithOperations{
				{
					Operations: []admissionregistrationv1.OperationType{admissionregistrationv1.Create},
					Rule: admissionregistrationv1.Rule{
						APIGroups:   []string{""},
						APIVersions: []string{"v1"},
						Resources:   []string{"pods"},
					},
				},
			},
			SideEffects:             &none,
			AdmissionReviewVersions: []string{"v1"},
		},
	}
}

func (d *pmemCSIDeployment) getSchedulerService(service *corev1.Service) {
	d.getService(service, corev1.ServiceTypeClusterIP, 443)
	service.Spec.Ports[0].TargetPort.IntVal = schedulerPort
	service.Spec.Ports[0].NodePort = d.Spec.SchedulerNodePort
	if d.Spec.SchedulerNodePort != 0 {
		service.Spec.Type = corev1.ServiceTypeNodePort
	}
}

func (d *pmemCSIDeployment) getControllerProvisionerRole(role *rbacv1.Role) {
	role.Rules = []rbacv1.PolicyRule{
		{
			APIGroups: []string{""},
			Resources: []string{"endpoints"},
			Verbs: []string{
				"get", "watch", "list", "delete", "update", "create",
			},
		},
		{
			APIGroups: []string{"coordination.k8s.io"},
			Resources: []string{"leases"},
			Verbs: []string{
				"get", "watch", "list", "delete", "update", "create",
			},
		},
		// As in the upstream external-provisioner 2.0.0 rbac.yaml,
		// permissions for CSIStorageCapacity support get enabled unconditionally,
		// just in case.
		{
			APIGroups: []string{"storage.k8s.io"},
			Resources: []string{"csistoragecapacities"},
			Verbs: []string{
				"get", "list", "watch", "create", "update", "patch", "delete",
			},
		},
		{
			APIGroups: []string{""},
			Resources: []string{"pods"},
			Verbs: []string{
				"get",
			},
		},
		{
			APIGroups: []string{"apps"},
			Resources: []string{"replicasets"},
			Verbs: []string{
				"get",
			},
		},
	}
}

func (d *pmemCSIDeployment) getControllerProvisionerRoleBinding(rb *rbacv1.RoleBinding) {
	rb.Subjects = []rbacv1.Subject{
		{
			Kind:      "ServiceAccount",
			Name:      d.ProvisionerServiceAccountName(),
			Namespace: d.namespace,
		},
	}
	rb.RoleRef = rbacv1.RoleRef{
		APIGroup: "rbac.authorization.k8s.io",
		Kind:     "Role",
		Name:     d.ProvisionerRoleName(),
	}
}

func (d *pmemCSIDeployment) getControllerProvisionerClusterRole(cr *rbacv1.ClusterRole) {
	cr.Rules = []rbacv1.PolicyRule{
		{
			APIGroups: []string{""},
			Resources: []string{"persistentvolumes"},
			Verbs: []string{
				"get", "list", "watch", "create", "delete",
			},
		},
		{
			APIGroups: []string{""},
			Resources: []string{"persistentvolumeclaims"},
			Verbs: []string{
				"get", "list", "watch", "update",
			},
		},
		{
			APIGroups: []string{"storage.k8s.io"},
			Resources: []string{"storageclasses"},
			Verbs: []string{
				"get", "list", "watch",
			},
		},
		{
			APIGroups: []string{""},
			Resources: []string{"events"},
			Verbs: []string{
				"list", "watch", "create", "update", "patch",
			},
		},
		{
			APIGroups: []string{"snapshot.storage.k8s.io"},
			Resources: []string{"volumesnapshots"},
			Verbs: []string{
				"get", "list",
			},
		},
		{
			APIGroups: []string{"snapshot.storage.k8s.io"},
			Resources: []string{"volumesnapshotcontents"},
			Verbs: []string{
				"get", "list",
			},
		},
		{
			APIGroups: []string{"storage.k8s.io"},
			Resources: []string{"csinodes"},
			Verbs: []string{
				"get", "list", "watch",
			},
		},
		{
			APIGroups: []string{""},
			Resources: []string{"nodes"},
			Verbs: []string{
				"get", "list", "watch",
			},
		},
	}
}

func (d *pmemCSIDeployment) getControllerProvisionerClusterRoleBinding(crb *rbacv1.ClusterRoleBinding) {
	crb.Subjects = []rbacv1.Subject{
		{
			Kind:      "ServiceAccount",
			Name:      d.ProvisionerServiceAccountName(),
			Namespace: d.namespace,
		},
	}
	crb.RoleRef = rbacv1.RoleRef{
		APIGroup: "rbac.authorization.k8s.io",
		Kind:     "ClusterRole",
		Name:     d.ProvisionerClusterRoleName(),
	}
}

func (d *pmemCSIDeployment) getControllerStatefulSet(ss *appsv1.StatefulSet) {
	replicas := int32(1)
	true := true
	pmemcsiUser := int64(1000)

	if ss.Labels == nil {
		ss.Labels = map[string]string{}
	}
	ss.Labels["app.kubernetes.io/name"] = "pmem-csi-controller"
	ss.Labels["app.kubernetes.io/part-of"] = "pmem-csi"
	ss.Labels["app.kubernetes.io/component"] = "controller"
	ss.Labels["app.kubernetes.io/instance"] = d.Name

	ss.Spec = appsv1.StatefulSetSpec{
		Replicas: &replicas,
		Selector: &metav1.LabelSelector{
			MatchLabels: map[string]string{
				"app.kubernetes.io/name":     "pmem-csi-controller",
				"app.kubernetes.io/instance": d.Name,
			},
		},
		ServiceName: d.ControllerServiceName(),
		Template: corev1.PodTemplateSpec{
			ObjectMeta: metav1.ObjectMeta{
				Labels: joinMaps(
					d.Spec.Labels,
					map[string]string{
						"app.kubernetes.io/name":      "pmem-csi-controller",
						"app.kubernetes.io/part-of":   "pmem-csi",
						"app.kubernetes.io/component": "controller",
						"app.kubernetes.io/instance":  d.Name,
						"pmem-csi.intel.com/webhook":  "ignore",
					}),
				Annotations: map[string]string{
					"pmem-csi.intel.com/scrape": "containers",
				},
			},
			Spec: corev1.PodSpec{
				SecurityContext: &corev1.PodSecurityContext{
					// Controller pod must run as non-root user
					RunAsNonRoot: &true,
					RunAsUser:    &pmemcsiUser,
				},
				ServiceAccountName: d.GetHyphenedName() + "-webhooks",
				Containers: []corev1.Container{
					d.getControllerContainer(),
				},
				Tolerations: []corev1.Toleration{
					{
						// Allow this pod to run on a master node.
						Key:    "node-role.kubernetes.io/master",
						Effect: "NoSchedule",
					},
				},
			},
		},
	}
	if d.Spec.ControllerTLSSecret != "" {
		ss.Spec.Template.Spec.Volumes = append(ss.Spec.Template.Spec.Volumes,
			corev1.Volume{
				Name: "webhook-cert",
				VolumeSource: corev1.VolumeSource{
					Secret: &corev1.SecretVolumeSource{
						SecretName: d.Spec.ControllerTLSSecret,
					},
				},
			})
	}
}

func (d *pmemCSIDeployment) getNodeDaemonSet(ds *appsv1.DaemonSet) {
	directoryOrCreate := corev1.HostPathDirectoryOrCreate

	if ds.Labels == nil {
		ds.Labels = map[string]string{}
	}
	ds.Labels["app.kubernetes.io/name"] = "pmem-csi-node"
	ds.Labels["app.kubernetes.io/part-of"] = "pmem-csi"
	ds.Labels["app.kubernetes.io/component"] = "node"
	ds.Labels["app.kubernetes.io/instance"] = d.Name

	ds.Spec = appsv1.DaemonSetSpec{
		Selector: &metav1.LabelSelector{
			MatchLabels: map[string]string{
				"app.kubernetes.io/name":     "pmem-csi-node",
				"app.kubernetes.io/instance": d.Name,
			},
		},
		Template: corev1.PodTemplateSpec{
			ObjectMeta: metav1.ObjectMeta{
				Labels: joinMaps(
					d.Spec.Labels,
					map[string]string{
						"app.kubernetes.io/name":      "pmem-csi-node",
						"app.kubernetes.io/part-of":   "pmem-csi",
						"app.kubernetes.io/component": "node",
						"app.kubernetes.io/instance":  d.Name,
						"pmem-csi.intel.com/webhook":  "ignore",
					}),
				Annotations: map[string]string{
					"pmem-csi.intel.com/scrape": "containers",
				},
			},
			Spec: corev1.PodSpec{
				ServiceAccountName: d.ProvisionerServiceAccountName(),
				Tolerations: []corev1.Toleration{
					{
						// Allow this pod to run on a master node.
						Key:    "node-role.kubernetes.io/master",
						Effect: "NoSchedule",
					},
				},
				NodeSelector: d.Spec.NodeSelector,
				Containers: []corev1.Container{
					d.getNodeDriverContainer(),
					d.getNodeRegistrarContainer(),
					d.getProvisionerContainer(),
				},
				Volumes: []corev1.Volume{
					{
						Name: "socket-dir",
						VolumeSource: corev1.VolumeSource{
							HostPath: &corev1.HostPathVolumeSource{
								Path: d.Spec.KubeletDir + "/plugins/" + d.GetName(),
								Type: &directoryOrCreate,
							},
						},
					},
					{
						Name: "registration-dir",
						VolumeSource: corev1.VolumeSource{
							HostPath: &corev1.HostPathVolumeSource{
								Path: d.Spec.KubeletDir + "/plugins_registry/",
								Type: &directoryOrCreate,
							},
						},
					},
					{
						Name: "mountpoint-dir",
						VolumeSource: corev1.VolumeSource{
							HostPath: &corev1.HostPathVolumeSource{
								Path: d.Spec.KubeletDir + "/plugins/kubernetes.io/csi",
								Type: &directoryOrCreate,
							},
						},
					},
					{
						Name: "pods-dir",
						VolumeSource: corev1.VolumeSource{
							HostPath: &corev1.HostPathVolumeSource{
								Path: d.Spec.KubeletDir + "/pods",
								Type: &directoryOrCreate,
							},
						},
					},
					{
						Name: "pmem-state-dir",
						VolumeSource: corev1.VolumeSource{
							HostPath: &corev1.HostPathVolumeSource{
								Path: "/var/lib/" + d.GetName(),
								Type: &directoryOrCreate,
							},
						},
					},
					{
						Name: "dev-dir",
						VolumeSource: corev1.VolumeSource{
							HostPath: &corev1.HostPathVolumeSource{
								Path: "/dev",
								Type: &directoryOrCreate,
							},
						},
					},
					{
						Name: "sys-dir",
						VolumeSource: corev1.VolumeSource{
							HostPath: &corev1.HostPathVolumeSource{
								Path: "/sys",
								Type: &directoryOrCreate,
							},
						},
					},
				},
			},
		},
	}
}

func (d *pmemCSIDeployment) getControllerCommand() []string {
	nodeSelector := types.NodeSelector(d.Spec.NodeSelector)
	args := []string{
		"/usr/local/bin/pmem-csi-driver",
		fmt.Sprintf("-v=%d", d.Spec.LogLevel),
		"-logging-format=" + string(d.Spec.LogFormat),
		"-mode=webhooks",
		"-drivername=$(PMEM_CSI_DRIVER_NAME)",
		"-nodeSelector=" + nodeSelector.String(),
	}

	if d.Spec.ControllerTLSSecret != "" {
		args = append(args,
			"-caFile=/certs/ca.crt",
			"-certFile=/certs/tls.crt",
			"-keyFile=/certs/tls.key",
			fmt.Sprintf("-schedulerListen=:%d", schedulerPort),
		)
	}

	args = append(args, fmt.Sprintf("-metricsListen=:%d", controllerMetricsPort))

	return args
}

func (d *pmemCSIDeployment) getNodeDriverCommand() []string {
	return []string{
		"/usr/local/bin/pmem-csi-driver",
		fmt.Sprintf("-deviceManager=%s", d.Spec.DeviceMode),
		fmt.Sprintf("-v=%d", d.Spec.LogLevel),
		"-logging-format=" + string(d.Spec.LogFormat),
		"-mode=node",
		"-endpoint=unix:///csi/csi.sock",
		"-nodeid=$(KUBE_NODE_NAME)",
		"-statePath=/var/lib/$(PMEM_CSI_DRIVER_NAME)",
		"-drivername=$(PMEM_CSI_DRIVER_NAME)",
		fmt.Sprintf("-pmemPercentage=%d", d.Spec.PMEMPercentage),
		fmt.Sprintf("-metricsListen=:%d", nodeMetricsPort),
	}
}

func (d *pmemCSIDeployment) getControllerContainer() corev1.Container {
	true := true
	c := corev1.Container{
		Name:            "pmem-driver",
		Image:           d.Spec.Image,
		ImagePullPolicy: d.Spec.PullPolicy,
		Command:         d.getControllerCommand(),
		Env: []corev1.EnvVar{
			{
				Name:  "TERMINATION_LOG_PATH",
				Value: "/dev/termination-log",
			},
			{
				Name:  "PMEM_CSI_DRIVER_NAME",
				Value: d.GetName(),
			},
			{
				Name: "POD_NAMESPACE",
				ValueFrom: &corev1.EnvVarSource{
					FieldRef: &corev1.ObjectFieldSelector{
						APIVersion: "v1",
						FieldPath:  "metadata.namespace",
					},
				},
			},
		},
		Ports:                  d.getMetricsPorts(controllerMetricsPort),
		Resources:              *d.Spec.ControllerDriverResources,
		TerminationMessagePath: "/dev/termination-log",
		SecurityContext: &corev1.SecurityContext{
			ReadOnlyRootFilesystem: &true,
		},
	}

	if d.Spec.ControllerTLSSecret != "" {
		c.VolumeMounts = append(c.VolumeMounts,
			corev1.VolumeMount{
				Name:      "webhook-cert",
				MountPath: "/certs",
			})
	}

	return c
}

func (d *pmemCSIDeployment) getNodeDriverContainer() corev1.Container {
	bidirectional := corev1.MountPropagationBidirectional
	true := true
	root := int64(0)
	c := corev1.Container{
		Name:            "pmem-driver",
		Image:           d.Spec.Image,
		ImagePullPolicy: d.Spec.PullPolicy,
		Command:         d.getNodeDriverCommand(),
		Env: []corev1.EnvVar{
			{
				Name: "KUBE_NODE_NAME",
				ValueFrom: &corev1.EnvVarSource{
					FieldRef: &corev1.ObjectFieldSelector{
						APIVersion: "v1",
						FieldPath:  "spec.nodeName",
					},
				},
			},
			{
				Name:  "PMEM_CSI_DRIVER_NAME",
				Value: d.GetName(),
			},
			{
				Name:  "TERMINATION_LOG_PATH",
				Value: "/tmp/termination-log",
			},
		},
		VolumeMounts: []corev1.VolumeMount{
			{
				Name:             "mountpoint-dir",
				MountPath:        d.Spec.KubeletDir + "/plugins/kubernetes.io/csi",
				MountPropagation: &bidirectional,
			},
			{
				Name:             "pods-dir",
				MountPath:        d.Spec.KubeletDir + "/pods",
				MountPropagation: &bidirectional,
			},
			{
				Name:      "dev-dir",
				MountPath: "/dev",
			},
			{
				Name:      "sys-dir",
				MountPath: "/sys",
			},
			{
				Name:      "sys-dir",
				MountPath: "/host-sys",
			},
			{
				Name:      "socket-dir",
				MountPath: "/csi",
			},
			{
				Name:             "pmem-state-dir",
				MountPath:        "/var/lib/" + d.GetName(),
				MountPropagation: &bidirectional,
			},
		},
		Ports:     d.getMetricsPorts(nodeMetricsPort),
		Resources: *d.Spec.NodeDriverResources,
		SecurityContext: &corev1.SecurityContext{
			Privileged: &true,
			// Node driver must run as root user
			RunAsUser: &root,
		},
		TerminationMessagePath: "/tmp/termination-log",
	}

	return c
}

func (d *pmemCSIDeployment) getProvisionerContainer() corev1.Container {
	true := true
	return corev1.Container{
		Name:            "external-provisioner",
		Image:           d.Spec.ProvisionerImage,
		ImagePullPolicy: d.Spec.PullPolicy,
		Args: []string{
			fmt.Sprintf("-v=%d", d.Spec.LogLevel),
			"--csi-address=/csi/csi.sock",
			"--feature-gates=Topology=true",
			"--node-deployment=true",
			"--strict-topology=true",
			"--immediate-topology=false",
			// TODO (?): make this configurable?
			"--timeout=5m",
			"--default-fstype=ext4",
			"--worker-threads=5",
			fmt.Sprintf("--metrics-address=:%d", provisionerMetricsPort),
		},
		Env: []corev1.EnvVar{
			{
				Name: "NODE_NAME",
				ValueFrom: &corev1.EnvVarSource{
					FieldRef: &corev1.ObjectFieldSelector{
						APIVersion: "v1",
						FieldPath:  "spec.nodeName",
					},
				},
			},
		},
		VolumeMounts: []corev1.VolumeMount{
			{
				Name:      "socket-dir",
				MountPath: "/csi",
			},
		},
		Ports:     d.getMetricsPorts(provisionerMetricsPort),
		Resources: *d.Spec.ProvisionerResources,
		SecurityContext: &corev1.SecurityContext{
			ReadOnlyRootFilesystem: &true,
		},
	}
}

func (d *pmemCSIDeployment) getNodeRegistrarContainer() corev1.Container {
	true := true
	return corev1.Container{
		Name:            "driver-registrar",
		Image:           d.Spec.NodeRegistrarImage,
		ImagePullPolicy: d.Spec.PullPolicy,
		Args: []string{
			fmt.Sprintf("-v=%d", d.Spec.LogLevel),
			"--kubelet-registration-path=" + d.Spec.KubeletDir + "/plugins/$(PMEM_CSI_DRIVER_NAME)/csi.sock",
			"--csi-address=/csi/csi.sock",
		},
		SecurityContext: &corev1.SecurityContext{
			ReadOnlyRootFilesystem: &true,
		},
		VolumeMounts: []corev1.VolumeMount{
			{
				Name:      "socket-dir",
				MountPath: "/csi",
			},
			{
				Name:      "registration-dir",
				MountPath: "/registration",
			},
		},
		Env: []corev1.EnvVar{
			{
				Name:  "PMEM_CSI_DRIVER_NAME",
				Value: d.GetName(),
			},
		},
		Resources: *d.Spec.NodeRegistrarResources,
	}
}

func (d *pmemCSIDeployment) getNodeSetupClusterRole(cr *rbacv1.ClusterRole) {
	cr.Rules = []rbacv1.PolicyRule{
		{
			APIGroups: []string{""},
			Resources: []string{"nodes"},
			Verbs: []string{
				"patch",
			},
		},
	}
}

func (d *pmemCSIDeployment) getNodeSetupClusterRoleBinding(crb *rbacv1.ClusterRoleBinding) {
	crb.Subjects = []rbacv1.Subject{
		{
			Kind:      "ServiceAccount",
			Name:      d.NodeSetupServiceAccountName(),
			Namespace: d.namespace,
		},
	}
	crb.RoleRef = rbacv1.RoleRef{
		APIGroup: "rbac.authorization.k8s.io",
		Kind:     "ClusterRole",
		Name:     d.NodeSetupClusterRoleName(),
	}
}

func (d *pmemCSIDeployment) getNodeSetupDaemonSet(ds *appsv1.DaemonSet) {
	directoryOrCreate := corev1.HostPathDirectoryOrCreate

	if ds.Labels == nil {
		ds.Labels = map[string]string{}
	}
	ds.Labels["app.kubernetes.io/name"] = "pmem-csi-node-setup"
	ds.Labels["app.kubernetes.io/part-of"] = "pmem-csi"
	ds.Labels["app.kubernetes.io/component"] = "node-setup"
	ds.Labels["app.kubernetes.io/instance"] = d.Name

	spec := &ds.Spec
	spec.Selector = &metav1.LabelSelector{
		MatchLabels: map[string]string{
			"app.kubernetes.io/name":     "pmem-csi-node-setup",
			"app.kubernetes.io/instance": d.Name,
		},
	}
	spec.Template.ObjectMeta.Labels = joinMaps(
		d.Spec.Labels,
		map[string]string{
			"app.kubernetes.io/name":      "pmem-csi-node-setup",
			"app.kubernetes.io/part-of":   "pmem-csi",
			"app.kubernetes.io/component": "node-setup",
			"app.kubernetes.io/instance":  d.Name,
			"pmem-csi.intel.com/webhook":  "ignore",
		})
	podSpec := &ds.Spec.Template.Spec
	podSpec.ServiceAccountName = d.NodeSetupServiceAccountName()
	podSpec.Tolerations = []corev1.Toleration{
		{
			// Allow this pod to run on a master node.
			Key:    "node-role.kubernetes.io/master",
			Effect: "NoSchedule",
		},
	}
	podSpec.NodeSelector = map[string]string{
		d.Name + "/convert-raw-namespaces": "force",
	}
	podSpec.Containers = []corev1.Container{
		d.getNodeSetupContainer(),
	}
	podSpec.Volumes = []corev1.Volume{
		{
			Name: "dev-dir",
			VolumeSource: corev1.VolumeSource{
				HostPath: &corev1.HostPathVolumeSource{
					Path: "/dev",
					Type: &directoryOrCreate,
				},
			},
		},
		{
			Name: "sys-dir",
			VolumeSource: corev1.VolumeSource{
				HostPath: &corev1.HostPathVolumeSource{
					Path: "/sys",
					Type: &directoryOrCreate,
				},
			},
		},
	}
}

func (d *pmemCSIDeployment) getNodeSetupContainer() corev1.Container {
	true := true
	root := int64(0)
	c := corev1.Container{
		Name:            "pmem-driver",
		Image:           d.Spec.Image,
		ImagePullPolicy: d.Spec.PullPolicy,
		Command:         d.getNodeSetupCommand(),
		Env: []corev1.EnvVar{
			{
				Name: "KUBE_NODE_NAME",
				ValueFrom: &corev1.EnvVarSource{
					FieldRef: &corev1.ObjectFieldSelector{
						APIVersion: "v1",
						FieldPath:  "spec.nodeName",
					},
				},
			},
			{
				Name:  "TERMINATION_LOG_PATH",
				Value: "/tmp/termination-log",
			},
		},
		VolumeMounts: []corev1.VolumeMount{
			{
				Name:      "dev-dir",
				MountPath: "/dev",
			},
			{
				Name:      "sys-dir",
				MountPath: "/sys",
			},
			{
				Name:      "sys-dir",
				MountPath: "/host-sys",
			},
		},
		SecurityContext: &corev1.SecurityContext{
			Privileged: &true,
			// Node setup must run as root user
			RunAsUser: &root,
		},
		TerminationMessagePath:   "/tmp/termination-log",
		TerminationMessagePolicy: corev1.TerminationMessageReadFile,
	}

	return c
}

func (d *pmemCSIDeployment) getNodeSetupCommand() []string {
	nodeSelector := types.NodeSelector(d.Spec.NodeSelector)
	return []string{
		"/usr/local/bin/pmem-csi-driver",
		fmt.Sprintf("-v=%d", d.Spec.LogLevel),
		"-logging-format=" + string(d.Spec.LogFormat),
		"-mode=force-convert-raw-namespaces",
		"-nodeSelector=" + nodeSelector.String(),
		"-nodeid=$(KUBE_NODE_NAME)",
	}
}

func (d *pmemCSIDeployment) getMetricsPorts(port int32) []corev1.ContainerPort {
	return []corev1.ContainerPort{
		{
			Name:          "metrics",
			ContainerPort: port,
			Protocol:      "TCP",
		},
	}
}

func (d *pmemCSIDeployment) getObjectMeta(name string, isClusterResource bool) metav1.ObjectMeta {
	meta := metav1.ObjectMeta{
		Name: name,
		OwnerReferences: []metav1.OwnerReference{
			d.GetOwnerReference(),
		},
	}
	if !isClusterResource {
		meta.Namespace = d.namespace
	}
	return meta
}

func joinMaps(left, right map[string]string) map[string]string {
	result := map[string]string{}
	for key, value := range left {
		result[key] = value
	}
	for key, value := range right {
		result[key] = value
	}
	return result
}
