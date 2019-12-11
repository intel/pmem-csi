package deployment

import (
	"context"
	"fmt"

	pmemcsiv1alpha1 "github.com/intel/pmem-csi/operator/pkg/apis/pmemcsi/v1alpha1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/klog"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	"sigs.k8s.io/controller-runtime/pkg/source"
)

// Add creates a new Deployment Controller and adds it to the Manager. The Manager will set fields on the Controller
// and Start it when the Manager is Started.
func Add(mgr manager.Manager) error {
	return add(mgr, newReconcileDeployment(mgr.GetClient(), mgr.GetScheme()))
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
}

func newReconcileDeployment(c client.Client, s *runtime.Scheme) reconcile.Reconciler {
	return &ReconcileDeployment{
		client: c,
		scheme: s,
	}
}

// Reconcile reads that state of the cluster for a Deployment object and makes changes based on the state read
// and what is in the Deployment.Spec
// TODO(user): Modify this Reconcile function to implement your Controller logic.  This example creates
// a Pod as an example
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

	d := PmemCSIDriver{*deployment}

	objects := d.getDeploymentObjects()
	for _, obj := range objects {
		metaObj, err := meta.Accessor(obj)
		if err != nil {
			klog.Error("Failed to get meta object: %s", err, "(", obj, ")")
			return reconcile.Result{}, err
		}

		// Set Deployment instance as the owner and controller
		if metaObj.GetNamespace() != "" {
			if err := controllerutil.SetControllerReference(deployment, metaObj, r.scheme); err != nil {
				klog.Info("Failed to set controller reference:", err)
			}
		}

		klog.Info(fmt.Sprintf("Checking if object %q is present in namespace %q", metaObj.GetName(), metaObj.GetNamespace()))
		err = r.client.Get(context.TODO(), types.NamespacedName{Name: metaObj.GetName(), Namespace: metaObj.GetNamespace()}, obj)
		if err != nil {
			if errors.IsNotFound(err) {
				klog.Info("Creating a new Object(Namespace: ", metaObj.GetNamespace(), ", Name: ", metaObj.GetName())
				if err := r.client.Create(context.TODO(), obj); err != nil {
					klog.Error("Failed to create object:", err)
					return reconcile.Result{}, err
				}
				klog.Info("Object created")
			} else {
			}
		} else {
			klog.Info("Found object: ", metaObj.GetName())
		}

	}
	/* // Define a new Pod object
	pod := newPodForCR(instance)

	// Set Deployment instance as the owner and controller
	if err := controllerutil.SetControllerReference(instance, pod, r.scheme); err != nil {
		return reconcile.Result{}, err
	}

	// Check if this Pod already exists
	found := &corev1.Pod{}
	err = r.client.Get(context.TODO(), types.NamespacedName{Name: pod.Name, Namespace: pod.Namespace}, found)
	if err != nil && errors.IsNotFound(err) {
		klog.Info("Creating a new Pod(Namespace: ", pod.Namespace, ", Pod.Name: ", pod.Name, ")")
		err = r.client.Create(context.TODO(), pod)
		if err != nil {
			return reconcile.Result{}, err
		}

		// Pod created successfully - don't requeue
		return reconcile.Result{}, nil
	} else if err != nil {
		return reconcile.Result{}, err
	}

	// Pod already exists - don't requeue
	klog.Info("Skip reconcile: Pod already exists (Namespace: ", found.Namespace, ", Name: ", found.Name, ")")
	*/
	return reconcile.Result{}, nil
}
