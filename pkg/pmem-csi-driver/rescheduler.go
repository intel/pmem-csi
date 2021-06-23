/*
Copyright 2021 Intel Corporation.

SPDX-License-Identifier: Apache-2.0
*/

package pmemcsidriver

import (
	"context"
	"errors"
	"fmt"

	pmemlog "github.com/intel/pmem-csi/pkg/logger"
	"github.com/intel/pmem-csi/pkg/types"

	v1 "k8s.io/api/core/v1"
	storagev1 "k8s.io/api/storage/v1"
	apierrs "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/client-go/kubernetes"
	storagelistersv1 "k8s.io/client-go/listers/storage/v1"
	"k8s.io/client-go/tools/cache"
	"sigs.k8s.io/sig-storage-lib-external-provisioner/v6/controller"
)

const (
	annSelectedNode = "volume.kubernetes.io/selected-node"
)

// newRescheduler creates an instance of
// sig-storage-lib-external-provisioner which has only one purpose: it
// detects PVCs that were assigned to a node which doesn't have a
// PMEM-CSI node driver running and triggers re-scheduling of those
// PVCs by removing the "selected node" annotation. It never
// provisions volumes. That is handled by the node instances.
func newRescheduler(ctx context.Context,
	driverName string,
	client kubernetes.Interface,
	pvcInformer cache.SharedIndexInformer,
	scInformer cache.SharedIndexInformer,
	pvInformer cache.SharedIndexInformer,
	csiNodeLister storagelistersv1.CSINodeLister,
	nodeSelector types.NodeSelector,
	serverGitVersion string) *pmemCSIProvisioner {
	provisionerOptions := []func(*controller.ProvisionController) error{
		controller.LeaderElection(false),
		controller.ClaimsInformer(pvcInformer),
		controller.ClassesInformer(scInformer),
		controller.VolumesInformer(pvInformer),
	}

	pcp := &pmemCSIProvisioner{
		driverName:    driverName,
		nodeSelector:  nodeSelector,
		csiNodeLister: csiNodeLister,
	}

	provisionController := controller.NewProvisionController(
		client,
		driverName,
		pcp,
		serverGitVersion,
		provisionerOptions...,
	)

	pcp.provisionController = provisionController
	return pcp
}

type pmemCSIProvisioner struct {
	driverName          string
	nodeSelector        types.NodeSelector
	csiNodeLister       storagelistersv1.CSINodeLister
	provisionController *controller.ProvisionController
}

// startRescheduler logs errors and cancels the context when it runs
// into a problem, either during the startup phase (blocking) or later
// at runtime (in a go routine).
func (pcp *pmemCSIProvisioner) startRescheduler(ctx context.Context, cancel func()) {
	l := pmemlog.Get(ctx).WithName("rescheduler")

	l.Info("starting")
	go func() {
		defer cancel()
		defer l.Info("stopped")
		pcp.provisionController.Run(ctx)
	}()
}

// ShouldProvision is called for each pending PVC before the lib
// starts working on the PVC. We only deal with those which need to be
// rescheduled.
func (pcp *pmemCSIProvisioner) ShouldProvision(ctx context.Context, pvc *v1.PersistentVolumeClaim) bool {
	l := pmemlog.Get(ctx)

	reschedule, err := pcp.shouldReschedule(ctx, pvc, nil)
	if err != nil {
		// Something went wrong. We have to allow the lib to
		// start working on this PVC, otherwise users will
		// never see error events.
		l.Error(err, "deprovision check failed")
		reschedule = true
	}
	return reschedule
}

// Provision is called after the lib has emitted an event about "starting to provision".
// Despite the name, the only outcome is "no change" (= leave PVC unmodified)
// or "reschedule" (= remove selected node annotation).
func (pcp *pmemCSIProvisioner) Provision(ctx context.Context, opts controller.ProvisionOptions) (*v1.PersistentVolume, controller.ProvisioningState, error) {
	reschedule, err := pcp.shouldReschedule(ctx, opts.PVC, opts.SelectedNode)
	if err != nil {
		return nil, controller.ProvisioningNoChange, fmt.Errorf("deprovision check failed: %v", err)
	}
	if reschedule {
		return nil, controller.ProvisioningReschedule, fmt.Errorf("reschedule PVC %s/%s because it is assigned to node %s which has no PMEM-CSI driver",
			opts.PVC.Namespace, opts.PVC.Name, opts.SelectedNode.Name)
	}
	if opts.SelectedNode != nil {
		err = &controller.IgnoredError{
			Reason: fmt.Sprintf("not responsible for provisioning of PVC %s/%s because it will be handled by the PMEM-CSI driver on node %q",
				opts.PVC.Namespace, opts.PVC.Name, opts.SelectedNode.Name),
		}
	} else {
		err = &controller.IgnoredError{
			Reason: fmt.Sprintf("not responsible for provisioning of PVC %s/%s because it is not assigned to a node",
				opts.PVC.Namespace, opts.PVC.Name),
		}
	}
	return nil, controller.ProvisioningNoChange, err
}

func (pcp *pmemCSIProvisioner) Delete(context.Context, *v1.PersistentVolume) error {
	return errors.New("not implemented")
}

func (pcp *pmemCSIProvisioner) shouldReschedule(ctx context.Context, pvc *v1.PersistentVolumeClaim, node *v1.Node) (bool, error) {
	l := pmemlog.Get(ctx).WithName("ShouldReschedulePVC").WithValues("pvc", pmemlog.KObj(pvc))
	if node != nil {
		l = l.WithValues("node", pmemlog.KObj(node))
	}

	// "node" might be nil. Check the label directly.
	var selectedNode string
	if pvc.Annotations != nil {
		selectedNode = pvc.Annotations[annSelectedNode]
	}
	if selectedNode == "" {
		// No need to reschedule.
		l.V(5).Info("no need to reschedule, no selected node")
		return false, nil
	}

	// We have to be absolutely certain that the PVC is not going
	// to be handled on the node. If we remove the annotation
	// while a driver node instance starts to provisision it,
	// volumes may leak.
	//
	// Therefore we check both the labels on the node ("Should a
	// PMEM-CSI driver run here?") and the CSINode object ("Does a
	// PMEM-CSI driver (still) run here?").
	//
	// The node check is expensive. We either would have to watch
	// all nodes (which is expensive) or do a GET per check (also
	// expensive). To mitigate this, the node is only checked when
	// called through Provision() with a Node object already
	// retrieved by the lib.
	//
	// When the scheduler extensions work, we should rarely get to
	// Provision() because typically PVCs get assigned to nodes
	// with PMEM-CSI and thus the CSINode check already bails out
	// of ShouldProvision.
	//
	// Only when the extensions are off, then Provision() may get
	// called more often. Such a cluster setup should better be
	// avoided.
	driverIsRunning := true
	csiNode, err := pcp.csiNodeLister.Get(selectedNode)
	switch {
	case err == nil:
		driverIsRunning = hasDriver(csiNode, pcp.driverName)
	case apierrs.IsNotFound(err):
		driverIsRunning = false
	default:
		return false, fmt.Errorf("retrieve CSINode %s: %v", selectedNode, err)
	}
	if node == nil {
		// Decide only based on CSINode.
		reschedule := !driverIsRunning
		l.V(3).Info("result", "reschedule", reschedule, "driverIsRunning", driverIsRunning)
		return reschedule, nil
	}

	driverMightRun := pcp.nodeSelector.MatchesLabels(node.Labels)

	reschedule := !driverMightRun && !driverIsRunning
	l.V(3).Info("result", "reschedule", reschedule, "driverMightRun", driverMightRun, "driverIsRunning", driverIsRunning)
	return reschedule, nil
}

func hasDriver(csiNode *storagev1.CSINode, driverName string) bool {
	for _, driver := range csiNode.Spec.Drivers {
		if driver.Name == driverName {
			return true
		}
	}
	return false
}
