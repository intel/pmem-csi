/*
Copyright 2021 Intel Corporation.

SPDX-License-Identifier: Apache-2.0
*/

package pmemcsidriver

import (
	"errors"
	"testing"

	v1 "k8s.io/api/core/v1"
	storagev1 "k8s.io/api/storage/v1"
	apierrs "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/klog/v2/ktesting"
	"sigs.k8s.io/sig-storage-lib-external-provisioner/v6/controller"

	"github.com/intel/pmem-csi/pkg/types"
)

type testcase struct {
	driverName    string
	haveCSIDriver bool
	haveCSINode   bool
	selectedNode  string
	nodeLabels    map[string]string
	nodeSelector  types.NodeSelector

	expectError                bool
	expectReschedulePreCheck   bool
	expectRescheduleFinalCheck bool
}

const (
	driverName     = "pmem-csi.intel.com"
	nodeLabelName  = "storage"
	nodeLabelValue = "pmem"
	nodeName       = "pmem-worker"
)

func TestRescheduler(t *testing.T) {
	testcases := map[string]testcase{
		"node-okay": {
			driverName:    driverName,
			haveCSIDriver: true,
			haveCSINode:   true,
			selectedNode:  nodeName,
			nodeSelector: types.NodeSelector{
				nodeLabelName: nodeLabelValue,
			},
			nodeLabels: map[string]string{
				nodeLabelName: nodeLabelValue,
			},
		},
		"missing-csi-node": {
			driverName:    driverName,
			haveCSIDriver: true,
			haveCSINode:   false,
			selectedNode:  nodeName,
			nodeSelector: types.NodeSelector{
				nodeLabelName: nodeLabelValue,
			},
			nodeLabels: map[string]string{
				nodeLabelName: nodeLabelValue,
			},

			expectReschedulePreCheck:   true,
			expectRescheduleFinalCheck: false,
		},
		"missing-csi-driver": {
			driverName:    driverName,
			haveCSIDriver: false,
			haveCSINode:   true,
			selectedNode:  nodeName,
			nodeSelector: types.NodeSelector{
				nodeLabelName: nodeLabelValue,
			},
			nodeLabels: map[string]string{
				nodeLabelName: nodeLabelValue,
			},

			expectReschedulePreCheck:   true,
			expectRescheduleFinalCheck: false,
		},
		"wrong-csi-driver": {
			driverName:    "other." + driverName,
			haveCSIDriver: false,
			haveCSINode:   true,
			selectedNode:  nodeName,
			nodeSelector: types.NodeSelector{
				nodeLabelName: nodeLabelValue,
			},
			nodeLabels: map[string]string{
				nodeLabelName: nodeLabelValue,
			},

			expectReschedulePreCheck:   true,
			expectRescheduleFinalCheck: false,
		},
		"missing-node-labels": {
			driverName:    driverName,
			haveCSIDriver: true,
			haveCSINode:   true,
			selectedNode:  nodeName,
			nodeSelector: types.NodeSelector{
				nodeLabelName: nodeLabelValue,
			},
			nodeLabels: map[string]string{},

			expectReschedulePreCheck:   false,
			expectRescheduleFinalCheck: false,
		},
		"reschedule": {
			driverName:    driverName,
			haveCSIDriver: false,
			haveCSINode:   true,
			selectedNode:  nodeName,
			nodeSelector: types.NodeSelector{
				nodeLabelName: nodeLabelValue,
			},
			nodeLabels: map[string]string{},

			expectReschedulePreCheck:   true,
			expectRescheduleFinalCheck: true,
		},
	}

	for name, tc := range testcases {
		t.Run(name, func(t *testing.T) {
			_, ctx := ktesting.NewTestContext(t)
			pcp := pmemCSIProvisioner{
				driverName:   driverName,
				nodeSelector: tc.nodeSelector,
				csiNodeLister: fakeCSINodeLister{
					driverName:    tc.driverName,
					haveCSIDriver: tc.haveCSIDriver,
					haveCSINode:   tc.haveCSINode,
				},
			}

			pvc := &v1.PersistentVolumeClaim{}
			if tc.selectedNode != "" {
				pvc.Annotations = map[string]string{
					annSelectedNode: tc.selectedNode,
				}
			}

			if pcp.ShouldProvision(ctx, pvc) != tc.expectReschedulePreCheck {
				t.Errorf("ShouldProvision unexpectedly returned %v", !tc.expectReschedulePreCheck)
			}

			node := &v1.Node{}
			node.Name = tc.selectedNode
			node.Labels = tc.nodeLabels
			pv, state, err := pcp.Provision(ctx, controller.ProvisionOptions{
				PVC:          pvc,
				SelectedNode: node,
			})
			if pv != nil {
				t.Error("Provision returned non-nil PV")
			}
			switch {
			case tc.expectError:
				if state != controller.ProvisioningNoChange {
					t.Errorf("expected state %s, got %s", controller.ProvisioningNoChange, state)
				}
				if err == nil {
					t.Error("expected error, got nil")
				} else {
					_, ignored := err.(*controller.IgnoredError)
					if ignored {
						t.Errorf("expected normal error, got IgnoredError: %v", err)
					}
				}
			case tc.expectRescheduleFinalCheck:
				if state != controller.ProvisioningReschedule {
					t.Errorf("expected state %s, got %s", controller.ProvisioningReschedule, state)
				}
				if err == nil {
					t.Error("expected error, got nil")
				} else {
					_, ignored := err.(*controller.IgnoredError)
					if ignored {
						t.Errorf("expected normal error, got IgnoredError: %v", err)
					}
				}
			default:
				if state != controller.ProvisioningNoChange {
					t.Errorf("expected state %s, got %s", controller.ProvisioningNoChange, state)
				}
				if err == nil {
					t.Error("expected error, got nil")
				} else {
					_, ignored := err.(*controller.IgnoredError)
					if !ignored {
						t.Errorf("expected ignored error, got normal error: %v", err)
					}
				}
			}
		})
	}
}

type fakeCSINodeLister struct {
	driverName    string
	haveCSIDriver bool
	haveCSINode   bool
}

func (f fakeCSINodeLister) Get(nodeName string) (*storagev1.CSINode, error) {
	if !f.haveCSINode {
		return nil, apierrs.NewNotFound(schema.GroupResource{}, "nodeName")
	}
	csiNode := &storagev1.CSINode{}
	csiNode.Name = nodeName
	if f.haveCSIDriver {
		csiNode.Spec.Drivers = []storagev1.CSINodeDriver{
			{Name: f.driverName},
		}
	}
	return csiNode, nil
}

func (f fakeCSINodeLister) List(labels.Selector) ([]*storagev1.CSINode, error) {
	return nil, errors.New("not implemented")
}
