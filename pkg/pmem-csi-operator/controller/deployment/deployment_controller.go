/*
Copyright 2020 The Kubernetes Authors.

SPDX-License-Identifier: Apache-2.0
*/

package deployment

import (
	"context"
	"fmt"
	"time"

	pmemcsiv1alpha1 "github.com/intel/pmem-csi/pkg/apis/pmemcsi/v1alpha1"
	"github.com/intel/pmem-csi/pkg/k8sutil"
	pmemcontroller "github.com/intel/pmem-csi/pkg/pmem-csi-operator/controller"
	"github.com/intel/pmem-csi/pkg/pmem-csi-operator/version"
	appsv1 "k8s.io/api/apps/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/klog"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	"sigs.k8s.io/controller-runtime/pkg/source"
)

func init() {
	// AddToManagerFuncs is a list of functions to create controllers and add them to a manager.
	pmemcontroller.AddToManagerFuncs = append(pmemcontroller.AddToManagerFuncs, Add)
}

// Add creates a new Deployment Controller and adds it to the Manager. The Manager will set fields on the Controller
// and Start it when the Manager is Started.
func Add(mgr manager.Manager, opts pmemcontroller.ControllerOptions) error {
	return add(mgr, NewReconcileDeployment(mgr.GetClient(), opts))
}

// add adds a new Controller to mgr with r as the reconcile.Reconciler
func add(mgr manager.Manager, r reconcile.Reconciler) error {
	// Create a new controller
	c, err := controller.New("deployment-controller", mgr, controller.Options{Reconciler: r})
	if err != nil {
		klog.Errorf("Deployment.Add: error: %v", err)
		return err
	}

	// Watch for changes to primary resource Deployment
	err = c.Watch(&source.Kind{Type: &pmemcsiv1alpha1.Deployment{}}, &handler.EnqueueRequestForObject{})
	if err != nil {
		klog.Errorf("Deployment.Add: watch error: %v", err)
		return err
	}

	return nil
}

// blank assignment to verify that ReconcileDeployment implements reconcile.Reconciler
var _ reconcile.Reconciler = &ReconcileDeployment{}

// ReconcileDeployment reconciles a Deployment object
type ReconcileDeployment struct {
	client     client.Client
	namespace  string
	k8sVersion *version.Version
	// known deployments
	deployments map[string]*pmemcsiv1alpha1.Deployment
}

// NewReconcileDeployment creates new deployment reconciler
func NewReconcileDeployment(client client.Client, opts pmemcontroller.ControllerOptions) reconcile.Reconciler {
	if opts.K8sVersion == nil {
		if ver, err := k8sutil.GetKubernetesVersion(opts.Config); err != nil {
			klog.Warningf("Failed to get kubernetes version: %v", err)
		} else {
			opts.K8sVersion = ver
		}
	}
	if opts.Namespace == "" {
		opts.Namespace = k8sutil.GetNamespace()
	}

	return &ReconcileDeployment{
		client:      client,
		k8sVersion:  opts.K8sVersion,
		namespace:   opts.Namespace,
		deployments: map[string]*pmemcsiv1alpha1.Deployment{},
	}
}

// Reconcile reads that state of the cluster for a Deployment object and makes changes based on the state read
// and what is in the Deployment.Spec
// Note:
// The Controller will requeue the Request to be processed again if the returned error is non-nil or
// Result.Requeue is true, otherwise upon completion it will remove the work from the queue.
func (r *ReconcileDeployment) Reconcile(request reconcile.Request) (reconcile.Result, error) {
	var requeue bool
	var err error

	requeueDelay := 1 * time.Minute
	requeueDelayOnError := 2 * time.Minute

	// Fetch the Deployment instance
	deployment := &pmemcsiv1alpha1.Deployment{}
	err = r.client.Get(context.TODO(), request.NamespacedName, deployment)
	if err != nil {
		if errors.IsNotFound(err) {
			// Request object not found, could have been deleted after reconcile request.
			// Owned objects are automatically garbage collected.
			// Remove the reference from our records
			delete(r.deployments, request.Name)
			return reconcile.Result{}, nil
		}
		// Error reading the object - requeue the request.
		return reconcile.Result{Requeue: requeue, RequeueAfter: requeueDelayOnError}, err
	}

	d := &PmemCSIDriver{deployment, r.namespace}

	// update status
	defer func() {
		klog.Infof("Updating deployment status....")
		if statusErr := r.client.Status().Update(context.TODO(), d.Deployment); statusErr != nil {
			klog.Warningf("failed to update status %q for deployment %q: %v",
				d.Deployment.Status.Phase, d.Name, statusErr)
		}
	}()

	if err := deployment.EnsureDefaults(); err != nil {
		d.Deployment.Status.Phase = pmemcsiv1alpha1.DeploymentPhaseFailed
		return reconcile.Result{}, err
	}

	requeue, err = d.Reconcile(r)

	klog.Infof("Requeue: %t, error: %v", requeue, err)

	if !requeue {
		return reconcile.Result{}, err
	}

	delay := requeueDelay
	if err != nil {
		delay = requeueDelayOnError
	}

	return reconcile.Result{Requeue: requeue, RequeueAfter: delay}, err
}

func (r *ReconcileDeployment) Namespace() string {
	return r.namespace
}

//Get tries to retrives the Kubernetes objects
func (r *ReconcileDeployment) Get(obj runtime.Object) error {
	metaObj, err := meta.Accessor(obj)
	if err != nil {
		klog.Errorf("Failed to get object: %v", err)
		return err
	}
	key := types.NamespacedName{Name: metaObj.GetName(), Namespace: metaObj.GetNamespace()}

	return r.client.Get(context.TODO(), key, obj)
}

// Create create new Kubernetes object
func (r *ReconcileDeployment) Create(obj runtime.Object) error {
	err := r.Get(obj)
	if err == nil {
		// Already found an active object
		return nil
	}
	if errors.IsNotFound(err) {
		metaObj, _ := meta.Accessor(obj)
		klog.Infof("Creating: %q of type %q ", metaObj.GetName(), obj.GetObjectKind().GroupVersionKind())
		if err := r.client.Create(context.TODO(), obj); err != nil {
			return err
		}
	} else {
		return err
	}

	return nil
}

// Update updates existing Kubernetes object. The object must be a modified copy of the existing object in the apiserver.
func (r *ReconcileDeployment) Update(obj runtime.Object) error {
	return r.client.Update(context.TODO(), obj)
}

// UpdateOrCreate updates the spec of an existing object or, if it does not exist yet, creates it.
func (r *ReconcileDeployment) UpdateOrCreate(obj runtime.Object) error {
	existing := obj.DeepCopyObject()
	err := r.Get(existing)
	if err != nil && !errors.IsNotFound(err) {
		return err
	}
	if err == nil {
		// Update spec of existing object.
		switch update := existing.(type) {
		case *appsv1.StatefulSet:
			update.Spec = obj.(*appsv1.StatefulSet).Spec
		case *appsv1.DaemonSet:
			update.Spec = obj.(*appsv1.DaemonSet).Spec
		default:
			return fmt.Errorf("internal error: updating %T not supported", obj)
		}
		return r.client.Update(context.TODO(), existing)
	}
	// Fall back to creating the object.
	return r.client.Create(context.TODO(), obj)
}

// Delete delete existing Kubernetes object
func (r *ReconcileDeployment) Delete(obj runtime.Object) error {
	return r.client.Delete(context.TODO(), obj)
}
