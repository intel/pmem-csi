/*
Copyright 2020 The Kubernetes Authors.

SPDX-License-Identifier: Apache-2.0
*/

package deployment

import (
	"context"
	"crypto/rsa"
	"crypto/tls"
	"fmt"
	"io/ioutil"
	"os"
	"path"
	"reflect"
	"runtime"

	api "github.com/intel/pmem-csi/pkg/apis/pmemcsi/v1beta1"
	grpcserver "github.com/intel/pmem-csi/pkg/grpc-server"
	"github.com/intel/pmem-csi/pkg/logger"
	pmemtls "github.com/intel/pmem-csi/pkg/pmem-csi-operator/pmem-tls"
	pmemgrpc "github.com/intel/pmem-csi/pkg/pmem-grpc"
	"github.com/intel/pmem-csi/pkg/version"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	storagev1beta1 "k8s.io/api/storage/v1beta1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	apiruntime "k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/kubectl/pkg/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

const (
	controllerServicePort  = 10000
	controllerMetricsPort  = 10010
	nodeControllerPort     = 10001
	nodeMetricsPort        = 10010
	provisionerMetricsPort = 10011
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
var currentObjects = []apiruntime.Object{
	&rbacv1.ClusterRole{TypeMeta: typeMeta(rbacv1.SchemeGroupVersion, "ClusterRole")},
	&rbacv1.ClusterRoleBinding{TypeMeta: typeMeta(rbacv1.SchemeGroupVersion, "ClusterRoleBinding")},
	&storagev1beta1.CSIDriver{TypeMeta: typeMeta(storagev1beta1.SchemeGroupVersion, "CSIDriver")},
	&appsv1.DaemonSet{TypeMeta: typeMeta(appsv1.SchemeGroupVersion, "DaemonSet")},
	&rbacv1.Role{TypeMeta: typeMeta(rbacv1.SchemeGroupVersion, "Role")},
	&rbacv1.RoleBinding{TypeMeta: typeMeta(rbacv1.SchemeGroupVersion, "RoleBinding")},
	&corev1.Secret{TypeMeta: typeMeta(corev1.SchemeGroupVersion, "Secret")},
	&corev1.Service{TypeMeta: typeMeta(corev1.SchemeGroupVersion, "Service")},
	&corev1.ServiceAccount{TypeMeta: typeMeta(corev1.SchemeGroupVersion, "ServiceAccount")},
	&appsv1.StatefulSet{TypeMeta: typeMeta(appsv1.SchemeGroupVersion, "StatefulSet")},
}

// A list of objects that may have been created by a previous release
// of the operator. This is relevant when updating from such an older
// release to the current one, because the current one must remove
// obsolete objects.
//
// The RBAC rules in deploy/kustomize/operator/operator.yaml must
// allow listing and removing of these objects.
var obsoleteObjects = []apiruntime.Object{
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
// deployment.
type pmemCSIDeployment struct {
	*api.Deployment
	// operator's namespace used for creating sub-resources
	namespace  string
	k8sVersion version.Version
}

// objectPatch combines a modified object and the patch against
// the current revision of that object that produces the modified
// object.
type objectPatch struct {
	obj   apiruntime.Object
	patch client.Patch
}

func newObjectPatch(obj, copy apiruntime.Object) *objectPatch {
	return &objectPatch{
		obj:   obj,
		patch: client.MergeFrom(copy),
	}
}

// IsNew checks if the object is a new object, i.e, the it is not
// yet stored with the APIServer.
func (op objectPatch) isNew() bool {
	if op.obj != nil {
		// We ignore only possible error - client.errNotObject
		// and treat it's as new object
		if o, err := meta.Accessor(op.obj); err == nil {
			// An object registered with API serve will have
			// a non-empty(zero) resource version
			return o.GetResourceVersion() == ""
		}
	}
	return false
}

// Diff returns the raw data representing the differences between
// original object and the changed/patch object.
func (op objectPatch) diff() ([]byte, error) {
	data, err := op.patch.Data(op.obj)
	if err != nil {
		return nil, err
	}

	// No changes
	if string(data) == "{}" {
		return nil, nil
	}
	return data, nil
}

// Apply sends the changes to API Server
// Creates new object if not existing, otherwise patches it with changes
func (op *objectPatch) apply(ctx context.Context, c client.Client, labels map[string]string) error {
	objMeta, err := meta.Accessor(op.obj)
	if err != nil {
		return fmt.Errorf("internal error %T: %v", op.obj, err)
	}
	l := logger.Get(ctx).WithName("objectPatch/apply")

	// NOTE(avalluri): Set labels just before creating/patching.
	// Setting them before creating the client.Patch makes
	// they get lost from the final diff.
	objMeta.SetLabels(labels)

	if op.isNew() {
		// For unknown reason client.Create() clearing off the
		// GVK on obj, So restore it manually.
		gvk := op.obj.GetObjectKind().GroupVersionKind()
		l.V(3).Info("create", logger.KObjWithType(objMeta))
		err := c.Create(ctx, op.obj)
		op.obj.GetObjectKind().SetGroupVersionKind(gvk)
		return err
	}
	l.V(3).Info("update", logger.KObjWithType(objMeta))
	data, err := op.diff()
	if err != nil {
		return err
	}
	// NOTE(avalluri): Fake client used in tests can not handle the
	// empty diff case, It treats every Patch() call as an update
	// and that results in change in objects's resourceVersion.
	if len(data) == 0 {
		return nil
	}

	return c.Patch(ctx, op.obj, op.patch)
}

// Reconcile reconciles the driver deployment. When adding new
// objects, extend also currentObjects above and the RBAC rules in
// deploy/kustomize/operator/operator.yaml.
func (d *pmemCSIDeployment) reconcile(ctx context.Context, r *ReconcileDeployment) error {

	if err := d.EnsureDefaults(r.containerImage); err != nil {
		return err
	}
	l := logger.Get(ctx).WithName("reconcile")

	l.V(3).Info("start", "deployment", d.Name, "phase", d.Status.Phase)
	var allObjects []apiruntime.Object
	redeployAll := func() error {
		var o apiruntime.Object
		var err error
		s, err := d.redeploySecrets(ctx, r)
		if err != nil {
			return err
		}
		for _, o := range s {
			allObjects = append(allObjects, o)
		}

		for name, handler := range subObjectHandlers {
			if o, err = d.redeploy(ctx, r, handler); err != nil {
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
func (d *pmemCSIDeployment) getSubObject(ctx context.Context, r *ReconcileDeployment, obj apiruntime.Object) error {
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

// updateSubObject writes the object changes to the API server.
func (d *pmemCSIDeployment) updateSubObject(ctx context.Context, r *ReconcileDeployment, op *objectPatch) error {
	return op.apply(ctx, r.client, d.Spec.Labels)
}

type redeployObject struct {
	objType    reflect.Type
	object     func(*pmemCSIDeployment) apiruntime.Object
	modify     func(*pmemCSIDeployment, apiruntime.Object) error
	postUpdate func(*pmemCSIDeployment, apiruntime.Object) error
}

// redeploy resets/patches the object returned by ro.object()
// with the updated data. The caller must set appropriate callabacks.
// Here are the redeploy steps:
//  1. Get the object by calling ro.object() which needs redeploying.
//  2. Retrieve the latest data saved at APIServer for that object.
//  3. Create an objectPatch for that object to record the changes from this point.
//  4. Call ro.modify() to modify the object's data.
//  5. Call objectPatch.Apply() to submit the chanages to the APIServer.
//  6. If the update in step-5 was success, then call the ro.postUpdate() callback
//     to run any post update steps.
func (d *pmemCSIDeployment) redeploy(ctx context.Context, r *ReconcileDeployment, ro redeployObject) (apiruntime.Object, error) {
	o := ro.object(d)
	if o == nil {
		return nil, fmt.Errorf("nil object")
	}
	if err := d.getSubObject(ctx, r, o); err != nil {
		return nil, err
	}
	op := newObjectPatch(o, o.DeepCopyObject())
	if err := ro.modify(d, o); err != nil {
		return nil, err
	}
	if err := d.updateSubObject(ctx, r, op); err != nil {
		return nil, err
	}
	if ro.postUpdate != nil {
		if err := ro.postUpdate(d, o); err != nil {
			return nil, err
		}
	}
	return o, nil
}

// redeploySecrets ensures that the secrets get (re)deployed that are
// required for running the driver.
//
// First it checks if the deployment is configured with the needed certificates.
// If provided, validate and (re)create secrets using them.
// Else, provision new certificates(only if no existing secrets found) and deploy.
//
// We cannot use d.redeploy() as secrets needs to be provisioned if not preset.
// This special case cannot be fit into generice redeploy logic.
func (d *pmemCSIDeployment) redeploySecrets(ctx context.Context, r *ReconcileDeployment) ([]*corev1.Secret, error) {
	rs := &corev1.Secret{
		TypeMeta:   metav1.TypeMeta{Kind: "Secret", APIVersion: "v1"},
		ObjectMeta: d.getObjectMeta(d.RegistrySecretName(), false),
	}
	if err := d.getSubObject(ctx, r, rs); err != nil {
		return nil, err
	}
	rop := newObjectPatch(rs, rs.DeepCopy())

	ns := &corev1.Secret{
		TypeMeta:   metav1.TypeMeta{Kind: "Secret", APIVersion: "v1"},
		ObjectMeta: d.getObjectMeta(d.NodeSecretName(), false),
	}
	if err := d.getSubObject(ctx, r, ns); err != nil {
		return nil, err
	}
	nop := newObjectPatch(ns, ns.DeepCopy())

	update := func() error {
		d.getRegistrySecrets(rs)
		if err := d.updateSubObject(ctx, r, rop); err != nil {
			return fmt.Errorf("failed to update registry secrets: %w", err)
		}

		d.getNodeSecrets(ns)
		if err := d.updateSubObject(ctx, r, nop); err != nil {
			return fmt.Errorf("failed to update node secrets: %w", err)
		}
		return nil
	}

	certsProvided, err := d.HaveCertificatesConfigured()
	if err != nil {
		return nil, err
	}

	updateSecrets := false
	if certsProvided {
		// Use  provided certificates
		if err := d.validateCertificates(); err != nil {
			d.SetCondition(api.CertsVerified, corev1.ConditionFalse, err.Error())
			return nil, err
		}
		d.SetCondition(api.CertsVerified, corev1.ConditionTrue, "Driver certificates validated.")
		updateSecrets = true
	} else if rop.isNew() || nop.isNew() {
		// Provision new self-signed certificates if not already present
		if err := d.provisionCertificates(ctx); err != nil {
			d.SetCondition(api.CertsReady, corev1.ConditionFalse, err.Error())
			return nil, err
		}
		updateSecrets = true
	}

	if updateSecrets {
		if err := update(); err != nil {
			return nil, err
		}
	}
	d.SetCondition(api.CertsReady, corev1.ConditionTrue, "Driver certificates are available.")

	return []*corev1.Secret{rs, ns}, nil
}

var subObjectHandlers = map[string]redeployObject{
	"node driver": {
		objType: reflect.TypeOf(&appsv1.DaemonSet{}),
		object: func(d *pmemCSIDeployment) apiruntime.Object {
			return &appsv1.DaemonSet{
				TypeMeta:   metav1.TypeMeta{Kind: "DaemonSet", APIVersion: "apps/v1"},
				ObjectMeta: d.getObjectMeta(d.NodeDriverName(), false),
			}
		},
		modify: func(d *pmemCSIDeployment, o apiruntime.Object) error {
			d.getNodeDaemonSet(o.(*appsv1.DaemonSet))
			return nil
		},
		postUpdate: func(d *pmemCSIDeployment, o apiruntime.Object) error {
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
		object: func(d *pmemCSIDeployment) apiruntime.Object {
			return &appsv1.StatefulSet{
				TypeMeta:   metav1.TypeMeta{Kind: "StatefulSet", APIVersion: "apps/v1"},
				ObjectMeta: d.getObjectMeta(d.ControllerDriverName(), false),
			}
		},
		modify: func(d *pmemCSIDeployment, o apiruntime.Object) error {
			d.getControllerStatefulSet(o.(*appsv1.StatefulSet))
			return nil
		},
		postUpdate: func(d *pmemCSIDeployment, o apiruntime.Object) error {
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
		object: func(d *pmemCSIDeployment) apiruntime.Object {
			return &corev1.Service{
				TypeMeta:   metav1.TypeMeta{Kind: "Service", APIVersion: "v1"},
				ObjectMeta: d.getObjectMeta(d.ControllerServiceName(), false),
			}
		},
		modify: func(d *pmemCSIDeployment, o apiruntime.Object) error {
			d.getControllerService(o.(*corev1.Service))
			return nil
		},
	},
	"metrics service": {
		objType: reflect.TypeOf(&corev1.Service{}),
		object: func(d *pmemCSIDeployment) apiruntime.Object {
			return &corev1.Service{
				TypeMeta:   metav1.TypeMeta{Kind: "Service", APIVersion: "v1"},
				ObjectMeta: d.getObjectMeta(d.MetricsServiceName(), false),
			}
		},
		modify: func(d *pmemCSIDeployment, o apiruntime.Object) error {
			d.getMetricsService(o.(*corev1.Service))
			return nil
		},
	},
	"CSIDriver": {
		objType: reflect.TypeOf(&storagev1beta1.CSIDriver{}),
		object: func(d *pmemCSIDeployment) apiruntime.Object {
			return &storagev1beta1.CSIDriver{
				TypeMeta:   metav1.TypeMeta{Kind: "CSIDriver", APIVersion: "storage.k8s.io/v1beta1"},
				ObjectMeta: d.getObjectMeta(d.CSIDriverName(), true),
			}
		},
		modify: func(d *pmemCSIDeployment, o apiruntime.Object) error {
			d.getCSIDriver(o.(*storagev1beta1.CSIDriver))
			return nil
		},
	},
	"provisioner role": {
		objType: reflect.TypeOf(&rbacv1.Role{}),
		object: func(d *pmemCSIDeployment) apiruntime.Object {
			return &rbacv1.Role{
				TypeMeta:   metav1.TypeMeta{Kind: "Role", APIVersion: "rbac.authorization.k8s.io/v1"},
				ObjectMeta: d.getObjectMeta(d.ProvisionerRoleName(), false),
			}
		},
		modify: func(d *pmemCSIDeployment, o apiruntime.Object) error {
			d.getControllerProvisionerRole(o.(*rbacv1.Role))
			return nil
		},
	},
	"provisioner role binding": {
		objType: reflect.TypeOf(&rbacv1.RoleBinding{}),
		object: func(d *pmemCSIDeployment) apiruntime.Object {
			return &rbacv1.RoleBinding{
				TypeMeta:   metav1.TypeMeta{Kind: "RoleBinding", APIVersion: "rbac.authorization.k8s.io/v1"},
				ObjectMeta: d.getObjectMeta(d.ProvisionerRoleBindingName(), false),
			}
		},
		modify: func(d *pmemCSIDeployment, o apiruntime.Object) error {
			d.getControllerProvisionerRoleBinding(o.(*rbacv1.RoleBinding))
			return nil
		},
	},
	"provisioner cluster role": {
		objType: reflect.TypeOf(&rbacv1.ClusterRole{}),
		object: func(d *pmemCSIDeployment) apiruntime.Object {
			return &rbacv1.ClusterRole{
				TypeMeta:   metav1.TypeMeta{Kind: "ClusterRole", APIVersion: "rbac.authorization.k8s.io/v1"},
				ObjectMeta: d.getObjectMeta(d.ProvisionerClusterRoleName(), true),
			}
		},
		modify: func(d *pmemCSIDeployment, o apiruntime.Object) error {
			d.getControllerProvisionerClusterRole(o.(*rbacv1.ClusterRole))
			return nil
		},
	},
	"provisioner cluster role binding": {
		objType: reflect.TypeOf(&rbacv1.ClusterRoleBinding{}),
		object: func(d *pmemCSIDeployment) apiruntime.Object {
			return &rbacv1.ClusterRoleBinding{
				TypeMeta:   metav1.TypeMeta{Kind: "ClusterRoleBinding", APIVersion: "rbac.authorization.k8s.io/v1"},
				ObjectMeta: d.getObjectMeta(d.ProvisionerClusterRoleBindingName(), true),
			}
		},
		modify: func(d *pmemCSIDeployment, o apiruntime.Object) error {
			d.getControllerProvisionerClusterRoleBinding(o.(*rbacv1.ClusterRoleBinding))
			return nil
		},
	},
	"service account": {
		objType: reflect.TypeOf(&corev1.ServiceAccount{}),
		object: func(d *pmemCSIDeployment) apiruntime.Object {
			return &corev1.ServiceAccount{
				TypeMeta:   metav1.TypeMeta{Kind: "ServiceAccount", APIVersion: "v1"},
				ObjectMeta: d.getObjectMeta(d.ServiceAccountName(), false),
			}
		},
		modify: func(d *pmemCSIDeployment, o apiruntime.Object) error {
			// nothing to customize for service account
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
		if err := r.patchDeploymentStatus(d.Deployment, client.MergeFrom(org)); err != nil {
			return fmt.Errorf("failed to update deployment CR status: %v", err)
		}
	}

	if objType == v1SecretPtr {
		l.V(3).Info("redeploying", "name", "driver secrets", "object", logger.KObjWithType(metaData))
		if _, err := d.redeploySecrets(ctx, r); err != nil {
			return fmt.Errorf("failed to redeploy %q secrets: %v", metaData.GetName(), err)
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

			o, err := scheme.Scheme.New(obj.GetObjectKind().GroupVersionKind())
			if err != nil {
				return err
			}
			metaObj, err := meta.Accessor(o)
			if err != nil {
				return err
			}
			metaObj.SetName(obj.GetName())
			metaObj.SetNamespace(obj.GetNamespace())

			if err := r.Delete(o); err != nil && !errors.IsNotFound(err) {
				return err
			}
		}
	}
	return nil
}

func (d *pmemCSIDeployment) getRegistrySecrets(secret *corev1.Secret) {
	d.getSecret(secret, "registry-secrets", d.Spec.CACert, d.Spec.RegistryPrivateKey, d.Spec.RegistryCert)
}

func (d *pmemCSIDeployment) getNodeSecrets(secret *corev1.Secret) {
	d.getSecret(secret, "node-secrets", d.Spec.CACert, d.Spec.NodeControllerPrivateKey, d.Spec.NodeControllerCert)
}

func (d *pmemCSIDeployment) provisionCertificates(ctx context.Context) error {
	l := logger.Get(ctx).WithName("provisionCertificates")
	var prKey *rsa.PrivateKey

	l.V(3).Info("provisioning new certificates")
	ca, err := pmemtls.NewCA(nil, nil)
	if err != nil {
		return fmt.Errorf("failed to initialize CA: %v", err)
	}
	d.Spec.CACert = ca.EncodedCertificate()

	if d.Spec.RegistryPrivateKey != nil {
		prKey, err = pmemtls.DecodeKey(d.Spec.RegistryPrivateKey)
	} else {
		prKey, err = pmemtls.NewPrivateKey()
		d.Spec.RegistryPrivateKey = pmemtls.EncodeKey(prKey)
	}
	if err != nil {
		return err
	}

	cert, err := ca.GenerateCertificate("pmem-registry", prKey.Public())
	if err != nil {
		return fmt.Errorf("failed to generate registry certificate: %v", err)
	}
	d.Spec.RegistryCert = pmemtls.EncodeCert(cert)

	if d.Spec.NodeControllerPrivateKey == nil {
		prKey, err = pmemtls.NewPrivateKey()
		d.Spec.NodeControllerPrivateKey = pmemtls.EncodeKey(prKey)
	} else {
		prKey, err = pmemtls.DecodeKey(d.Spec.NodeControllerPrivateKey)
	}
	if err != nil {
		return err
	}

	cert, err = ca.GenerateCertificate("pmem-node-controller", prKey.Public())
	if err != nil {
		return err
	}
	d.Spec.NodeControllerCert = pmemtls.EncodeCert(cert)

	// Instead of waiting for next GC cycle, initiate garbage collector manually
	// so that the unneeded CA key, certificate get removed.
	defer runtime.GC()

	return nil
}

// validateCertificates ensures that the given keys and certificates are valid
// to start PMEM-CSI driver by running a mutual-tls registry server and initiating
// a tls client connection to that sever using the provided keys and certificates.
// As we use mutual-tls, testing one server is enough to make sure that the provided
// certificates works
func (d *pmemCSIDeployment) validateCertificates() error {
	tmp, err := ioutil.TempDir("", "pmem-csi-validate-certs-*")
	if err != nil {
		return err
	}
	defer os.RemoveAll(tmp)

	// Registry server config
	regCfg, err := pmemgrpc.ServerTLS(d.Spec.CACert, d.Spec.RegistryCert, d.Spec.RegistryPrivateKey, "pmem-node-controller")
	if err != nil {
		return err
	}

	clientCfg, err := pmemgrpc.ClientTLS(d.Spec.CACert, d.Spec.NodeControllerCert, d.Spec.NodeControllerPrivateKey, "pmem-registry")
	if err != nil {
		return err
	}

	// start a registry server
	server := grpcserver.NewNonBlockingGRPCServer()
	path := path.Join(tmp, "socket")
	if err := server.Start("unix://"+path, regCfg, nil); err != nil {
		return fmt.Errorf("registry certificate: %w", err)
	}
	defer server.ForceStop()

	conn, err := tls.Dial("unix", path, clientCfg)
	if err != nil {
		return fmt.Errorf("node certificate: %w", err)
	}

	conn.Close()

	return nil
}

func (d *pmemCSIDeployment) getCSIDriver(csiDriver *storagev1beta1.CSIDriver) {
	attachRequired := false
	podInfoOnMount := true

	csiDriver.Spec = storagev1beta1.CSIDriverSpec{
		AttachRequired: &attachRequired,
		PodInfoOnMount: &podInfoOnMount,
	}

	// Volume lifecycle modes are supported only after k8s v1.16
	if d.k8sVersion.Compare(1, 16) >= 0 {
		csiDriver.Spec.VolumeLifecycleModes = []storagev1beta1.VolumeLifecycleMode{
			storagev1beta1.VolumeLifecyclePersistent,
			storagev1beta1.VolumeLifecycleEphemeral,
		}
	}
}

func (d *pmemCSIDeployment) getSecret(secret *corev1.Secret, cn string, ca, encodedKey, encodedCert []byte) {
	secret.Type = corev1.SecretTypeTLS
	secret.Data = map[string][]byte{
		// Same names as in the example secrets and in the v1 API.
		"ca.crt":  ca,          // no standard name for this one
		"tls.key": encodedKey,  // v1.TLSPrivateKeyKey
		"tls.crt": encodedCert, // v1.TLSCertKey
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
		"app": d.GetHyphenedName() + "-controller",
	}
}

func (d *pmemCSIDeployment) getControllerService(service *corev1.Service) {
	d.getService(service, corev1.ServiceTypeClusterIP, controllerServicePort)
}

func (d *pmemCSIDeployment) getMetricsService(service *corev1.Service) {
	d.getService(service, corev1.ServiceTypeNodePort, controllerMetricsPort)
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
			Name:      d.GetHyphenedName() + "-controller",
			Namespace: d.namespace,
		},
	}
	rb.RoleRef = rbacv1.RoleRef{
		APIGroup: "rbac.authorization.k8s.io",
		Kind:     "Role",
		Name:     d.GetHyphenedName() + "-external-provisioner-cfg",
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
			Name:      d.ServiceAccountName(),
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

	ss.Spec = appsv1.StatefulSetSpec{
		Replicas: &replicas,
		Selector: &metav1.LabelSelector{
			MatchLabels: map[string]string{
				"app": d.GetHyphenedName() + "-controller",
			},
		},
		ServiceName: d.GetHyphenedName() + "-controller",
		Template: corev1.PodTemplateSpec{
			ObjectMeta: metav1.ObjectMeta{
				Labels: joinMaps(
					d.Spec.Labels,
					map[string]string{
						"app":                        d.GetHyphenedName() + "-controller",
						"pmem-csi.intel.com/webhook": "ignore",
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
				ServiceAccountName: d.GetHyphenedName() + "-controller",
				Containers: []corev1.Container{
					d.getControllerContainer(),
					d.getProvisionerContainer(),
				},
				Affinity: &corev1.Affinity{
					NodeAffinity: &corev1.NodeAffinity{
						RequiredDuringSchedulingIgnoredDuringExecution: &corev1.NodeSelector{
							NodeSelectorTerms: []corev1.NodeSelectorTerm{
								{
									MatchExpressions: []corev1.NodeSelectorRequirement{
										// By default, the controller will run anywhere in the cluster.
										// If that isn't desired, the "pmem-csi.intel.com/controller" label
										// can be set to "no" or "false" for a node to prevent the controller
										// from running there.
										//
										// This is used during testing as a workaround for a particular issue
										// on Clear Linux where network configuration randomly fails such that
										// the driver which runs on the same node as the controller cannot
										// connect to the controller (https://github.com/intel/pmem-csi/issues/555).
										//
										// It may also be useful for other purposes, in particular for deployment
										// through the operator: it has the same rule and currently no other API for
										// setting affinity.
										{
											Key:      "pmem-csi.intel.com/controller",
											Operator: corev1.NodeSelectorOpNotIn,
											Values:   []string{"no", "false"},
										},
									},
								},
							},
						},
					},
				},
				Tolerations: []corev1.Toleration{
					{
						// Allow this pod to run on a master node.
						Key:    "node-role.kubernetes.io/master",
						Effect: "NoSchedule",
					},
				},
				Volumes: []corev1.Volume{
					{
						Name: "plugin-socket-dir",
						VolumeSource: corev1.VolumeSource{
							EmptyDir: &corev1.EmptyDirVolumeSource{},
						},
					},
					{
						Name: "registry-cert",
						VolumeSource: corev1.VolumeSource{
							Secret: &corev1.SecretVolumeSource{
								SecretName: d.GetHyphenedName() + "-registry-secrets",
							},
						},
					},
					{
						Name: "tmp-dir",
						VolumeSource: corev1.VolumeSource{
							EmptyDir: &corev1.EmptyDirVolumeSource{},
						},
					},
				},
			},
		},
	}
}

func (d *pmemCSIDeployment) getNodeDaemonSet(ds *appsv1.DaemonSet) {
	directoryOrCreate := corev1.HostPathDirectoryOrCreate
	ds.Spec = appsv1.DaemonSetSpec{
		Selector: &metav1.LabelSelector{
			MatchLabels: map[string]string{
				"app": d.GetHyphenedName() + "-node",
			},
		},
		Template: corev1.PodTemplateSpec{
			ObjectMeta: metav1.ObjectMeta{
				Labels: joinMaps(
					d.Spec.Labels,
					map[string]string{
						"app":                        d.GetHyphenedName() + "-node",
						"pmem-csi.intel.com/webhook": "ignore",
					}),
				Annotations: map[string]string{
					"pmem-csi.intel.com/scrape": "containers",
				},
			},
			Spec: corev1.PodSpec{
				NodeSelector: d.Spec.NodeSelector,
				Containers: []corev1.Container{
					d.getNodeDriverContainer(),
					d.getNodeRegistrarContainer(),
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
						Name: "node-cert",
						VolumeSource: corev1.VolumeSource{
							Secret: &corev1.SecretVolumeSource{
								SecretName: d.GetHyphenedName() + "-node-secrets",
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
	return []string{
		"/usr/local/bin/pmem-csi-driver",
		fmt.Sprintf("-v=%d", d.Spec.LogLevel),
		"-logging-format=" + string(d.Spec.LogFormat),
		"-mode=controller",
		"-endpoint=unix:///csi/csi-controller.sock",
		fmt.Sprintf("-registryEndpoint=tcp://0.0.0.0:%d", controllerServicePort),
		"-nodeid=$(KUBE_NODE_NAME)",
		"-caFile=/certs/ca.crt",
		"-certFile=/certs/tls.crt",
		"-keyFile=/certs/tls.key",
		"-drivername=$(PMEM_CSI_DRIVER_NAME)",
		fmt.Sprintf("-metricsListen=:%d", controllerMetricsPort),
	}
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
		fmt.Sprintf("-controllerEndpoint=tcp://$(KUBE_POD_IP):%d", nodeControllerPort),
		// User controller service name(== deployment name) as registry endpoint.
		fmt.Sprintf("-registryEndpoint=tcp://%s-controller:%d", d.GetHyphenedName(), controllerServicePort),
		"-caFile=/certs/ca.crt",
		"-certFile=/certs/tls.crt",
		"-keyFile=/certs/tls.key",
		"-statePath=/var/lib/$(PMEM_CSI_DRIVER_NAME)",
		"-drivername=$(PMEM_CSI_DRIVER_NAME)",
		fmt.Sprintf("-pmemPercentage=%d", d.Spec.PMEMPercentage),
		fmt.Sprintf("-metricsListen=:%d", nodeMetricsPort),
	}
}

func (d *pmemCSIDeployment) getControllerContainer() corev1.Container {
	true := true
	return corev1.Container{
		Name:            "pmem-driver",
		Image:           d.Spec.Image,
		ImagePullPolicy: d.Spec.PullPolicy,
		Command:         d.getControllerCommand(),
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
			{
				Name:  "PMEM_CSI_DRIVER_NAME",
				Value: d.GetName(),
			},
			{
				Name:  "GODEBUG",
				Value: "x509ignoreCN=0",
			},
		},
		VolumeMounts: []corev1.VolumeMount{
			{
				Name:      "registry-cert",
				MountPath: "/certs",
			},
			{
				Name:      "plugin-socket-dir",
				MountPath: "/csi",
			},
			{
				Name:      "tmp-dir",
				MountPath: "/tmp",
			},
		},
		Ports:                  d.getMetricsPorts(controllerMetricsPort),
		Resources:              *d.Spec.ControllerDriverResources,
		TerminationMessagePath: "/tmp/termination-log",
		SecurityContext: &corev1.SecurityContext{
			ReadOnlyRootFilesystem: &true,
		},
	}
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
				Name: "KUBE_POD_IP",
				ValueFrom: &corev1.EnvVarSource{
					FieldRef: &corev1.ObjectFieldSelector{
						APIVersion: "v1",
						FieldPath:  "status.podIP",
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
			{
				Name:  "GODEBUG",
				Value: "x509ignoreCN=0",
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
				Name:      "node-cert",
				MountPath: "/certs",
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
			"--csi-address=/csi/csi-controller.sock",
			"--feature-gates=Topology=true",
			"--strict-topology=true",
			"--timeout=5m",
			"--default-fstype=ext4",
			fmt.Sprintf("--metrics-address=:%d", provisionerMetricsPort),
		},
		VolumeMounts: []corev1.VolumeMount{
			{
				Name:      "plugin-socket-dir",
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
