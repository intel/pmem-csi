/*
Copyright 2021 Intel Corporation.

SPDX-License-Identifier: Apache-2.0
*/

package pmdmanager

import (
	"context"
	"fmt"
	"os"
	"strings"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	k8stypes "k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes"

	"github.com/google/uuid"
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

// convert is an implementation of `ndctl create-namespace -e namespace0.0 --mode fsdax --force`
//
// For example:
// [fedora@pmem-csi-pmem-govm-worker3 ~]$ sudo ndctl list -NR
// [
//   {
//     "provider":"ACPI.NFIT",
//     "dev":"ndbus0",
//     "regions":[
//       {
//         "dev":"region0",
//         "size":68719476736,
//         "align":16777216,
//         "available_size":0,
//         "max_available_extent":0,
//         "type":"pmem",
//         "numa_node":0,
//         "target_node":0,
//         "persistence_domain":"unknown",
//         "namespaces":[
//           {
//             "dev":"namespace0.0",
//             "mode":"raw",
//             "size":68719476736,
//             "sector_size":512,
//             "state":"disabled",
//             "numa_node":0,
//             "target_node":0
//           }
//         ]
//       }
//     ]
//   }
// ]
//
// [fedora@pmem-csi-pmem-govm-worker3 ~]$ sudo ndctl create-namespace -e namespace0.0 --mode fsdax --force
// {
//   "dev":"namespace0.0",
//   "mode":"fsdax",
//   "map":"dev",
//   "size":"63.00 GiB (67.64 GB)",
//   "uuid":"c6d75928-770e-4e81-a58b-7149cf76c336",
//   "sector_size":512,
//   "align":2097152,
//   "blockdev":"pmem0"
// }
//
// [fedora@pmem-csi-pmem-govm-worker3 ~]$ sudo ndctl list -NRicvv
// [
//   {
//     "provider":"ACPI.NFIT",
//     "dev":"ndbus0",
//     "regions":[
//       {
//         "dev":"region0",
//         "size":68719476736,
//         "align":16777216,
//         "available_size":0,
//         "max_available_extent":0,
//         "type":"pmem",
//         "numa_node":0,
//         "target_node":0,
//         "iset_id":52512752653219036,
//         "persistence_domain":"unknown",
//         "namespaces":[
//           {
//             "dev":"namespace0.1",
//             "mode":"raw",
//             "size":0,
//             "uuid":"00000000-0000-0000-0000-000000000000",
//             "sector_size":512,
//             "state":"disabled",
//             "numa_node":0,
//             "target_node":0
//           },
//           {
//             "dev":"namespace0.0",
//             "mode":"fsdax",
//             "map":"dev",
//             "size":67643637760,
//             "uuid":"c6d75928-770e-4e81-a58b-7149cf76c336",
//             "raw_uuid":"c98d4bba-c37a-46ac-a7f1-c169eace9118",
//             "sector_size":512,
//             "align":2097152,
//             "blockdev":"pmem0",
//             "numa_node":0,
//             "target_node":0
//           }
//         ]
//       }
//     ]
//   }
// ]
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

				// namespace_prep_reconfig (https://github.com/pmem/ndctl/blob/ea014c0c9ec8d0ef945d072dcc52b306c7a686f9/ndctl/namespace.c#L1108-L1158)
				if err := namespace.Disable(); err != nil {
					finalErr = err
					return
				}

				// Zero the info block (https://github.com/pmem/ndctl/blob/ea014c0c9ec8d0ef945d072dcc52b306c7a686f9/ndctl/namespace.c#L1042-L1106).
				// The block should already be empty, but without this SetRawMode/Enable/SetRawMode/Disable sequence
				// the namespace.Set calls below failed with "device node found".
				err := func() error {
					blockDevice := namespace.BlockDeviceName()
					if blockDevice == "" {
						return nil
					}
					if err := namespace.SetRawMode(true); err != nil {
						return err
					}
					if err := namespace.Enable(); err != nil {
						return err
					}
					path := "/dev/" + blockDevice
					file, err := os.OpenFile(path, os.O_RDWR|os.O_EXCL, 0)
					if err != nil {
						return err
					}
					defer file.Close()
					infoSize := 8192
					buffer := make([]byte, infoSize, infoSize)
					written, err := file.WriteAt(buffer, 0)
					if err != nil {
						return fmt.Errorf("failed to overwrite %s: %v", path, err)
					}
					if written < infoSize {
						return fmt.Errorf("unexpected short write to %s, only %d out of %d written", path, written, infoSize)
					}
					if err := file.Close(); err != nil {
						return fmt.Errorf("closing %s failed: %v", path, err)
					}
					if err := namespace.SetRawMode(false); err != nil {
						return err
					}
					if err := namespace.Disable(); err != nil {
						return err
					}
					return nil
				}()
				if err != nil {
					finalErr = fmt.Errorf("clearing the info block: %v", err)
					return
				}

				// We don't attempt to enable labels
				// (in contrast to
				// https://github.com/pmem/ndctl/blob/ea014c0c9ec8d0ef945d072dcc52b306c7a686f9/ndctl/namespace.c#L1287-L1305)
				// because the whole point of this
				// code is to support PMEM hardware
				// that doesn't support labels.

				// setup_namespace (https://github.com/pmem/ndctl/blob/ea014c0c9ec8d0ef945d072dcc52b306c7a686f9/ndctl/namespace.c#L493-L606)
				// first sets some parameters, changes the mode, then sets up the PFN.
				align, err := region.FsdaxAlignment()
				if err != nil {
					finalErr = err
					return
				}
				// Setting UUID, size and alt name
				// does not work for legacy persistent
				// memory and has to be skipped.
				if namespace.Type() != ndctl.IoNamespace {
					uid, _ := uuid.NewUUID()
					if err := namespace.SetUUID(uid); err != nil {
						finalErr = err
						return
					}
					if err := namespace.SetSize(size); err != nil {
						finalErr = err
						return
					}
					// Set the name that is expected by LVM device manager.
					if err := namespace.SetAltName("pmem-csi"); err != nil {
						finalErr = err
						return
					}
				}
				if err := namespace.SetEnforceMode(ndctl.FsdaxMode); err != nil {
					finalErr = fmt.Errorf("set fsdax mode: %v", err)
					return
				}
				if err := namespace.SetPfnSeed(ndctl.DeviceMap, align); err != nil {
					finalErr = err
					return
				}
				if err := namespace.Enable(); err != nil {
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
