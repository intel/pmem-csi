/*
Copyright 2021 Intel Corporation.

SPDX-License-Identifier: Apache-2.0
*/

package pmdmanager

import (
	"context"
	"errors"
	"fmt"
	"strings"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	k8stypes "k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes"

	"github.com/intel/pmem-csi/pkg/exec"
	pmemlog "github.com/intel/pmem-csi/pkg/logger"
	"github.com/intel/pmem-csi/pkg/ndctl"
	pmemcommon "github.com/intel/pmem-csi/pkg/pmem-common"
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
func ForceConvertRawNamespaces(ctx context.Context, client kubernetes.Interface, driverName string, nodeSelector types.NodeSelector, nodeName string) (finalErr error) {
	ctx, _ = pmemlog.WithName(ctx, "ForceConvertRawNamespaces")
	defer func() {
		if finalErr == nil {
			return
		}

		// Gather some information and append it.
		finalErr = fmt.Errorf("%w\n%s\n%s",
			finalErr,
			exec.CmdResult("ndctl", "list", "-NRi"),
			exec.CmdResult("vgdisplay"),
		)
	}()

	ndctx, err := ndctl.NewContext()
	if err != nil {
		return fmt.Errorf("ndctl: %v", err)
	}

	if _, err := convert(ctx, ndctx); err != nil {
		return err
	}

	if err := havePMEM(ctx, ndctx); err != nil {
		return err
	}

	if err := relabel(ctx, client, driverName, nodeSelector, nodeName); err != nil {
		return fmt.Errorf("relabel node %s: %v:", nodeName, err)
	}
	return nil
}

func convert(ctx context.Context, ndctx ndctl.Context) (numConverted int, finalErr error) {
	ctx, logger := pmemlog.WithName(ctx, "convert")
	defer func() {
		if finalErr != nil {
			logger.Error(finalErr, "failed", "converted", numConverted)
		} else {
			logger.V(3).Info("successful", "converted", numConverted)
		}
	}()

	logger.V(3).Info("checking for namespaces")
	for _, bus := range ndctx.GetBuses() {
		logger.V(3).Info("checking", "bus", bus)
		for _, region := range bus.ActiveRegions() {
			logger.V(3).Info("checking", "region", region)
			if region.Readonly() {
				logger.V(3).Info("skipped because read-only")
				continue
			}
			vgName := pmemcommon.VgName(bus, region)
			for _, namespace := range region.AllNamespaces() {
				logger.V(3).Info("checking", "namespace", namespace)
				size := namespace.Size()
				if size <= 0 {
					logger.V(3).Info("skipped because size is zero")
					continue
				}

				switch namespace.Mode() {
				case ndctl.RawMode:
					logger.V(2).Info("converting raw namespace", "namespace", namespace)
					// We don't even try to set the special namespace alt name here.
					// This code is supposed to be used for legacy PMEM where the
					// name would be silently ignored by ndctl. By not even trying
					// set it, we avoid a special case and can test this also
					// with PMEM were the name would be set.
					_, err := exec.RunCommand(ctx, "ndctl", "create-namespace",
						"--force", "--mode", "fsdax",
						"--bus", bus.DeviceName(),
						"--region", region.DeviceName(),
						"--reconfig", namespace.DeviceName(),
					)
					if err != nil {
						finalErr = err
						return
					}
					fallthrough
				case ndctl.FsdaxMode:
					// If it has the right name, then PMEM-CSI in LVM mode will
					// manage it and we are done with it. Because of this special check,
					// preparing a node as required by PMEM-CSI and then forcing
					// conversion skips the unnecessary conversion and handles such
					// a node normally.
					if namespace.Name() == pmemCSINamespaceName {
						continue
					}
					// Otherwise we must have the right volume group for it.
					// If we don't, try to create it.
					logger.V(2).Info("setting up volume group", "namespace", namespace, "vg", vgName)
					devName := "/dev/" + namespace.BlockDeviceName()
					if err := setupVGForNamespaces(ctx, vgName, devName); err != nil {
						finalErr = err
						return
					}
					logger.V(2).Info("converted to fsdax namespace", "namespace", namespace, "vg", vgName)
					numConverted++
				default:
					logger.V(3).Info("ignoring namespace because of mode", "mode", namespace.Mode())
				}
			}
		}
	}

	return
}

func havePMEM(ctx context.Context, ndctx ndctl.Context) error {
	ctx, logger := pmemlog.WithName(ctx, "havePMEM")

	haveFsdaxWithName := 0
	haveVG := 0
	for _, bus := range ndctx.GetBuses() {
		logger.V(5).Info("Checking bus", "bus", bus)
		for _, region := range bus.ActiveRegions() {
			logger.V(5).Info("Checking region", "region", region)
			if region.Readonly() {
				logger.V(3).Info("Skipped because read-only")
				continue
			}
			vgName := pmemcommon.VgName(bus, region)
			for _, namespace := range region.AllNamespaces() {
				logger.V(5).Info("Checking namespace", "namespace", namespace)
				size := namespace.Size()
				if size > 0 &&
					namespace.Mode() == ndctl.FsdaxMode &&
					namespace.Name() == pmemCSINamespaceName {
					logger.V(3).Info("Namespace will be used by PMEM-CSI in LVM mode because of name", "namespace", namespace)
					haveFsdaxWithName++
				}
			}
			if _, err := exec.RunCommand(ctx, "vgdisplay", vgName); err != nil {
				logger.V(5).Info("Volume group does not exist", "vg", vgName)
			} else {
				logger.V(3).Info("Volume group will be used by PMEM-CSI in LVM mode", "vg", vgName)
				haveVG++
			}
		}
	}
	if haveFsdaxWithName == 0 && haveVG == 0 {
		return errors.New("no volume group and no suitable namespace found")
	}
	return nil
}

func relabel(ctx context.Context, client kubernetes.Interface, driverName string, nodeSelector types.NodeSelector, nodeName string) error {
	ctx, logger := pmemlog.WithName(ctx, "relabel")
	labels := []string{}

	// Remove "force" label.
	labels = append(labels, fmt.Sprintf(`"%s/%s": null`, driverName, ConvertRawNamespacesLabel))
	// Add labels for node driver.
	for key, value := range nodeSelector {
		labels = append(labels, fmt.Sprintf("%q: %q", key, value))
	}

	// Apply patch.
	patch := fmt.Sprintf(`{"metadata":{"labels":{%s}}}`, strings.Join(labels, ", "))
	logger.V(5).Info("Node", "patch", patch)
	if _, err := client.CoreV1().Nodes().Patch(ctx, nodeName, k8stypes.MergePatchType, []byte(patch), metav1.PatchOptions{}, ""); err != nil {
		return fmt.Errorf("failed to patch node: %v", err)
	}
	logger.V(3).Info("Change node labels", "node", nodeName, "patch", patch)
	return nil
}
