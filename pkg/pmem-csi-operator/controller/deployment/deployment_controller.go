package deployment

import (
	"context"
	"time"

	pmemcsiv1alpha1 "github.com/intel/pmem-csi/pkg/apis/pmemcsi/v1alpha1"
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

// Add creates a new Deployment Controller and adds it to the Manager. The Manager will set fields on the Controller
// and Start it when the Manager is Started.
func Add(mgr manager.Manager) error {
	return add(mgr, NewReconcileDeployment(mgr.GetClient(), mgr.GetScheme()))
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
	// This client, initialized using mgr.Client() above, is a split client
	// that reads objects from the cache and writes to the apiserver
	client client.Client
	scheme *runtime.Scheme
	// known deployments
	deployments map[types.NamespacedName]*pmemcsiv1alpha1.Deployment
}

func NewReconcileDeployment(c client.Client, s *runtime.Scheme) reconcile.Reconciler {
	return &ReconcileDeployment{
		client:      c,
		scheme:      s,
		deployments: map[types.NamespacedName]*pmemcsiv1alpha1.Deployment{},
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
			for driverName, d := range r.deployments {
				if d.Name == request.Name && d.Namespace == request.Namespace {
					delete(r.deployments, driverName)
				}
			}
			return reconcile.Result{}, nil
		}
		// Error reading the object - requeue the request.
		return reconcile.Result{Requeue: requeue, RequeueAfter: requeueDelayOnError}, err
	}

	deployment.EnsureDefaults()
	d := &PmemCSIDriver{deployment}

	requeue, err = d.Reconcile(r)

	// update status
	defer func() {
		klog.Infof("Updating deployment status....")
		if statusErr := r.client.Status().Update(context.TODO(), d.Deployment); statusErr != nil {
			klog.Warningf("failed to update status %q for deployment %q: %v",
				d.Deployment.Status.Phase, d.Name, statusErr)
		}
	}()

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
		if err := r.client.Create(context.TODO(), obj); err != nil {
			return err
		}
	} else {
		return err
	}

	return nil
}

// Update updates existing Kubernetes object
func (r *ReconcileDeployment) Update(obj runtime.Object) error {
	return r.client.Update(context.TODO(), obj)
}

// Delete delete existing Kubernetes object
func (r *ReconcileDeployment) Delete(obj runtime.Object) error {
	return r.client.Delete(context.TODO(), obj)
}
