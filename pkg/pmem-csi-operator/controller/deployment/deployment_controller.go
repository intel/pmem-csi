/*
Copyright 2020 The Kubernetes Authors.

SPDX-License-Identifier: Apache-2.0
*/

package deployment

import (
	"context"
	"fmt"
	"os"
	"reflect"
	"time"

	pmemcsiv1alpha1 "github.com/intel/pmem-csi/pkg/apis/pmemcsi/v1alpha1"
	"github.com/intel/pmem-csi/pkg/k8sutil"
	pmemcontroller "github.com/intel/pmem-csi/pkg/pmem-csi-operator/controller"
	"github.com/intel/pmem-csi/pkg/version"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes"
	v1 "k8s.io/client-go/kubernetes/typed/core/v1"
	"k8s.io/client-go/tools/record"
	"k8s.io/klog/v2"
	"k8s.io/kubectl/pkg/scheme"
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
	r, err := NewReconcileDeployment(mgr.GetClient(), opts)
	if err != nil {
		return err
	}
	return add(mgr, r)
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

// ReconcileHook function to be invoked on reconciling a deployment.
type ReconcileHook *func(d *pmemcsiv1alpha1.Deployment)

// ReconcileDeployment reconciles a Deployment object
type ReconcileDeployment struct {
	client        client.Client
	evBroadcaster record.EventBroadcaster
	evRecorder    record.EventRecorder
	namespace     string
	k8sVersion    version.Version
	// container image used for deploying the operator
	containerImage string
	// known deployments
	deployments    map[string]*pmemcsiv1alpha1.Deployment
	reconcileHooks map[ReconcileHook]struct{}
}

// NewReconcileDeployment creates new deployment reconciler
func NewReconcileDeployment(client client.Client, opts pmemcontroller.ControllerOptions) (reconcile.Reconciler, error) {
	if opts.Namespace == "" {
		opts.Namespace = k8sutil.GetNamespace()
	}

	if opts.DriverImage == "" {
		// we can not use passed controller-runtime client as it's cache has
		// not yet been populated, and so it fails to fetch the pod details.
		// The cache only gets populated once after the Manager.Start().
		// So we depend on direct Kubernetes clientset.
		cs, err := kubernetes.NewForConfig(opts.Config)
		if err != nil {
			return nil, fmt.Errorf("failed to get in-cluster client: %v", err)
		}
		image, err := containerImage(cs, opts.Namespace)
		if err != nil {
			return nil, fmt.Errorf("failed to find the operator image: %v", err)
		}
		opts.DriverImage = image
	}
	klog.Infof("Using '%s' as default driver image.", opts.DriverImage)

	evBroadcaster := record.NewBroadcaster()
	evBroadcaster.StartRecordingToSink(&v1.EventSinkImpl{Interface: opts.EventsClient})
	evRecorder := evBroadcaster.NewRecorder(scheme.Scheme, corev1.EventSource{Component: "pmem-csi-operator"})

	return &ReconcileDeployment{
		client:         client,
		evBroadcaster:  evBroadcaster,
		evRecorder:     evRecorder,
		k8sVersion:     opts.K8sVersion,
		namespace:      opts.Namespace,
		containerImage: opts.DriverImage,
		deployments:    map[string]*pmemcsiv1alpha1.Deployment{},
		reconcileHooks: map[ReconcileHook]struct{}{},
	}, nil
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
			klog.Infof("Deployment '%s' deleted, removing local reference", request.Name)
			delete(r.deployments, request.Name)
			return reconcile.Result{}, nil
		}
		// Error reading the object - requeue the request.
		return reconcile.Result{Requeue: requeue, RequeueAfter: requeueDelayOnError}, err
	}

	for f := range r.reconcileHooks {
		if f != nil {
			(*f)(deployment)
		}
	}

	// If the deployment has already been marked for deletion,
	// then we don't need to do anything for it because the
	// apiserver is in the process of garbage-collecting all
	// sub-objects and then will remove it.
	if deployment.DeletionTimestamp != nil {
		return reconcile.Result{Requeue: false}, nil
	}

	patch := client.MergeFrom(deployment.DeepCopy())
	d := &PmemCSIDriver{deployment, r.namespace, r.k8sVersion}

	// update status
	defer func() {
		klog.Infof("Updating deployment status....")
		d.Deployment.Status.LastUpdated = metav1.Now()
		// Passing copy of CR to patch as the fake client used in tests
		// will write back the changes to both status and spec.
		copy := d.Deployment.DeepCopy()
		if statusErr := r.client.Status().Patch(context.TODO(), copy, patch); statusErr != nil {
			klog.Warningf("failed to update status %q for deployment %q: %v",
				d.Deployment.Status.Phase, d.Name, statusErr)
		}
		d.Deployment.Status = copy.Status
	}()

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

func (r *ReconcileDeployment) EventBroadcaster() record.EventBroadcaster {
	return r.evBroadcaster
}

// AddHook adds given reconcile hook to hooks list
func (r *ReconcileDeployment) AddHook(h ReconcileHook) {
	r.reconcileHooks[h] = struct{}{}
}

// RemoveHook removes previously added hook
func (r *ReconcileDeployment) RemoveHook(h ReconcileHook) {
	delete(r.reconcileHooks, h)
}

//Get tries to retrives the Kubernetes objects
func (r *ReconcileDeployment) Get(obj runtime.Object) error {
	key, err := client.ObjectKeyFromObject(obj)
	if err != nil {
		return fmt.Errorf("internal error %T: %v", obj, err)
	}
	return r.client.Get(context.TODO(), key, obj)
}

// Create create new Kubernetes object
func (r *ReconcileDeployment) Create(obj runtime.Object) error {
	metaObj, err := meta.Accessor(obj)
	if err != nil {
		return fmt.Errorf("internal error %T: %v", obj, err)
	}
	klog.Infof("Creating: '%s/%s' of type %T", metaObj.GetNamespace(), metaObj.GetName(), obj)
	return r.client.Create(context.TODO(), obj)
}

// Update updates existing Kubernetes object. The object must be a modified copy of the existing object in the apiserver.
func (r *ReconcileDeployment) Update(obj runtime.Object) error {
	metaObj, err := meta.Accessor(obj)
	if err != nil {
		return fmt.Errorf("internal error %T: %v", obj, err)
	}
	klog.Infof("Updating '%s/%s' of type '%T'", metaObj.GetNamespace(), metaObj.GetName(), obj)
	return r.client.Update(context.TODO(), obj)
}

// UpdateOrCreate updates the spec of an existing object or, if it does not exist yet, creates it.
func (r *ReconcileDeployment) UpdateOrCreate(obj runtime.Object) error {
	metaObj, err := meta.Accessor(obj)
	if err != nil {
		return fmt.Errorf("internal error %T: %v", obj, err)
	}
	existing := obj.DeepCopyObject()
	err = r.Get(existing)
	if err != nil && !errors.IsNotFound(err) {
		return err
	}
	if err == nil {
		metaExisting, err := meta.Accessor(existing)
		if err != nil {
			return fmt.Errorf("internal error %T: %v", existing, err)
		}

		ownerRef := metaObj.GetOwnerReferences()[0]
		if !isOwnedBy(metaExisting, &ownerRef) {
			return fmt.Errorf("'%s' of type %T is not owned by '%s'", metaObj.GetName(), obj, ownerRef.Name)
		}

		// Copy metadata from existing object
		metaObj.SetGenerateName(metaExisting.GetGenerateName())
		metaObj.SetSelfLink(metaExisting.GetSelfLink())
		metaObj.SetUID(metaExisting.GetUID())
		metaObj.SetResourceVersion(metaExisting.GetResourceVersion())
		metaObj.SetGeneration(metaExisting.GetGeneration())
		metaObj.SetCreationTimestamp(metaExisting.GetCreationTimestamp())
		metaObj.SetAnnotations(metaExisting.GetAnnotations())
		metaObj.SetFinalizers(metaExisting.GetFinalizers())
		metaObj.SetClusterName(metaExisting.GetClusterName())
		metaObj.SetManagedFields(metaExisting.GetManagedFields())
		metaObj.SetLabels(joinMaps(metaExisting.GetLabels(), metaObj.GetLabels()))

		klog.Infof("Updating '%s/%s' of type '%T'", metaObj.GetNamespace(), metaObj.GetName(), obj)
		return r.client.Update(context.TODO(), obj)
	}
	// Fall back to creating the object.
	klog.Infof("Creating '%s/%s' of type '%T'", metaObj.GetNamespace(), metaObj.GetName(), obj)
	return r.client.Create(context.TODO(), obj)
}

// Delete delete existing Kubernetes object
func (r *ReconcileDeployment) Delete(obj runtime.Object) error {
	metaObj, err := meta.Accessor(obj)
	if err != nil {
		return fmt.Errorf("internal error %T: %v", obj, err)
	}
	klog.Infof("Deleting '%s/%s' of type '%T'", metaObj.GetNamespace(), metaObj.GetName(), obj)
	return r.client.Delete(context.TODO(), obj)
}

// containerImage returns container image name used by operator Pod
func containerImage(cs *kubernetes.Clientset, namespace string) (string, error) {
	const podNameEnv = "POD_NAME"
	const operatorNameEnv = "OPERATOR_NAME"

	containerImage := ""
	// Operator deployment shall provide this environment
	if podName := os.Getenv(podNameEnv); podName != "" {
		pod, err := cs.CoreV1().Pods(namespace).Get(context.TODO(), podName, metav1.GetOptions{})
		if err != nil {
			return "", fmt.Errorf("error getting self pod '%s/%s': %v", namespace, podName, err)
		}
		// Operator container name shall be provided by deployment
		operatorName := os.Getenv(operatorNameEnv)
		if operatorName == "" {
			operatorName = "pmem-csi-operator"
		}
		for _, c := range pod.Spec.Containers {
			if c.Name == operatorName {
				containerImage = c.Image
				break
			}
		}
		if containerImage == "" {
			return "", fmt.Errorf("no container with name '%s' found. Set '%s' environment variable with the operator container name", operatorName, operatorNameEnv)
		}
	} else {
		return "", fmt.Errorf("'%s' environment variable not set", podNameEnv)
	}

	return containerImage, nil
}

// isOwnedBy checks if expectedOwner is in the object's owner references list
func isOwnedBy(object metav1.Object, expectedOwner *metav1.OwnerReference) bool {
	for _, owner := range object.GetOwnerReferences() {
		if reflect.DeepEqual(&owner, expectedOwner) {
			return true
		}
	}

	return false
}
