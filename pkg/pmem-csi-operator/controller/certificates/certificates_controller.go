/*
Copyright 2020 The Kubernetes Authors.

SPDX-License-Identifier: Apache-2.0
*/

package certificates

import (
	"context"
	"time"

	api "github.com/intel/pmem-csi/pkg/apis/pmemcsi/v1alpha1"
	certificates "k8s.io/api/certificates/v1beta1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
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
	return add(mgr, newReconcileCertificateRequests(mgr.GetClient(), mgr.GetScheme(), mgr.GetConfig()))
}

// add adds a new Controller to mgr with r as the reconcile.Reconciler
func add(mgr manager.Manager, r reconcile.Reconciler) error {
	// Create a new controller
	c, err := controller.New("certificaterequests-controller", mgr, controller.Options{Reconciler: r})
	if err != nil {
		return err
	}

	// Watch for changes to certificate signing requests initiated by Deployments
	err = c.Watch(&source.Kind{Type: &certificates.CertificateSigningRequest{}}, &handler.EnqueueRequestForObject{})
	if err != nil {
		return err
	}

	return nil
}

// blank assignment to verify that ReconcileCertificateRequests implements reconcile.Reconciler
var _ reconcile.Reconciler = &ReconcileCertificateRequests{}

// ReconcileCertificateRequests reconciles a Deployment object
type ReconcileCertificateRequests struct {
	// This client, initialized using mgr.Client() above, is a split client
	// that reads objects from the cache and writes to the apiserver
	client client.Client
	scheme *runtime.Scheme
	config *rest.Config
}

func newReconcileCertificateRequests(c client.Client, s *runtime.Scheme, cfg *rest.Config) reconcile.Reconciler {
	return &ReconcileCertificateRequests{
		client: c,
		scheme: s,
		config: cfg,
	}
}

// The Controller will requeue the Request to be processed again if the returned error is non-nil or
// Result.Requeue is true, otherwise upon completion it will remove the work from the queue.
func (r *ReconcileCertificateRequests) Reconcile(request reconcile.Request) (reconcile.Result, error) {
	// Fetch the Deployment instance
	csr := &certificates.CertificateSigningRequest{}
	err := r.client.Get(context.TODO(), request.NamespacedName, csr)
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

	deploymentName := ""
	for _, owner := range csr.OwnerReferences {
		if owner.Kind == "Deployment" &&
			owner.APIVersion == api.SchemeGroupVersion.String() {
			deploymentName = owner.Name
			break
		}
	}

	// Ignore any CSRs which are not owned by PMEM-CSI Deployment
	if deploymentName == "" {
		klog.Infof("CSR %q is not initiated by PMEM-CSI Operator, Ignoring!!!", csr.ObjectMeta.Name)
		return reconcile.Result{}, nil
	}

	klog.Infof("Found CSR %q, requested by %q", csr.ObjectMeta.Name, deploymentName)
	for _, c := range csr.Status.Conditions {
		if c.Type == certificates.CertificateApproved {
			// Already approved, nothing to do
			return reconcile.Result{}, err
		}
	}

	csr.Status.Conditions = append(csr.Status.Conditions, certificates.CertificateSigningRequestCondition{
		Type:           certificates.CertificateApproved,
		Reason:         "PMEM-CSI driver deployment",
		Message:        "Approved by PMEM-CSI Operator for driver deployment",
		LastUpdateTime: metav1.Now(),
	})

	certClient, err := certclient.NewForConfig(r.config)
	if err != nil {
		klog.Errorf("Failed to get certificates client: %v", err)
		return reconcile.Result{Requeue: true, RequeueAfter: 5 * time.Minute}, err
	}

	updatedCsr, err := certClient.CertificateSigningRequests().UpdateApproval(csr)
	if err != nil {
		klog.Errorf("Failed to update CSR %q: %v", csr.ObjectMeta.Name, err)
		return reconcile.Result{Requeue: true, RequeueAfter: 5 * time.Minute}, err
	}

	klog.Infof("Approved!!! Updated CSR: %q", updatedCsr.ObjectMeta.Name)

	return reconcile.Result{}, nil
}
