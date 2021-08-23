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

	api "github.com/intel/pmem-csi/pkg/apis/pmemcsi/v1beta1"
	"github.com/intel/pmem-csi/pkg/k8sutil"
	"github.com/intel/pmem-csi/pkg/logger"
	pmemcontroller "github.com/intel/pmem-csi/pkg/pmem-csi-operator/controller"
	"github.com/intel/pmem-csi/pkg/pmem-csi-operator/metrics"
	"github.com/intel/pmem-csi/pkg/version"
	"github.com/operator-framework/operator-lib/handler"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	v1 "k8s.io/client-go/kubernetes/typed/core/v1"
	"k8s.io/client-go/tools/record"
	"k8s.io/kubectl/pkg/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/event"
	crhandler "sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	"sigs.k8s.io/controller-runtime/pkg/source"
)

func init() {
	// AddToManagerFuncs is a list of functions to create controllers and add them to a manager.
	pmemcontroller.AddToManagerFuncs = append(pmemcontroller.AddToManagerFuncs, Add)

	metrics.RegisterMetrics()
}

// Add creates a new Deployment Controller and adds it to the Manager. The Manager will set fields on the Controller
// and Start it when the Manager is Started.
func Add(ctx context.Context, mgr manager.Manager, opts pmemcontroller.ControllerOptions) error {
	r, err := NewReconcileDeployment(ctx, mgr.GetClient(), opts)
	if err != nil {
		return err
	}
	return add(ctx, mgr, r.(*ReconcileDeployment))
}

// add adds a new Controller to mgr with r as the reconcile.Reconciler
func add(ctx context.Context, mgr manager.Manager, r *ReconcileDeployment) error {
	// Create a new controller
	c, err := controller.New("deployment-controller", mgr, controller.Options{Reconciler: r})
	if err != nil {
		return fmt.Errorf("create controller: %v", err)
	}
	l := logger.Get(ctx)

	p := predicate.Funcs{
		UpdateFunc: func(e event.UpdateEvent) bool {
			r.reconcileMutex.Lock()
			defer r.reconcileMutex.Unlock()
			l.V(3).Info("UPDATED", "object", logger.KObjWithType(e.ObjectOld), "generation", e.ObjectNew.GetGeneration())
			if e.ObjectNew.GetDeletionTimestamp() != nil {
				// Deployment CR deleted, remove it's reference from cache.
				// Objects owned by it are automatically garbage collected.
				r.deleteDeployment(e.ObjectOld.GetName())
				return false
			}
			if e.ObjectOld.GetGeneration() == e.ObjectNew.GetGeneration() {
				// No changes registered
				return false
			}

			patch := client.MergeFrom(e.ObjectOld)
			data, err := patch.Data(e.ObjectNew)
			if err != nil {
				l.Error(err, "find deployment changes")
				return true
			}
			l.V(3).Info("all changes", "diff", string(data))
			// We are intersted in only spec changes, not CR status/metadata changes
			re := regexp.MustCompile(`{.*"spec":{(.*)}.*}`)
			res := re.FindSubmatch(data)
			if len(res) < 2 {
				l.V(3).Info("no spec changes observed, ignoring the event")
				return false
			}
			l.V(3).Info("CR changes", "diff", string(res[1]))
			return true
		},
		DeleteFunc: func(e event.DeleteEvent) bool {
			r.reconcileMutex.Lock()
			defer r.reconcileMutex.Unlock()
			l.V(3).Info("DELETED", "object", logger.KObjWithType(e.Object))
			// Deployment CR deleted, remove it's reference from cache.
			// Objects owned by it are automatically garbage collected.
			r.deleteDeployment(e.Object.GetName())
			// We already handled the event here,
			// so no more further reconcile required.
			return false
		},
	}

	// Watch for changes to primary resource Deployment
	if err := c.Watch(&source.Kind{Type: &api.PmemCSIDeployment{}}, &handler.InstrumentedEnqueueRequestForObject{}, p); err != nil {
		return fmt.Errorf("watch: %v", err)
	}

	// Predicate functions for sub-object changes
	// We do not want to pollute deployment reconcile loop by sending
	// sub-object changes, instead we provide a dedicated handler.
	// So all these event handlers returns 'false' so that the event
	// is not propagated further.
	// One exception is: If we fail to handle here, then we pass this
	// event to reconcile loop, where it should recognize these requests
	// and just requeue. Expecting that the failure is retried.
	eventFunc := func(what string, obj client.Object) bool {
		// TODO:
		// - check that this output is okay
		// - check why "go test" does not cover this code
		l.V(3).Info(what, "object", logger.KObjWithType(obj))
		// Get the owned deployment
		d, err := r.getDeploymentFor(ctx, obj)
		if err != nil {
			l.V(3).Info("not owned by any deployment", "object", logger.KObjWithType(obj))
			// The owner might have deleted already
			// we can safely ignore this event
			return false
		}
		r.reconcileMutex.Lock()
		defer r.reconcileMutex.Unlock()
		// TODO (?): single parameter
		if err := d.handleEvent(ctx, obj, obj, r); err != nil {
			l.Error(err, "while handling the event, requeuing the event")
			return true
		}
		return false
	}
	sop := predicate.Funcs{
		DeleteFunc: func(e event.DeleteEvent) bool {
			return eventFunc("DELETED", e.Object)
		},
		UpdateFunc: func(e event.UpdateEvent) bool {
			if e.ObjectNew.GetDeletionTimestamp() != nil {
				// We can handle this in delete handler
				return false
			}
			return eventFunc("UPDATED", e.ObjectOld)
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

	for _, resource := range currentObjects {
		if err := c.Watch(&source.Kind{Type: resource}, &crhandler.EnqueueRequestForOwner{
			IsController: true,
			OwnerType:    &api.PmemCSIDeployment{},
		}, sop); err != nil {
			return fmt.Errorf("create watch: %v", err)
		}
	}

	return nil
}

// blank assignment to verify that ReconcileDeployment implements reconcile.Reconciler
var _ reconcile.Reconciler = &ReconcileDeployment{}

// ReconcileHook function to be invoked on reconciling a deployment.
type ReconcileHook *func(d *api.PmemCSIDeployment)

// ReconcileDeployment reconciles a Deployment object
type ReconcileDeployment struct {
	// The context passed into NewReconcileDeployment, i.e. the same for all
	// operations.
	ctx context.Context

	client        client.Client
	evBroadcaster record.EventBroadcaster
	evRecorder    record.EventRecorder
	namespace     string
	k8sVersion    version.Version
	// container image used for deploying the operator
	containerImage string
	// known deployments
	deployments map[string]*api.PmemCSIDeployment
	// deploymentsMutex protects concurrent access to deployments
	deploymentsMutex sync.Mutex
	// reconcileMutex synchronizes concurrent reconcile calls
	// initiated by change events, either deployment or sub-object
	// changes.
	reconcileMutex sync.Mutex
	reconcileHooks map[ReconcileHook]struct{}
}

// NewReconcileDeployment creates new deployment reconciler
func NewReconcileDeployment(ctx context.Context, client client.Client, opts pmemcontroller.ControllerOptions) (reconcile.Reconciler, error) {
	// "reconcile" will be part of all future log messages.
	l := logger.Get(ctx).WithName("reconcile")
	ctx = logger.Set(ctx, l)

	if opts.Namespace == "" {
		opts.Namespace = k8sutil.GetNamespace(ctx)
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
		image, err := containerImage(ctx, cs, opts.Namespace)
		if err != nil {
			return nil, fmt.Errorf("failed to find the operator image: %v", err)
		}
		opts.DriverImage = image
	}
	l.Info("new instance", "defaultDriverImage", opts.DriverImage)

	evBroadcaster := record.NewBroadcaster()
	evBroadcaster.StartRecordingToSink(&v1.EventSinkImpl{Interface: opts.EventsClient})
	evRecorder := evBroadcaster.NewRecorder(scheme.Scheme, corev1.EventSource{Component: "pmem-csi-operator"})

	return &ReconcileDeployment{
		ctx:            ctx,
		client:         client,
		evBroadcaster:  evBroadcaster,
		evRecorder:     evRecorder,
		k8sVersion:     opts.K8sVersion,
		namespace:      opts.Namespace,
		containerImage: opts.DriverImage,
		deployments:    map[string]*api.PmemCSIDeployment{},
		reconcileHooks: map[ReconcileHook]struct{}{},
	}, nil
}

// Reconcile reads that state of the cluster for a Deployment object and makes changes based on the state read
// and what is in the Deployment.Spec
// Note:
// The Controller will requeue the Request to be processed again if the returned error is non-nil or
// Result.Requeue is true, otherwise upon completion it will remove the work from the queue.
func (r *ReconcileDeployment) Reconcile(ctx context.Context, request reconcile.Request) (reconcile.Result, error) {
	var requeue bool
	var err error
	r.reconcileMutex.Lock()
	defer r.reconcileMutex.Unlock()
	startTime := time.Now()

	requeueDelayOnError := 2 * time.Minute
	l := logger.Get(r.ctx).WithValues("deployment", request.NamespacedName.Name)
	ctx = logger.Set(ctx, l)

	// Fetch the Deployment instance
	deployment := &api.PmemCSIDeployment{}
	err = r.client.Get(ctx, request.NamespacedName, deployment)
	if err != nil {
		l.Error(err, "failed to retrieve CR to reconcile", "deployment", request.Name)
		// One reason for this could be a failed predicate event handler of
		// sub-objects. So requeue the request so that the same predicate
		// handle could be called on that object.
		return reconcile.Result{Requeue: requeue, RequeueAfter: requeueDelayOnError}, err
	}

	l.V(3).Info("reconcile starting", "deployment", deployment.GetName())

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

	if deployment.Status.Phase == api.DeploymentPhaseNew {
		/* New deployment */
		r.evRecorder.Event(deployment, corev1.EventTypeNormal, api.EventReasonNew, "Processing new driver deployment")
	}

	// Cache the deployment
	r.saveDeployment(deployment)

	dep := deployment.DeepCopy()

	// update status
	defer func() {
		l.V(5).Info("updating deployment status")
		// Some clients(fake client used in tests) do not support patching status reliably
		// and updates even spec changes. So, revert any spec changes(like deployment defaults) we made.
		// Revert back spec changes, those are not supposed to get saved on the API server.
		deployment.Spec.DeepCopyInto(&dep.Spec)

		if err := r.patchDeploymentStatus(dep, client.MergeFrom(deployment.DeepCopy())); err != nil {
			l.Error(err, "failed to update status", "phase", dep.Status.Phase, "deployment", dep.Name)
			// TODO: requeue object?!
		}

		l.V(3).Info("reconcile done", "duration", time.Since(startTime))
		if err := metrics.SetReconcileMetrics(deployment.Name, string(deployment.UID)); err != nil {
			l.V(3).Error(err, "failed to set reconcile metrics", "object", deployment)
		}
	}()

	d, err := r.newDeployment(ctx, dep)
	if err == nil {
		err = d.reconcile(ctx, r)
	}
	if err != nil {
		l.Error(err, "reconcile failed")
		dep.Status.Phase = api.DeploymentPhaseFailed
		dep.Status.Reason = err.Error()
		r.evRecorder.Event(dep, corev1.EventTypeWarning, api.EventReasonFailed, err.Error())

		return reconcile.Result{Requeue: true, RequeueAfter: requeueDelayOnError}, err
	}

	dep.Status.Phase = api.DeploymentPhaseRunning
	dep.Status.Reason = "All driver components are deployed successfully"
	r.evRecorder.Event(dep, corev1.EventTypeNormal, api.EventReasonRunning, "Driver deployment successful")

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
	r.reconcileMutex.Lock()
	defer r.reconcileMutex.Unlock()
	r.reconcileHooks[h] = struct{}{}
}

// RemoveHook removes previously added hook
func (r *ReconcileDeployment) RemoveHook(h ReconcileHook) {
	r.reconcileMutex.Lock()
	defer r.reconcileMutex.Unlock()
	delete(r.reconcileHooks, h)
}

//Get tries to retrives the Kubernetes objects
func (r *ReconcileDeployment) Get(obj client.Object) error {
	key := client.ObjectKeyFromObject(obj)
	return r.client.Get(r.ctx, key, obj)
}

// Delete delete existing Kubernetes object
func (r *ReconcileDeployment) Delete(obj client.Object) error {
	logger.Get(r.ctx).Info("deleting", "object", logger.KObjWithType(obj))
	return r.client.Delete(r.ctx, obj)
}

// PatchDeploymentStatus patches the give given deployment CR status
func (r *ReconcileDeployment) patchDeploymentStatus(dep *api.PmemCSIDeployment, patch client.Patch) error {
	dep.Status.LastUpdated = metav1.Now()
	// Passing a copy of CR to patch as the fake client used in tests
	// will write back the changes to both status and spec.
	if err := r.client.Status().Patch(r.ctx, dep.DeepCopy(), patch); err != nil {
		return err
	}
	// update the status of cached deployment
	r.cacheDeploymentStatus(dep.GetName(), dep.Status)
	return nil
}

func (r *ReconcileDeployment) saveDeployment(d *api.PmemCSIDeployment) {
	r.deploymentsMutex.Lock()
	defer r.deploymentsMutex.Unlock()
	r.deployments[d.Name] = d
}

func (r *ReconcileDeployment) deleteDeployment(name string) {
	r.deploymentsMutex.Lock()
	defer r.deploymentsMutex.Unlock()
	delete(r.deployments, name)
}

func (r *ReconcileDeployment) cacheDeploymentStatus(name string, status api.DeploymentStatus) {
	r.deploymentsMutex.Lock()
	defer r.deploymentsMutex.Unlock()
	if d, ok := r.deployments[name]; ok {
		status.DeepCopyInto(&d.Status)
	}
}

func (r *ReconcileDeployment) getDeploymentFor(ctx context.Context, obj metav1.Object) (*pmemCSIDeployment, error) {
	r.deploymentsMutex.Lock()
	defer r.deploymentsMutex.Unlock()
	for _, d := range r.deployments {
		selfRef := d.GetOwnerReference()
		if isOwnedBy(obj, &selfRef) {
			// Don't modify the existing deployment, clone it first.
			d := d.DeepCopy()
			return r.newDeployment(ctx, d)
		}
	}

	return nil, fmt.Errorf("Not found")
}

// newDeployment prepares for object creation and will modify the PmemCSIDeployment.
// Callers who don't want that need to clone it first.
func (r *ReconcileDeployment) newDeployment(ctx context.Context, deployment *api.PmemCSIDeployment) (*pmemCSIDeployment, error) {
	l := logger.Get(ctx).WithName("newDeployment")

	if err := deployment.EnsureDefaults(r.containerImage); err != nil {
		return nil, err
	}

	d := &pmemCSIDeployment{
		PmemCSIDeployment: deployment,
		namespace:         r.namespace,
		k8sVersion:        r.k8sVersion,
	}

	switch d.Spec.ControllerTLSSecret {
	case "":
		// Nothing to do.
	case api.ControllerTLSSecretOpenshift:
		// Nothing to load, we just add annotations.
	default:
		// Load the specified secret.
		secret := &corev1.Secret{
			TypeMeta: metav1.TypeMeta{
				APIVersion: "v1",
				Kind:       "Secret",
			},
		}
		objKey := client.ObjectKey{
			Namespace: d.namespace,
			Name:      d.Spec.ControllerTLSSecret,
		}
		if err := r.client.Get(ctx, objKey, secret); err != nil {
			return nil, fmt.Errorf("loading ControllerTLSSecret %s from namespace %s: %v", d.Spec.ControllerTLSSecret, d.namespace, err)
		}
		ca, ok := secret.Data[api.TLSSecretCA]
		if !ok {
			return nil, fmt.Errorf("ControllerTLSSecret %s in namespace %s contains no %s", d.Spec.ControllerTLSSecret, d.namespace, api.TLSSecretCA)
		}
		d.controllerCABundle = ca
		if len(d.controllerCABundle) == 0 {
			return nil, fmt.Errorf("ControllerTLSSecret %s in namespace %s contains empty %s", d.Spec.ControllerTLSSecret, d.namespace, api.TLSSecretCA)
		}
		l.V(3).Info("load controller TLS secret", "secret", objKey, "CAlength", len(d.controllerCABundle))
	}

	return d, nil
}

// containerImage returns container image name used by operator Pod
func containerImage(ctx context.Context, cs *kubernetes.Clientset, namespace string) (string, error) {
	const podNameEnv = "POD_NAME"
	const operatorNameEnv = "OPERATOR_NAME"

	containerImage := ""
	// Operator deployment shall provide this environment
	if podName := os.Getenv(podNameEnv); podName != "" {
		pod, err := cs.CoreV1().Pods(namespace).Get(ctx, podName, metav1.GetOptions{})
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
