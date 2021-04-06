/*
Copyright 2021 Intel Corporation.

SPDX-License-Identifier: Apache-2.0
*/

package pmdmanager

import (
	"context"
	"fmt"
	"strings"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	k8stypes "k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes"

	"github.com/intel/pmem-csi/pkg/exec"
	"github.com/intel/pmem-csi/pkg/logger"
	"github.com/intel/pmem-csi/pkg/ndctl"
	"github.com/intel/pmem-csi/pkg/types"
)

const (
	ConvertRawNamespacesLabel = "convert-raw-namespaces"
	ConvertRawNamespacesValye = "force"
)

// ForceConvertRawNamespaces iterates over all raw namespaces,
// force-converts them to fsdax + LVM volume group, then modifies the
// node labels such that the normal driver runs instead of this
// special one-time operation.
func ForceConvertRawNamespaces(ctx context.Context, client kubernetes.Interface, driverName string, nodeSelector types.NodeSelector, nodeName string) error {
	ndctx, err := ndctl.NewContext()
	if err != nil {
		return fmt.Errorf("ndctl: %v", err)
	}

	numConverted, err := convert(ctx, ndctx)
	if err != nil {
		return err
	}
	if numConverted == 0 {
		// Don't add the label if we didn't actually create any suitable namespace.
		nodeSelector = nil
	}
	if err := relabel(ctx, client, driverName, nodeSelector, nodeName); err != nil {
		return fmt.Errorf("relabel node %s: %v:", nodeName, err)
	}
	return nil
}

func convert(ctx context.Context, ndctx ndctl.Context) (numConverted int, finalErr error) {
	l := logger.Get(ctx).WithName("raw-namespace-conversion")
	defer func() {
		if finalErr != nil {
			l.Error(finalErr, "failed", "converted", numConverted)
		} else {
			l.V(3).Info("successful", "converted", numConverted)
		}
	}()

	l.V(3).Info("checking for namespaces")
	for _, bus := range ndctx.GetBuses() {
		l.V(3).Info("checking", "bus", bus)
		for _, region := range bus.AllRegions() {
			l.V(3).Info("checking", "region", region)
			if region.Readonly() {
				l.V(3).Info("skipped because read-only")
				continue
			}
			for _, namespace := range region.AllNamespaces() {
				l.V(5).Info("checking", "namespace", namespace)
				size := namespace.Size()
				if size <= 0 {
					l.V(3).Info("skipped because size is zero")
					continue
				}
				if namespace.Mode() != ndctl.RawMode {
					l.V(3).Info("skipped because mode is not " + string(ndctl.RawMode))
					continue
				}

				l.V(2).Info("converting raw namespace", "namespace", namespace)

				// TODO (?): verify that the name was set
				// and if not, create the volume group because the LVM
				// device manager will ignore the converted namespace
				_, err := exec.RunCommand("ndctl", "create-namespace",
					"--force", "--mode", "fsdax", "--name", "pmem-csi",
					"--bus", bus.DeviceName(),
					"--region", region.DeviceName(),
					"--reconfig", namespace.DeviceName(),
				)
				if err != nil {
					finalErr = err
					return
				}

				l.V(2).Info("converted to fsdax namespace", "namespace", namespace)
				numConverted++
			}
		}
	}

	return
}

func relabel(ctx context.Context, client kubernetes.Interface, driverName string, nodeSelector types.NodeSelector, nodeName string) error {
	l := logger.Get(ctx).WithName("relabel")

	labels := []string{}

	// Remove "force" label.
	labels = append(labels, fmt.Sprintf(`"%s/%s": null`, driverName, ConvertRawNamespacesLabel))
	// Add labels for node driver.
	for key, value := range nodeSelector {
		labels = append(labels, fmt.Sprintf("%q: %q", key, value))
	}

	// Apply patch.
	patch := fmt.Sprintf(`{"metadata":{"labels":{%s}}}`, strings.Join(labels, ", "))
	l.V(5).Info("node", "patch", patch)
	if _, err := client.CoreV1().Nodes().Patch(ctx, nodeName, k8stypes.MergePatchType, []byte(patch), metav1.PatchOptions{}, ""); err != nil {
		return fmt.Errorf("failed to patch node: %v", err)
	}
	l.V(3).Info("change node labels", "node", nodeName, "patch", patch)
	return nil
}
