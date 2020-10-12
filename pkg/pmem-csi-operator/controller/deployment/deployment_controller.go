/*
Copyright 2020 The Kubernetes Authors.

SPDX-License-Identifier: Apache-2.0
*/

package deployment

import (
	"context"
	"fmt"
	"os"
	"regexp"
	"sync"
	"time"

	pmemcsiv1alpha1 "github.com/intel/pmem-csi/pkg/apis/pmemcsi/v1alpha1"
	"github.com/intel/pmem-csi/pkg/k8sutil"
	pmemcontroller "github.com/intel/pmem-csi/pkg/pmem-csi-operator/controller"
	"github.com/intel/pmem-csi/pkg/version"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	storagev1beta1 "k8s.io/api/storage/v1beta1"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	apiruntime "k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes"
	v1 "k8s.io/client-go/kubernetes/typed/core/v1"
	"k8s.io/client-go/tools/record"
	"k8s.io/klog/v2"
	"k8s.io/kubectl/pkg/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
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
	return add(mgr, r.(*ReconcileDeployment))
}

// add adds a new Controller to mgr with r as the reconcile.Reconciler
func add(mgr manager.Manager, r *ReconcileDeployment) error {
	// Create a new controller
	c, err := controller.New("deployment-controller", mgr, controller.Options{Reconciler: r})
	if err != nil {
		klog.Errorf("Deployment.Add: error: %v", err)
		return err
	}

	p := predicate.Funcs{
		UpdateFunc: func(e event.UpdateEvent) bool {
			r.reconcileMutex.Lock()
			defer r.reconcileMutex.Unlock()
			klog.Infof("UPDATED: %T/%s, generation:%d", e.MetaOld, e.MetaOld.GetName(), e.MetaNew.GetGeneration())
			if e.MetaNew.GetDeletionTimestamp() != nil {
				// Deployment CR deleted, remove it's reference from cache.
				// Objects owned by it are automatically garbage collected.
				r.deleteDeployment(e.MetaOld.GetName())
				return false
			}
			if e.MetaOld.GetGeneration() == e.MetaNew.GetGeneration() {
				// No changes registered
				return false
			}

			op := NewObjectPatch(e.ObjectOld, e.ObjectNew)
			data, err := op.Diff()
			if err != nil {
				klog.Warningf("Failed to find deployment changes: %v", err)
				return true
			}
			klog.Infof("Diff: %v", string(data))
			// We are intersted in only spec changes, not CR status/metadata changes
			re := regexp.MustCompile(`{.*"spec":{(.*)}.*}`)
			res := re.FindSubmatch(data)
			if len(res) < 2 {
				klog.Infof("No spec changes observed, ignoring the event")
				return false
			}
			klog.Infof("CR changes: %v", string(res[1]))
			return true
		},
		DeleteFunc: func(e event.DeleteEvent) bool {
			r.reconcileMutex.Lock()
			defer r.reconcileMutex.Unlock()
			klog.Infof("DELETED: %T/%s", e.Object, e.Meta.GetName())
			// Deployment CR deleted, remove it's reference from cache.
			// Objects owned by it are automatically garbage collected.
			r.deleteDeployment(e.Meta.GetName())
			// We already handled the event here,
			// so no more further reconcile required.
			return false
		},
	}

	// Watch for changes to primary resource Deployment
	if err := c.Watch(&source.Kind{Type: &pmemcsiv1alpha1.Deployment{}}, &handler.EnqueueRequestForObject{}, p); err != nil {
		klog.Errorf("Deployment.Add: watch error: %v", err)
		return err
	}

	// Predicate functions for sub-object changes
	// We do not want to pollute deployment reconcile loop by sending
	// sub-object changes, instead we provide a dedicated handler.
	// So all these event handlers returns 'false' so that the event
	// is not propagated further.
	// One exception is: If we fail to handle here, then we pass this
	// event to reconcile loop, where it should recognize these requests
	// and just requeue. Expecting that the failure is retried.
	eventFunc := func(meta metav1.Object, obj apiruntime.Object) bool {
		// Get the owned deployment
		d, err := r.getDeploymentFor(meta)
		if err != nil {
			klog.Infof("%T/%s is not owned by any deployment", obj, meta.GetName())
			// The owner might have deleted already
			// we can safely ignore this event
			return false
		}
		r.reconcileMutex.Lock()
		defer r.reconcileMutex.Unlock()
		if err := d.HandleEvent(meta, obj, r); err != nil {
			klog.Warningf("Error occurred while handling the event: %v, requeuing the event", err)
			return true
		}
		return false
	}
	sop := predicate.Funcs{
		DeleteFunc: func(e event.DeleteEvent) bool {
			klog.Infof("DELETED: %T/%s", e.Object, e.Meta.GetName())
			return eventFunc(e.Meta, e.Object)
		},
		UpdateFunc: func(e event.UpdateEvent) bool {
			if e.MetaNew.GetDeletionTimestamp() != nil {
				// We can handle this in delete handler
				return false
			}
			klog.Infof("UPDATED: %T/%s", e.ObjectOld, e.MetaOld.GetName())
			return eventFunc(e.MetaOld, e.ObjectOld)
		},
		CreateFunc: func(e event.CreateEvent) bool {
			// Do not handle sub-object create events as the object was create by us.
			// Case of that object was not created by us and owned by a CR,
			// shouldn't happen in practice and checking whether it has happened
			// is just going to cause extra overhead in the normal case of objects
			// that were created by the operator"
			// So we intentionally ignore this event.
			return false
		},
	}

	// All possible object types for a Deployment CR
	// that would requires redeploying on change
	// NOTE: This list must be kept in sync with the objects
	// created by PmemCSIDriver.Reconcile() in controller_driver.go
	subresources := []runtime.Object{
		&rbacv1.ClusterRole{},
		&rbacv1.ClusterRoleBinding{},
		&storagev1beta1.CSIDriver{},
		&appsv1.DaemonSet{},
		&rbacv1.Role{},
		&rbacv1.RoleBinding{},
		&corev1.Secret{},
		&corev1.Service{},
		&corev1.ServiceAccount{},
		&appsv1.StatefulSet{},
	}

	for _, resource := range subresources {
		if err := c.Watch(&source.Kind{Type: resource}, &handler.EnqueueRequestForOwner{
			IsController: true,
			OwnerType:    &pmemcsiv1alpha1.Deployment{},
		}, sop); err != nil {
			klog.Errorf("Deployment.Add: watch error: %v", err)
			return err
		}
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
	deployments map[string]*pmemcsiv1alpha1.Deployment
	// deploymentsMutex protects concurrent access to deployments
	deploymentsMutex sync.Mutex
	// reconcileMutex synchronizes concurrent reconcile calls
	// initiated by change events, either deployment or sub-object
	// changes.
	reconcileMutex sync.Mutex
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
	r.reconcileMutex.Lock()
	defer r.reconcileMutex.Unlock()
	startTime := time.Now()

	requeueDelayOnError := 2 * time.Minute

	// Fetch the Deployment instance
	deployment := &pmemcsiv1alpha1.Deployment{}
	err = r.client.Get(context.TODO(), request.NamespacedName, deployment)
	if err != nil {
		klog.V(3).Infof("Failed to retrieve object '%s' to reconcile", request.Name)
		// One reason for this could be a failed predicate event handler of
		// sub-objects. So requeue the request so that the same predicate
		// handle could be called on that object.
		return reconcile.Result{Requeue: requeue, RequeueAfter: requeueDelayOnError}, err
	}

	klog.Infof("Reconciling deployment %q", deployment.GetName())

	// If the deployment has already been marked for deletion,
	// then we don't need to do anything for it because the
	// apiserver is in the process of garbage-collecting all
	// sub-objects and then will remove it.
	if deployment.DeletionTimestamp != nil {
		return reconcile.Result{Requeue: false}, nil
	}

	for f := range r.reconcileHooks {
		if f != nil {
			(*f)(deployment)
		}
	}

	if deployment.Status.Phase == pmemcsiv1alpha1.DeploymentPhaseNew {
		/* New deployment */
		r.evRecorder.Event(deployment, corev1.EventTypeNormal, pmemcsiv1alpha1.EventReasonNew, "Processing new driver deployment")
	}

	// Cache the deployment
	r.saveDeployment(deployment)

	dep := deployment.DeepCopy()

	// update status
	defer func() {
		klog.Infof("Updating deployment status...")
		// Some clients(fake client used in tests) do not support patching status reliably
		// and updates even spec changes. So, revert any spec changes(like deployment defaults) we made.
		// Revert back spec changes, those are not supposed to get saved on the API server.
		deployment.Spec.DeepCopyInto(&dep.Spec)

		if err := r.PatchDeploymentStatus(dep, client.MergeFrom(deployment.DeepCopy())); err != nil {
			klog.Warningf("failed to update status %q for deployment %q: %v",
				dep.Status.Phase, dep.Name, err)
		}

		klog.Infof("End of reconcile. Duration: %v", time.Since(startTime))
	}()

	d := &PmemCSIDriver{dep, r.namespace, r.k8sVersion}
	if err := d.Reconcile(r); err != nil {
		klog.Infof("Reconcile error: %v", err)
		dep.Status.Phase = pmemcsiv1alpha1.DeploymentPhaseFailed
		dep.Status.Reason = err.Error()
		r.evRecorder.Event(dep, corev1.EventTypeWarning, pmemcsiv1alpha1.EventReasonFailed, err.Error())

		return reconcile.Result{Requeue: true, RequeueAfter: requeueDelayOnError}, err
	}

	dep.Status.Phase = pmemcsiv1alpha1.DeploymentPhaseRunning
	dep.Status.Reason = "All driver components are deployed successfully"
	r.evRecorder.Event(dep, corev1.EventTypeNormal, pmemcsiv1alpha1.EventReasonRunning, "Driver deployment successful")

	return reconcile.Result{}, nil
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

// Delete delete existing Kubernetes object
func (r *ReconcileDeployment) Delete(obj runtime.Object) error {
	metaObj, err := meta.Accessor(obj)
	if err != nil {
		return fmt.Errorf("internal error %T: %v", obj, err)
	}
	klog.Infof("Deleting '%s/%s' of type '%T'", metaObj.GetNamespace(), metaObj.GetName(), obj)
	return r.client.Delete(context.TODO(), obj)
}

// PatchDeploymentStatus patches the give given deployment CR status
func (r *ReconcileDeployment) PatchDeploymentStatus(dep *pmemcsiv1alpha1.Deployment, patch client.Patch) error {
	dep.Status.LastUpdated = metav1.Now()
	// Passing a copy of CR to patch as the fake client used in tests
	// will write back the changes to both status and spec.
	if err := r.client.Status().Patch(context.TODO(), dep.DeepCopy(), patch); err != nil {
		return err
	}
	// update the status of cached deployment
	r.cacheDeploymentStatus(dep.GetName(), dep.Status)
	return nil
}

func (r *ReconcileDeployment) saveDeployment(d *pmemcsiv1alpha1.Deployment) {
	r.deploymentsMutex.Lock()
	defer r.deploymentsMutex.Unlock()
	r.deployments[d.Name] = d
}

func (r *ReconcileDeployment) getDeployment(name string) *pmemcsiv1alpha1.Deployment {
	r.deploymentsMutex.Lock()
	defer r.deploymentsMutex.Unlock()
	return r.deployments[name]
}

func (r *ReconcileDeployment) deleteDeployment(name string) {
	r.deploymentsMutex.Lock()
	defer r.deploymentsMutex.Unlock()
	delete(r.deployments, name)
}

func (r *ReconcileDeployment) cacheDeploymentStatus(name string, status pmemcsiv1alpha1.DeploymentStatus) {
	r.deploymentsMutex.Lock()
	defer r.deploymentsMutex.Unlock()
	if d, ok := r.deployments[name]; ok {
		status.DeepCopyInto(&d.Status)
	}
}

func (r *ReconcileDeployment) getDeploymentFor(obj metav1.Object) (*PmemCSIDriver, error) {
	r.deploymentsMutex.Lock()
	defer r.deploymentsMutex.Unlock()
	for _, d := range r.deployments {
		selfRef := d.GetOwnerReference()
		if isOwnedBy(obj, &selfRef) {
			deployment := d.DeepCopy()
			if err := deployment.EnsureDefaults(r.containerImage); err != nil {
				return nil, err
			}
			return &PmemCSIDriver{deployment, r.namespace, r.k8sVersion}, nil
		}
	}

	return nil, fmt.Errorf("Not found")
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
		if owner.UID == expectedOwner.UID {
			return true
		}
	}

	return false
}
