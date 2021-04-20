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
	"github.com/intel/pmem-csi/pkg/logger"
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
		for _, region := range bus.ActiveRegions() {
			l.V(3).Info("checking", "region", region)
			if region.Readonly() {
				l.V(3).Info("skipped because read-only")
				continue
			}
			vgName := pmemcommon.VgName(bus, region)
			for _, namespace := range region.AllNamespaces() {
				l.V(3).Info("checking", "namespace", namespace)
				size := namespace.Size()
				if size <= 0 {
					l.V(3).Info("skipped because size is zero")
					continue
				}

				switch namespace.Mode() {
				case ndctl.RawMode:
					l.V(2).Info("converting raw namespace", "namespace", namespace)
					// We don't even try to set the special namespace alt name here.
					// This code is supposed to be used for legacy PMEM where the
					// name would be silently ignored by ndctl. By not even trying
					// set it, we avoid a special case and can test this also
					// with PMEM were the name would be set.
					_, err := exec.RunCommand("ndctl", "create-namespace",
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
					l.V(2).Info("setting up volume group", "namespace", namespace, "VG", vgName)
					devName := "/dev/" + namespace.BlockDeviceName()
					if err := setupVGForNamespaces(vgName, devName); err != nil {
						finalErr = err
						return
					}
					l.V(2).Info("converted to fsdax namespace", "namespace", namespace, "VG", vgName)
					numConverted++
				default:
					l.V(3).Info("ignoring namespace because of mode", "mode", namespace.Mode())
				}
			}
		}
	}

	return
}

func havePMEM(ctx context.Context, ndctx ndctl.Context) error {
	l := logger.Get(ctx).WithName("check-pmem")

	haveFsdaxWithName := 0
	haveVG := 0
	for _, bus := range ndctx.GetBuses() {
		l.V(5).Info("checking", "bus", bus)
		for _, region := range bus.ActiveRegions() {
			l.V(5).Info("checking", "region", region)
			if region.Readonly() {
				l.V(3).Info("skipped because read-only")
				continue
			}
			vgName := pmemcommon.VgName(bus, region)
			for _, namespace := range region.AllNamespaces() {
				l.V(5).Info("checking", "namespace", namespace)
				size := namespace.Size()
				if size > 0 &&
					namespace.Mode() == ndctl.FsdaxMode &&
					namespace.Name() == pmemCSINamespaceName {
					l.V(3).Info("namespace will be used by PMEM-CSI in LVM mode because of name", "namespace", namespace)
					haveFsdaxWithName++
				}
			}
			if _, err := exec.RunCommand("vgdisplay", vgName); err != nil {
				l.V(5).Info("volume group does not exist", "VG", vgName)
			} else {
				l.V(3).Info("volume group will be used by PMEM-CSI in LVM mode", "VG", vgName)
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
