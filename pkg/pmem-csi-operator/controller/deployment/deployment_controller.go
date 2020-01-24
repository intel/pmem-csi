package deployment

import (
	"context"
	"fmt"
	"time"

	pmemcsiv1alpha1 "github.com/intel/pmem-csi/pkg/apis/pmemcsi/v1alpha1"
	certv1beta1 "k8s.io/api/certificates/v1beta1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	certclient "k8s.io/client-go/kubernetes/typed/certificates/v1beta1"
	"k8s.io/client-go/rest"
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
	return add(mgr, newReconciler(mgr))
}

// newReconciler returns a new reconcile.Reconciler
func newReconciler(mgr manager.Manager) reconcile.Reconciler {
	return &ReconcileDeployment{
		client: mgr.GetClient(),
		scheme: mgr.GetScheme(),
		config: mgr.GetConfig(),
	}
}

// add adds a new Controller to mgr with r as the reconcile.Reconciler
func add(mgr manager.Manager, r reconcile.Reconciler) error {
	// Create a new controller
	c, err := controller.New("deployment-controller", mgr, controller.Options{Reconciler: r})
	if err != nil {
		return err
	}

	// Watch for changes to primary resource Deployment
	err = c.Watch(&source.Kind{Type: &pmemcsiv1alpha1.Deployment{}}, &handler.EnqueueRequestForObject{})
	if err != nil {
		return err
	}

	// TODO(user): Modify this to be the types you create that are owned by the primary resource
	// Watch for changes to secondary resource Pods and requeue the owner Deployment
	err = c.Watch(&source.Kind{Type: &corev1.Pod{}}, &handler.EnqueueRequestForOwner{
		IsController: true,
		OwnerType:    &pmemcsiv1alpha1.Deployment{},
	})
	if err != nil {
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
	config *rest.Config
}

func newReconcileDeployment(c client.Client, s *runtime.Scheme, cfg *rest.Config) reconcile.Reconciler {
	return &ReconcileDeployment{
		client: c,
		scheme: s,
		config: cfg,
	}
}

// Reconcile reads that state of the cluster for a Deployment object and makes changes based on the state read
// and what is in the Deployment.Spec
// Note:
// The Controller will requeue the Request to be processed again if the returned error is non-nil or
// Result.Requeue is true, otherwise upon completion it will remove the work from the queue.
func (r *ReconcileDeployment) Reconcile(request reconcile.Request) (reconcile.Result, error) {
	// Fetch the Deployment instance
	deployment := &pmemcsiv1alpha1.Deployment{}
	err := r.client.Get(context.TODO(), request.NamespacedName, deployment)
	if err != nil {
		if errors.IsNotFound(err) {
			// Request object not found, could have been deleted after reconcile request.
			// Owned objects are automatically garbage collected. For additional cleanup logic use finalizers.
			// Return and don't requeue
			return reconcile.Result{}, nil
		}
		// Error reading the object - requeue the request.
		return reconcile.Result{}, err
	}

	deployment.EnsureDefaults()
	d := &PmemCSIDriver{deployment}

	requeue, err := d.Reconcile(r)
	if err == nil {
		// update status
		if statusErr := r.client.Status().Update(context.TODO(), d.Deployment); statusErr != nil {
			err = fmt.Errorf("failed to update deployment status: %v", statusErr)
			requeue = true
		}
	}

	klog.Infof("Requeue: %t, error: %v", requeue, err)

	if !requeue {
		return reconcile.Result{}, nil
	}

	return reconcile.Result{Requeue: requeue, RequeueAfter: time.Minute}, err
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

func (r *ReconcileDeployment) ApproveCSR(csr *certv1beta1.CertificateSigningRequest) error {
	// Default client interface provided by controller-runtime
	// does not support certificates API, so we have to create
	// appropriate client object
	certClient, err := certclient.NewForConfig(r.config)
	if err != nil {
		return err
	}
	_, err = certClient.CertificateSigningRequests().UpdateApproval(csr)
	return err
}
