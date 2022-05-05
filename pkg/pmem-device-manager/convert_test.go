/*
Copyright 2021 Intel Corporation.

SPDX-License-Identifier: Apache-2.0
*/

package pmdmanager

import (
	"io/ioutil"
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes/fake"
	"k8s.io/klog/v2/ktesting"

	"github.com/intel/pmem-csi/pkg/ndctl"
	ndctlfake "github.com/intel/pmem-csi/pkg/ndctl/fake"
	"github.com/intel/pmem-csi/pkg/types"
)

func TestConvert(t *testing.T) {
	withName := `#!/bin/sh
cat <<EOF
{
  "dev":"namespace0.0",
  "mode":"fsdax",
  "map":"dev",
  "size":67643637760,
  "uuid":"f4afa860-b590-451f-8d64-3e2eb228d366",
  "sector_size":512,
  "align":2097152,
  "blockdev":"pmem0",
  "name":"pmem-csi"
}
EOF
`
	failure := `#!/bin/sh
echo "$@: fake error"
exit 1
`

	failureForNamespace02 := `#!/bin/sh
case "$@" in
    *namespace0.2*)
        echo "fake error"
        exit 1
        ;;
    *)
        exit 0
        ;;
esac
`

	pvsNone := `#!/bin/sh
case "$*" in
    --noheadings\ -o\ vg_name\ /dev/pmem0)
       exit 0
       ;;
    *)
       echo >&2 "unexpected invocation: $*"
       exit 1
       ;;
esac
`
	pvsOne := `#!/bin/sh
case "$*" in
    --noheadings\ -o\ vg_name\ /dev/pmem0)
       echo "bus0region0fsdax"
       ;;
    *)
       echo >&2 "unexpected invocation: $*"
       exit 1
       ;;
esac
`

	vgCreateOkay := `#!/bin/sh
case "$*" in
    --force\ bus0region*fsdax\ /dev/pmem*)
       exit 0
       ;;
    *)
       echo >&2 "unexpected invocation: $*"
       exit 1
       ;;
esac
`

	vgExtendOkay := `#!/bin/sh
case "$*" in
    --force\ bus0region*fsdax\ /dev/pmem*)
       exit 0
       ;;
    *)
       echo >&2 "unexpected invocation: $*"
       exit 1
       ;;
esac
`

	vgDisplayOne := `#!/bin/sh
case "$*" in
    bus0region0fsdax)
       echo "bus0degion0fsdax: okay" # output does not matter
       ;;
    *)
       echo >&2 "unexpected invocation: $*"
       exit 1
       ;;
esac
`
	testcases := map[string]struct {
		hardware    ndctl.Context
		scripts     map[string]string
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
		"fsdax-namespace-with-name": {
			hardware: func() ndctl.Context {
				hardware := makeRawNamespace()
				region := hardware.Buses[0].(*ndctlfake.Bus).Regions_[0].(*ndctlfake.Region)
				ns := region.Namespaces_[0].(*ndctlfake.Namespace)
				// already converted with special name
				ns.Mode_ = ndctl.FsdaxMode
				ns.Name_ = "pmem-csi"
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
			hardware: makeRawNamespace(),
			scripts: map[string]string{
				"ndctl":    withName,
				"pvs":      pvsNone,
				"vgcreate": vgCreateOkay,
			},
			expectNum: 1,
		},
		"only-vgcreate": {
			hardware: func() ndctl.Context {
				hardware := makeRawNamespace()
				region := hardware.Buses[0].(*ndctlfake.Bus).Regions_[0].(*ndctlfake.Region)
				ns := region.Namespaces_[0].(*ndctlfake.Namespace)
				ns.Mode_ = ndctl.FsdaxMode // already converted
				return hardware
			}(),
			scripts: map[string]string{
				"pvs":      pvsNone,
				"vgcreate": vgCreateOkay,
			},
			expectNum: 1,
		},
		"vgextend": {
			hardware: makeRawNamespace(),
			scripts: map[string]string{
				"ndctl":     withName,
				"pvs":       pvsNone,
				"vgextend":  vgExtendOkay,
				"vgdisplay": vgDisplayOne,
			},
			expectNum: 1,
		},
		"namespace-in-vg": {
			hardware: func() ndctl.Context {
				hardware := makeRawNamespace()
				region := hardware.Buses[0].(*ndctlfake.Bus).Regions_[0].(*ndctlfake.Region)
				ns := region.Namespaces_[0].(*ndctlfake.Namespace)
				ns.Mode_ = ndctl.FsdaxMode // already converted
				return hardware
			}(),
			scripts: map[string]string{
				"pvs": pvsOne,
			},
			expectNum: 1, // counted as conversion despite the nop in setupVGForNamespaces
		},
		"convert-two-namespaces": {
			hardware: func() ndctl.Context {
				hardware := makeRawNamespace()
				region := hardware.Buses[0].(*ndctlfake.Bus).Regions_[0].(*ndctlfake.Region)
				ns := *region.Namespaces_[0].(*ndctlfake.Namespace)
				region.Namespaces_ = append(region.Namespaces_, &ns)
				return hardware
			}(),
			scripts: map[string]string{
				"ndctl":    withName,
				"pvs":      pvsNone,
				"vgcreate": vgCreateOkay,
			},
			expectNum: 2,
		},
		"convert-two-regions": {
			hardware: func() ndctl.Context {
				hardware := makeRawNamespace()
				bus := hardware.Buses[0].(*ndctlfake.Bus)
				region := bus.Regions_[0].(*ndctlfake.Region)
				ns := *region.Namespaces_[0].(*ndctlfake.Namespace)
				ns.BlockDeviceName_ = "pmem1"
				ns.DeviceName_ = "namespace1.0"
				bus.Regions_ = append(bus.Regions_,
					&ndctlfake.Region{
						Type_:       ndctl.PmemRegion,
						DeviceName_: "region1",
						Enabled_:    true,
						Namespaces_: []ndctl.Namespace{&ns},
					})
				return hardware
			}(),
			scripts: map[string]string{
				"ndctl":    withName,
				"pvs":      pvsNone,
				"vgcreate": vgCreateOkay,
			},
			expectNum: 2,
		},
		"convert-failure": {
			hardware: makeRawNamespace(),
			scripts: map[string]string{
				"pvs":      pvsNone,
				"vgcreate": vgCreateOkay,
			},
			expectError: true,
		},
		"convert-failure-second-namespaces": {
			hardware: func() ndctl.Context {
				hardware := makeRawNamespace()
				region := hardware.Buses[0].(*ndctlfake.Bus).Regions_[0].(*ndctlfake.Region)
				ns := *region.Namespaces_[0].(*ndctlfake.Namespace)
				ns.DeviceName_ = "namespace0.2"
				ns.BlockDeviceName_ = "pmem1"
				region.Namespaces_ = append(region.Namespaces_, &ns)
				return hardware
			}(),
			scripts: map[string]string{
				"ndctl":    failureForNamespace02,
				"pvs":      pvsNone,
				"vgcreate": vgCreateOkay,
			},
			expectError: true,
			expectNum:   1,
		},
	}

	for name, tc := range testcases {
		t.Run(name, func(t *testing.T) {
			path := os.Getenv("PATH")
			defer os.Setenv("PATH", path)
			tmp := t.TempDir()

			// Create fake commands, backfilling with a
			// version which fails if called.
			for _, script := range []string{"ndctl", "pvs", "vgcreate", "vgdisplay", "vgextend"} {
				content, ok := tc.scripts[script]
				if !ok {
					content = failure
				}
				err := ioutil.WriteFile(tmp+"/"+script, []byte(content), 0700)
				require.NoError(t, err)
			}
			os.Setenv("PATH", tmp+":"+path)

			_, ctx := ktesting.NewTestContext(t)

			numConverted, err := convert(ctx, tc.hardware)
			if tc.expectError {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}
			assert.Equal(t, tc.expectNum, numConverted)
		})
	}
}

// makeRawNamespace creates a context with exactly one raw namespace
// that needs to be converted.
func makeRawNamespace() *ndctlfake.Context {
	return ndctlfake.NewContext(&ndctlfake.Context{
		Buses: []ndctl.Bus{
			&ndctlfake.Bus{
				DeviceName_: "bus0",
				Regions_: []ndctl.Region{
					&ndctlfake.Region{
						DeviceName_: "region0",
						Type_:       ndctl.PmemRegion,
						Enabled_:    true,
						Namespaces_: []ndctl.Namespace{
							&ndctlfake.Namespace{
								Mode_:            ndctl.RawMode,
								Size_:            1024 * 1024 * 1024,
								Enabled_:         true,
								Active_:          true,
								DeviceName_:      "namespace0.0",
								BlockDeviceName_: "pmem0",
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
			_, ctx := ktesting.NewTestContext(t)
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
