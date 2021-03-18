/*
Copyright 2021 Intel Corporation.

SPDX-License-Identifier: Apache-2.0
*/

package pmdmanager

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes/fake"

	"github.com/intel/pmem-csi/pkg/logger"
	"github.com/intel/pmem-csi/pkg/logger/testinglogger"
	"github.com/intel/pmem-csi/pkg/ndctl"
	ndctlfake "github.com/intel/pmem-csi/pkg/ndctl/fake"
	"github.com/intel/pmem-csi/pkg/types"
)

func TestConvert(t *testing.T) {
	testcases := map[string]struct {
		hardware    ndctl.Context
		expectError bool
		expectNum   int
	}{
		"nop": {
			hardware: ndctlfake.NewContext(&ndctlfake.Context{}),
		},
		"no-region": {
			hardware: ndctlfake.NewContext(&ndctlfake.Context{
				Buses: []ndctl.Bus{
					&ndctlfake.Bus{},
				},
			}),
		},
		"no-namespace": {
			hardware: ndctlfake.NewContext(&ndctlfake.Context{
				Buses: []ndctl.Bus{
					&ndctlfake.Bus{
						Regions_: []ndctl.Region{
							&ndctlfake.Region{},
						},
					},
				},
			}),
		},
		"readonly-region": {
			hardware: func() ndctl.Context {
				hardware := makeRawNamespace()
				hardware.Buses[0].(*ndctlfake.Bus).Regions_[0].(*ndctlfake.Region).Readonly_ = true
				return hardware
			}(),
		},
		"fsdax-namespace": {
			hardware: func() ndctl.Context {
				hardware := makeRawNamespace()
				hardware.Buses[0].(*ndctlfake.Bus).Regions_[0].(*ndctlfake.Region).Namespaces_[0].(*ndctlfake.Namespace).Mode_ = ndctl.FsdaxMode
				return hardware
			}(),
		},
		"deleted-namespace": {
			hardware: func() ndctl.Context {
				hardware := makeRawNamespace()
				hardware.Buses[0].(*ndctlfake.Bus).Regions_[0].(*ndctlfake.Region).Namespaces_[0].(*ndctlfake.Namespace).Size_ = 0
				return hardware
			}(),
		},
		"convert-one": {
			hardware:  makeRawNamespace(),
			expectNum: 1,
		},
		"convert-two-namespaces": {
			hardware:  func() ndctl.Context {
				hardware := makeRawNamespace()
				region := hardware.Buses[0].(*ndctlfake.Bus).Regions_[0].(*ndctlfake.Region)
				ns := *region.Namespaces_[0].(*ndctlfake.Namespace)
				region.Namespaces_ = append(region.Namespaces_, &ns)
				return hardware
			}(),
			expectNum: 2,
		},
		"convert-two-regions": {
			hardware:  func() ndctl.Context {
				hardware := makeRawNamespace()
				bus := hardware.Buses[0].(*ndctlfake.Bus)
				region := bus.Regions_[0].(*ndctlfake.Region)
				ns := *region.Namespaces_[0].(*ndctlfake.Namespace)
				bus.Regions_ = append(bus.Regions_,
					&ndctlfake.Region{
						Type_: ndctl.PmemRegion,
						Namespaces_: []ndctl.Namespace{&ns},
					})
				return hardware
			}(),
			expectNum: 2,
		},
	}

	for name, tc := range testcases {
		t.Run(name, func(t *testing.T) {
			ctx := logger.Set(context.Background(), testinglogger.New(t))
			numConverted, err := convert(ctx, tc.hardware)
			if tc.expectError {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
			}
			require.Equal(t, tc.expectNum, numConverted)
		})
	}
}

// makeRawNamespace creates a context with exactly one raw namespace
// that needs to be converted.
func makeRawNamespace() *ndctlfake.Context {
	return ndctlfake.NewContext(&ndctlfake.Context{
		Buses: []ndctl.Bus{
			&ndctlfake.Bus{
				Regions_: []ndctl.Region{
					&ndctlfake.Region{
						Type_: ndctl.PmemRegion,
						Namespaces_: []ndctl.Namespace{
							&ndctlfake.Namespace{
								Mode_:    ndctl.RawMode,
								Size_:    1024 * 1024 * 1024,
								Enabled_: true,
								Active_:  true,
							},
						},
					},
				},
			},
		},
	})
}

func TestRelabel(t *testing.T) {
	testcases := map[string]struct {
		objects      []runtime.Object
		driverName   string
		nodeSelector types.NodeSelector
		nodeName     string
		expectError  bool
		expectNode   *v1.Node
	}{
		"okay": {
			objects: []runtime.Object{makeNode("worker", map[string]string{
				"pmem-csi/convert-raw-namespaces": "force",
				"foo":                             "bar",
			})},
			driverName: "pmem-csi",
			nodeSelector: types.NodeSelector{
				"storage": "pmem",
			},
			nodeName: "worker",
			expectNode: makeNode("worker", map[string]string{
				"storage": "pmem",
				"foo":     "bar",
			}),
		},
		"nop": {
			objects: []runtime.Object{makeNode("worker", map[string]string{
				"storage": "pmem",
				"foo":     "bar",
			})},
			driverName: "pmem-csi",
			nodeSelector: types.NodeSelector{
				"storage": "pmem",
			},
			nodeName: "worker",
			expectNode: makeNode("worker", map[string]string{
				"storage": "pmem",
				"foo":     "bar",
			}),
		},
		"no-node": {
			driverName: "pmem-csi",
			nodeSelector: types.NodeSelector{
				"storage": "pmem",
			},
			nodeName:    "worker",
			expectError: true,
		},
		"complex-selector": {
			objects: []runtime.Object{makeNode("worker", map[string]string{
				"pmem-csi/convert-raw-namespaces": "force",
			})},
			driverName: "pmem-csi",
			nodeSelector: types.NodeSelector{
				"x":         "y",
				"a":         "b",
				"yyyy/zzzz": "1",
			},
			nodeName: "worker",
			expectNode: makeNode("worker", map[string]string{
				"x":         "y",
				"a":         "b",
				"yyyy/zzzz": "1",
			}),
		},
		"other-drivername": {
			objects: []runtime.Object{makeNode("worker", map[string]string{
				"pmem-csi1/convert-raw-namespaces": "force",
				"pmem-csi2/convert-raw-namespaces": "force",
				"foo":                              "bar",
			})},
			driverName: "pmem-csi1",
			nodeSelector: types.NodeSelector{
				"storage": "pmem",
			},
			nodeName: "worker",
			expectNode: makeNode("worker", map[string]string{
				"pmem-csi2/convert-raw-namespaces": "force",
				"storage":                          "pmem",
				"foo":                              "bar",
			}),
		},
	}

	for name, tc := range testcases {
		t.Run(name, func(t *testing.T) {
			ctx := logger.Set(context.Background(), testinglogger.New(t))
			client := fake.NewSimpleClientset(tc.objects...)
			err := relabel(ctx, client, tc.driverName, tc.nodeSelector, tc.nodeName)
			if tc.expectError {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
				node, err := client.CoreV1().Nodes().Get(ctx, tc.nodeName, metav1.GetOptions{})
				require.NoError(t, err, "get node")
				require.Equal(t, *tc.expectNode, *node)
			}
		})
	}
}

func makeNode(nodeName string, labels map[string]string) *v1.Node {
	return &v1.Node{
		ObjectMeta: metav1.ObjectMeta{
			Name:   nodeName,
			Labels: labels,
		},
	}
}
