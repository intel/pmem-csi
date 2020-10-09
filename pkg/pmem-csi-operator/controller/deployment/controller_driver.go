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
	"runtime"

	api "github.com/intel/pmem-csi/pkg/apis/pmemcsi/v1alpha1"
	grpcserver "github.com/intel/pmem-csi/pkg/grpc-server"
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
	"k8s.io/klog/v2"
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

// A list of all object types potentially created by the operator,
// in this or any previous release. In other words, this list may grow,
// but never shrink, because a newer release needs to delete objects
// created by an older release.
// This list also must be kept in sync with the operator RBAC rules.
var AllObjectTypes = []schema.GroupVersionKind{
	rbacv1.SchemeGroupVersion.WithKind("RoleList"),
	rbacv1.SchemeGroupVersion.WithKind("ClusterRoleList"),
	rbacv1.SchemeGroupVersion.WithKind("RoleBindingList"),
	rbacv1.SchemeGroupVersion.WithKind("ClusterRoleBindingList"),
	corev1.SchemeGroupVersion.WithKind("ServiceAccountList"),
	corev1.SchemeGroupVersion.WithKind("SecretList"),
	corev1.SchemeGroupVersion.WithKind("ServiceList"),
	corev1.SchemeGroupVersion.WithKind("ConfigMapList"),
	appsv1.SchemeGroupVersion.WithKind("DaemonSetList"),
	appsv1.SchemeGroupVersion.WithKind("StatefulSetList"),
	storagev1beta1.SchemeGroupVersion.WithKind("CSIDriverList"),
}

type PmemCSIDriver struct {
	*api.Deployment
	// operators namespace used for creating sub-resources
	namespace  string
	k8sVersion version.Version
}

type ObjectPatch struct {
	obj   apiruntime.Object
	patch client.Patch
}

func NewObjectPatch(obj, copy apiruntime.Object) *ObjectPatch {
	return &ObjectPatch{
		obj:   obj,
		patch: client.MergeFrom(copy),
	}
}

// IsNew checks if the object is a new object, i.e, the it is not
// yet stored with the APIServer.
func (op ObjectPatch) IsNew() bool {
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
func (op ObjectPatch) Diff() ([]byte, error) {
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
func (op *ObjectPatch) Apply(c client.Client, labels map[string]string) error {
	objMeta, err := meta.Accessor(op.obj)
	if err != nil {
		return fmt.Errorf("internal error %T: %v", op.obj, err)
	}

	// NOTE(avalluri): Set labels just before creating/patching.
	// Setting them before creating the client.Patch makes
	// they get lost from the final diff.
	objMeta.SetLabels(labels)

	if op.IsNew() {
		// For unknown reason client.Create() clearing off the
		// GVK on obj, So restore it manually.
		gvk := op.obj.GetObjectKind().GroupVersionKind()
		klog.Infof("Create: %T/%s", op.obj, objMeta.GetName())
		err := c.Create(context.TODO(), op.obj)
		op.obj.GetObjectKind().SetGroupVersionKind(gvk)
		return err
	}
	klog.Infof("Update: %T/%s", op.obj, objMeta.GetName())
	data, err := op.Diff()
	if err != nil {
		return err
	}
	// NOTE(avalluri): Fake client used in tests can not handle the
	// empty diff case, It treats every Patch() call as an update
	// and that results in change in objects's resourceVersion.
	if len(data) == 0 {
		return nil
	}

	return c.Patch(context.TODO(), op.obj, op.patch)
}

// Reconcile reconciles the driver deployment
func (d *PmemCSIDriver) Reconcile(r *ReconcileDeployment) error {

	if err := d.EnsureDefaults(r.containerImage); err != nil {
		return err
	}

	klog.Infof("Deployment: %q, state %q ", d.Name, d.Status.Phase)
	var allObjects []apiruntime.Object
	redeployAll := func() error {
		var o apiruntime.Object
		var err error
		s, err := d.redeploySecrets(r)
		if err != nil {
			return err
		}
		for _, o := range s {
			allObjects = append(allObjects, o)
		}
		if o, err = d.redeployProvisionerRole(r); err != nil {
			return fmt.Errorf("failed to update RBAC role: %v", err)
		}
		allObjects = append(allObjects, o)
		if o, err = d.redeployProvisionerRoleBinding(r); err != nil {
			return fmt.Errorf("failed to update RBAC role bindings: %v", err)
		}
		allObjects = append(allObjects, o)
		if o, err = d.redeployProvisionerClusterRole(r); err != nil {
			return fmt.Errorf("failed to update RBAC cluster role: %v", err)
		}
		allObjects = append(allObjects, o)
		if o, err = d.redeployProvisionerClusterRoleBinding(r); err != nil {
			return fmt.Errorf("failed to update RBAC cluster role bindings: %v", err)
		}
		allObjects = append(allObjects, o)
		if o, err = d.redeployServiceAccount(r); err != nil {
			return fmt.Errorf("failed to update service account: %v", err)
		}
		allObjects = append(allObjects, o)
		if o, err = d.redeployControllerService(r); err != nil {
			return fmt.Errorf("failed to update controller service: %v", err)
		}
		allObjects = append(allObjects, o)
		if o, err = d.redeployMetricsService(r); err != nil {
			return fmt.Errorf("failed to update controller service: %v", err)
		}
		allObjects = append(allObjects, o)
		if o, err = d.redeployCSIDriver(r); err != nil {
			return fmt.Errorf("failed to update CSI driver: %v", err)
		}
		allObjects = append(allObjects, o)
		if o, err = d.redeployControllerDriver(r); err != nil {
			return fmt.Errorf("failed to update controller driver: %v", err)
		}
		allObjects = append(allObjects, o)
		if o, err = d.redeployNodeDriver(r); err != nil {
			return fmt.Errorf("failed to update node driver: %v", err)
		}
		allObjects = append(allObjects, o)
		return nil
	}

	if err := redeployAll(); err != nil {
		return err
	}

	klog.Infof("Deployed '%d' objects.", len(allObjects))
	// FIXME(avalluri): Limit the obsolete object deletion either only on version upgrades
	// or on operator restart.
	if err := d.deleteObsoleteObjects(r, allObjects); err != nil {
		return fmt.Errorf("Delete obsolete objects failed with error: %v", err)
	}

	return nil
}

// getSubObject retrieves the latest revision of given object type from the API server
// And checks if that object is owned by the current deployment CR
func (d *PmemCSIDriver) getSubObject(r *ReconcileDeployment, obj apiruntime.Object) error {
	objMeta, err := meta.Accessor(obj)
	if err != nil {
		return fmt.Errorf("internal error %T: %v", obj, err)
	}

	klog.Infof("Get: %T/%s", obj, objMeta.GetName())
	if err := r.Get(obj); err != nil {
		if errors.IsNotFound(err) {
			klog.Infof("Not found: %T/%s", obj, objMeta.GetName())
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
func (d *PmemCSIDriver) updateSubObject(r *ReconcileDeployment, op *ObjectPatch) error {
	return op.Apply(r.client, d.Spec.Labels)
}

type redeployObject struct {
	object     func() apiruntime.Object
	modify     func(apiruntime.Object) error
	postUpdate func(apiruntime.Object) error
}

// redeploy resets/patches the object returned by ro.object()
// with the updated data. The caller must set appropriate callabacks.
// Here are the redeploy steps:
//  1. Get the object by calling ro.object() which needs redeploying.
//  2. Retrieve the latest data saved at APIServer for that object.
//  3. Create an ObjectPatch for that object to record the changes from this point.
//  4. Call ro.modify() to modify the object's data.
//  5. Call ObjectPatch.Apply() to submit the chanages to the APIServer.
//  6. If the update in step-5 was success, then call the ro.postUpdate() callback
//     to run any post update steps.
func (d *PmemCSIDriver) redeploy(r *ReconcileDeployment, ro redeployObject) (apiruntime.Object, error) {
	o := ro.object()
	if o == nil {
		return nil, fmt.Errorf("nil object")
	}
	if err := d.getSubObject(r, o); err != nil {
		return nil, err
	}
	op := NewObjectPatch(o, o.DeepCopyObject())
	if err := ro.modify(o); err != nil {
		return nil, err
	}
	if err := d.updateSubObject(r, op); err != nil {
		return nil, err
	}
	if ro.postUpdate != nil {
		if err := ro.postUpdate(o); err != nil {
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
func (d *PmemCSIDriver) redeploySecrets(r *ReconcileDeployment) ([]*corev1.Secret, error) {
	rs := &corev1.Secret{
		TypeMeta:   metav1.TypeMeta{Kind: "Secret", APIVersion: "v1"},
		ObjectMeta: d.getObjectMeta(d.RegistrySecretName(), false),
	}
	if err := d.getSubObject(r, rs); err != nil {
		return nil, err
	}
	rop := NewObjectPatch(rs, rs.DeepCopy())

	ns := &corev1.Secret{
		TypeMeta:   metav1.TypeMeta{Kind: "Secret", APIVersion: "v1"},
		ObjectMeta: d.getObjectMeta(d.NodeSecretName(), false),
	}
	if err := d.getSubObject(r, ns); err != nil {
		return nil, err
	}
	nop := NewObjectPatch(ns, ns.DeepCopy())

	update := func() error {
		d.getRegistrySecrets(rs)
		if err := d.updateSubObject(r, rop); err != nil {
			return fmt.Errorf("failed to update registry secrets: %w", err)
		}

		d.getNodeSecrets(ns)
		if err := d.updateSubObject(r, nop); err != nil {
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
			return nil, err
		}
		d.SetCondition(api.CertsVerified, corev1.ConditionTrue, "Driver certificates validated.")
		updateSecrets = true
	} else if rop.IsNew() || nop.IsNew() {
		// Provision new self-signed certificates if not already present
		if err := d.provisionCertificates(); err != nil {
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

// redeployNodeDriver deploys the node daemon set and records its status on to CR status.
func (d *PmemCSIDriver) redeployNodeDriver(r *ReconcileDeployment) (apiruntime.Object, error) {
	return d.redeploy(r, redeployObject{
		object: func() apiruntime.Object {
			return &appsv1.DaemonSet{
				TypeMeta:   metav1.TypeMeta{Kind: "DaemonSet", APIVersion: "apps/v1"},
				ObjectMeta: d.getObjectMeta(d.NodeDriverName(), false),
			}
		},
		modify: func(o apiruntime.Object) error {
			d.getNodeDaemonSet(o.(*appsv1.DaemonSet))
			return nil
		},
		postUpdate: func(o apiruntime.Object) error {
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
	})
}

// redeployControllerDriver deploys the controller stateful set and records its status on to CR status.
func (d *PmemCSIDriver) redeployControllerDriver(r *ReconcileDeployment) (apiruntime.Object, error) {
	return d.redeploy(r, redeployObject{
		object: func() apiruntime.Object {
			return &appsv1.StatefulSet{
				TypeMeta:   metav1.TypeMeta{Kind: "StatefulSet", APIVersion: "apps/v1"},
				ObjectMeta: d.getObjectMeta(d.ControllerDriverName(), false),
			}
		},
		modify: func(o apiruntime.Object) error {
			d.getControllerStatefulSet(o.(*appsv1.StatefulSet))
			return nil
		},
		postUpdate: func(o apiruntime.Object) error {
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
	})
}

func (d *PmemCSIDriver) redeployControllerService(r *ReconcileDeployment) (apiruntime.Object, error) {
	return d.redeploy(r, redeployObject{
		object: func() apiruntime.Object {
			return &corev1.Service{
				TypeMeta:   metav1.TypeMeta{Kind: "Service", APIVersion: "v1"},
				ObjectMeta: d.getObjectMeta(d.ControllerServiceName(), false),
			}
		},
		modify: func(o apiruntime.Object) error {
			d.getControllerService(o.(*corev1.Service))
			return nil
		},
	})
}

func (d *PmemCSIDriver) redeployMetricsService(r *ReconcileDeployment) (apiruntime.Object, error) {
	return d.redeploy(r, redeployObject{
		object: func() apiruntime.Object {
			return &corev1.Service{
				TypeMeta:   metav1.TypeMeta{Kind: "Service", APIVersion: "v1"},
				ObjectMeta: d.getObjectMeta(d.MetricsServiceName(), false),
			}
		},
		modify: func(o apiruntime.Object) error {
			d.getMetricsService(o.(*corev1.Service))
			return nil
		},
	})
}

func (d *PmemCSIDriver) redeployCSIDriver(r *ReconcileDeployment) (apiruntime.Object, error) {
	return d.redeploy(r, redeployObject{
		object: func() apiruntime.Object {
			return &storagev1beta1.CSIDriver{
				TypeMeta:   metav1.TypeMeta{Kind: "CSIDriver", APIVersion: "storage.k8s.io/v1beta1"},
				ObjectMeta: d.getObjectMeta(d.CSIDriverName(), true),
			}
		},
		modify: func(o apiruntime.Object) error {
			d.getCSIDriver(o.(*storagev1beta1.CSIDriver))
			return nil
		},
	})
}

func (d *PmemCSIDriver) redeployProvisionerRole(r *ReconcileDeployment) (apiruntime.Object, error) {
	return d.redeploy(r, redeployObject{
		object: func() apiruntime.Object {
			return &rbacv1.Role{
				TypeMeta:   metav1.TypeMeta{Kind: "Role", APIVersion: "rbac.authorization.k8s.io/v1"},
				ObjectMeta: d.getObjectMeta(d.ProvisionerRoleName(), false),
			}
		},
		modify: func(o apiruntime.Object) error {
			d.getControllerProvisionerRole(o.(*rbacv1.Role))
			return nil
		},
	})
}

func (d *PmemCSIDriver) redeployProvisionerRoleBinding(r *ReconcileDeployment) (apiruntime.Object, error) {
	return d.redeploy(r, redeployObject{
		object: func() apiruntime.Object {
			return &rbacv1.RoleBinding{
				TypeMeta:   metav1.TypeMeta{Kind: "RoleBinding", APIVersion: "rbac.authorization.k8s.io/v1"},
				ObjectMeta: d.getObjectMeta(d.ProvisionerRoleBindingName(), false),
			}
		},
		modify: func(o apiruntime.Object) error {
			d.getControllerProvisionerRoleBinding(o.(*rbacv1.RoleBinding))
			return nil
		},
	})
}

func (d *PmemCSIDriver) redeployProvisionerClusterRole(r *ReconcileDeployment) (apiruntime.Object, error) {
	return d.redeploy(r, redeployObject{
		object: func() apiruntime.Object {
			return &rbacv1.ClusterRole{
				TypeMeta:   metav1.TypeMeta{Kind: "ClusterRole", APIVersion: "rbac.authorization.k8s.io/v1"},
				ObjectMeta: d.getObjectMeta(d.ProvisionerClusterRoleName(), true),
			}
		},
		modify: func(o apiruntime.Object) error {
			d.getControllerProvisionerClusterRole(o.(*rbacv1.ClusterRole))
			return nil
		},
	})
}

func (d *PmemCSIDriver) redeployProvisionerClusterRoleBinding(r *ReconcileDeployment) (apiruntime.Object, error) {
	return d.redeploy(r, redeployObject{
		object: func() apiruntime.Object {
			return &rbacv1.ClusterRoleBinding{
				TypeMeta:   metav1.TypeMeta{Kind: "ClusterRoleBinding", APIVersion: "rbac.authorization.k8s.io/v1"},
				ObjectMeta: d.getObjectMeta(d.ProvisionerClusterRoleBindingName(), true),
			}
		},
		modify: func(o apiruntime.Object) error {
			d.getControllerProvisionerClusterRoleBinding(o.(*rbacv1.ClusterRoleBinding))
			return nil
		},
	})
}

func (d *PmemCSIDriver) redeployServiceAccount(r *ReconcileDeployment) (apiruntime.Object, error) {
	return d.redeploy(r, redeployObject{
		object: func() apiruntime.Object {
			return &corev1.ServiceAccount{
				TypeMeta:   metav1.TypeMeta{Kind: "ServiceAccount", APIVersion: "v1"},
				ObjectMeta: d.getObjectMeta(d.ServiceAccountName(), false),
			}
		},
		modify: func(o apiruntime.Object) error {
			// nothing to customize for service account
			return nil
		},
	})
}

// HandleEvent handles the delete/update events received on sub-objects
func (d *PmemCSIDriver) HandleEvent(meta metav1.Object, obj apiruntime.Object, r *ReconcileDeployment) error {
	switch obj.(type) {
	case *corev1.Secret:
		klog.Infof("Redeploying driver secrets")
		if _, err := d.redeploySecrets(r); err != nil {
			return fmt.Errorf("failed to redeploy %q secrets: %v", meta.GetName(), err)
		}
	case *appsv1.DaemonSet:
		klog.Infof("Redeploying node driver")
		org := d.DeepCopy()
		if _, err := d.redeployNodeDriver(r); err != nil {
			return fmt.Errorf("failed to redeploy node driver: %v", err)
		}
		if err := r.PatchDeploymentStatus(d.Deployment, client.MergeFrom(org)); err != nil {
			return fmt.Errorf("failed to update deployment CR status: %v", err)
		}
	case *appsv1.StatefulSet:
		klog.Infof("Redeploying controller driver")
		org := d.DeepCopy()
		if _, err := d.redeployControllerDriver(r); err != nil {
			return fmt.Errorf("failed to redeploy controller driver: %v", err)
		}
		if err := r.PatchDeploymentStatus(d.Deployment, client.MergeFrom(org)); err != nil {
			return fmt.Errorf("failed to update deployment CR status: %v", err)
		}
	case *rbacv1.Role:
		klog.Infof("Redeploying provisioner RBAC role: %q", meta.GetName())
		if _, err := d.redeployProvisionerRole(r); err != nil {
			return fmt.Errorf("failed to redeploy %q provisioner role: %v", meta.GetName(), err)
		}
	case *rbacv1.ClusterRole:
		klog.Infof("Redeploying provisioner cluster role: %q", meta.GetName())
		if _, err := d.redeployProvisionerClusterRole(r); err != nil {
			return fmt.Errorf("failed to redeploy %q cluster role: %v", meta.GetName(), err)
		}
	case *rbacv1.RoleBinding:
		klog.Infof("Redeploying provisioner role binding")
		if _, err := d.redeployProvisionerRoleBinding(r); err != nil {
			return fmt.Errorf("failed to redeploy %q role binding: %v", meta.GetName(), err)
		}
	case *rbacv1.ClusterRoleBinding:
		klog.Infof("Redeploying provisioner cluster role binding: %q", meta.GetName())
		if _, err := d.redeployProvisionerClusterRoleBinding(r); err != nil {
			return fmt.Errorf("failed to redeploy %q cluster role binding: %v", meta.GetName(), err)
		}
	case *corev1.ServiceAccount:
		klog.Infof("Redeploying service account")
		if _, err := d.redeployServiceAccount(r); err != nil {
			return fmt.Errorf("failed to redeploy %q service account: %v", meta.GetName(), err)
		}
	case *corev1.Service:
		var err error
		klog.Infof("Redeploying service: %s", meta.GetName())
		if meta.GetName() == d.ControllerServiceName() {
			_, err = d.redeployControllerService(r)
		} else if meta.GetName() == d.MetricsServiceName() {
			_, err = d.redeployMetricsService(r)
		}
		if err != nil {
			return fmt.Errorf("failed to redeploy %q service: %v", meta.GetName(), err)
		}
	case *storagev1beta1.CSIDriver:
		klog.Infof("Redeploying %q CSIDriver", meta.GetName())
		if _, err := d.redeployCSIDriver(r); err != nil {
			return fmt.Errorf("failed to redeploy %q CSIDriver: %v", meta.GetName(), err)
		}
	default:
		klog.Infof("Ignoring event on '%s' of type %T", meta.GetName(), obj)
	}

	return nil
}

func objectIsObsolete(objList []apiruntime.Object, toFind unstructured.Unstructured) (bool, error) {
	klog.V(5).Infof("Checking if %q of type %q is obsolete...", toFind.GetName(), toFind.GetObjectKind().GroupVersionKind())
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

func (d *PmemCSIDriver) isOwnerOf(obj unstructured.Unstructured) bool {
	for _, owner := range obj.GetOwnerReferences() {
		if owner.UID == d.GetUID() {
			return true
		}
	}

	return false
}

func (d *PmemCSIDriver) deleteObsoleteObjects(r *ReconcileDeployment, newObjects []apiruntime.Object) error {
	for _, obj := range newObjects {
		metaObj, _ := meta.Accessor(obj)
		klog.V(5).Infof("==>%q type %q", metaObj.GetName(), obj.GetObjectKind().GroupVersionKind())
	}

	for _, gvk := range AllObjectTypes {
		list := &unstructured.UnstructuredList{}
		list.SetGroupVersionKind(gvk)
		opts := &client.ListOptions{
			Namespace: d.namespace,
		}

		klog.V(5).Infof("Fetching '%s' list with options: %v", gvk, opts.Namespace)
		if err := r.client.List(context.TODO(), list, opts); err != nil {
			return err
		}

		for _, obj := range list.Items {
			if !d.isOwnerOf(obj) {
				continue
			}
			obsolete, err := objectIsObsolete(newObjects, obj)
			if err != nil {
				return err
			}
			if !obsolete {
				continue
			}
			klog.Infof("Deleting %q of type '%s' because it is an obsolete object.", obj.GetName(), obj.GetObjectKind().GroupVersionKind())

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

func (d *PmemCSIDriver) getRegistrySecrets(secret *corev1.Secret) {
	d.getSecret(secret, "registry-secrets", d.Spec.CACert, d.Spec.RegistryPrivateKey, d.Spec.RegistryCert)
}

func (d *PmemCSIDriver) getNodeSecrets(secret *corev1.Secret) {
	d.getSecret(secret, "node-secrets", d.Spec.CACert, d.Spec.NodeControllerPrivateKey, d.Spec.NodeControllerCert)
}

func (d *PmemCSIDriver) provisionCertificates() error {
	var prKey *rsa.PrivateKey

	klog.Infof("Provisioning new certificates for deployment '%s'", d.Name)
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
func (d *PmemCSIDriver) validateCertificates() error {
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

func (d *PmemCSIDriver) getCSIDriver(csiDriver *storagev1beta1.CSIDriver) {
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

func (d *PmemCSIDriver) getSecret(secret *corev1.Secret, cn string, ca, encodedKey, encodedCert []byte) {
	secret.Type = corev1.SecretTypeTLS
	secret.Data = map[string][]byte{
		// Same names as in the example secrets and in the v1 API.
		"ca.crt":  ca,          // no standard name for this one
		"tls.key": encodedKey,  // v1.TLSPrivateKeyKey
		"tls.crt": encodedCert, // v1.TLSCertKey
	}
}

func (d *PmemCSIDriver) getService(service *corev1.Service, t corev1.ServiceType, port int32) {
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

func (d *PmemCSIDriver) getControllerService(service *corev1.Service) {
	d.getService(service, corev1.ServiceTypeClusterIP, controllerServicePort)
}

func (d *PmemCSIDriver) getMetricsService(service *corev1.Service) {
	d.getService(service, corev1.ServiceTypeNodePort, controllerMetricsPort)
}

func (d *PmemCSIDriver) getControllerProvisionerRole(role *rbacv1.Role) {
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

func (d *PmemCSIDriver) getControllerProvisionerRoleBinding(rb *rbacv1.RoleBinding) {
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

func (d *PmemCSIDriver) getControllerProvisionerClusterRole(cr *rbacv1.ClusterRole) {
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

func (d *PmemCSIDriver) getControllerProvisionerClusterRoleBinding(crb *rbacv1.ClusterRoleBinding) {
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

func (d *PmemCSIDriver) getControllerStatefulSet(ss *appsv1.StatefulSet) {
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

func (d *PmemCSIDriver) getNodeDaemonSet(ds *appsv1.DaemonSet) {
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

func (d *PmemCSIDriver) getControllerCommand() []string {
	return []string{
		"/usr/local/bin/pmem-csi-driver",
		fmt.Sprintf("-v=%d", d.Spec.LogLevel),
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

func (d *PmemCSIDriver) getNodeDriverCommand() []string {
	return []string{
		"/usr/local/bin/pmem-csi-driver",
		fmt.Sprintf("-deviceManager=%s", d.Spec.DeviceMode),
		fmt.Sprintf("-v=%d", d.Spec.LogLevel),
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

func (d *PmemCSIDriver) getControllerContainer() corev1.Container {
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
		Resources:              *d.Spec.ControllerResources,
		TerminationMessagePath: "/tmp/termination-log",
		SecurityContext: &corev1.SecurityContext{
			ReadOnlyRootFilesystem: &true,
		},
	}
}

func (d *PmemCSIDriver) getNodeDriverContainer() corev1.Container {
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
		Resources: *d.Spec.NodeResources,
		SecurityContext: &corev1.SecurityContext{
			Privileged: &true,
			// Node driver must run as root user
			RunAsUser: &root,
		},
		TerminationMessagePath: "/tmp/termination-log",
	}

	return c
}

func (d *PmemCSIDriver) getProvisionerContainer() corev1.Container {
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
		Resources: *d.Spec.ControllerResources,
		SecurityContext: &corev1.SecurityContext{
			ReadOnlyRootFilesystem: &true,
		},
	}
}

func (d *PmemCSIDriver) getNodeRegistrarContainer() corev1.Container {
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
		Resources: *d.Spec.NodeResources,
	}
}

func (d *PmemCSIDriver) getMetricsPorts(port int32) []corev1.ContainerPort {
	return []corev1.ContainerPort{
		{
			Name:          "metrics",
			ContainerPort: port,
			Protocol:      "TCP",
		},
	}
}

func (d *PmemCSIDriver) getObjectMeta(name string, isClusterResource bool) metav1.ObjectMeta {
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
