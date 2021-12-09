/*
Copyright 2019,2020 Intel Corporation

SPDX-License-Identifier: Apache-2.0
*/

package parameters

import (
	"fmt"
	"strings"
	"testing"

	"k8s.io/apimachinery/pkg/api/resource"

	"github.com/stretchr/testify/assert"
)

func TestParameters(t *testing.T) {
	yes := true
	normal := PersistencyNormal
	gig := "1Gi"
	gigNum := int64(1 * 1024 * 1024 * 1024)
	appDirect := UsageAppDirect
	fileIO := UsageFileIO

	tests := []struct {
		name       string
		origin     Origin
		stringmap  map[string]string
		parameters Volume
		err        string
	}{
		{
			name:   "ephemeral",
			origin: EphemeralVolumeOrigin,
			stringmap: VolumeContext{
				EraseAfter:               "true",
				Size:                     gig,
				"csi.storage.k8s.io/foo": "bar",
			},
			parameters: Volume{
				EraseAfter: &yes,
				Size:       &gigNum,
			},
		},

		// Various parameters which are not allowed in this context.
		{
			name:   "invalid-parameter-create",
			origin: CreateVolumeOrigin,
			stringmap: VolumeContext{
				Size: "100",
			},
			err: "parameter \"size\" invalid in this context",
		},
		{
			name:   "invalid-persistent-context",
			origin: PersistentVolumeOrigin,
			stringmap: VolumeContext{
				"foo": "bar",
			},
			err: "parameter \"foo\" invalid in this context",
		},
		{
			name:   "invalid-node-context",
			origin: NodeVolumeOrigin,
			stringmap: VolumeContext{
				"foo": "bar",
			},
			err: "parameter \"foo\" invalid in this context",
		},

		// Usage values.
		{
			name:   "invalid-usage",
			origin: CreateVolumeOrigin,
			stringmap: VolumeContext{
				UsageModel: "Foo",
			},
			err: "parameter \"usage\": unknown value: Foo",
		},
		{
			name:   "invalid-kata-containers",
			origin: CreateVolumeOrigin,
			stringmap: VolumeContext{
				UsageModel:     "FileIO",
				KataContainers: "true",
			},
			err: "Kata Container support and usage \"FileIO\" are mutually exclusive",
		},
		{
			name:   "valid-usage-app-direct",
			origin: CreateVolumeOrigin,
			stringmap: VolumeContext{
				UsageModel: "AppDirect",
			},
			parameters: Volume{
				Usage: &appDirect,
			},
		},
		{
			name:   "valid-usage-file-io",
			origin: CreateVolumeOrigin,
			stringmap: VolumeContext{
				UsageModel: "FileIO",
			},
			parameters: Volume{
				Usage: &fileIO,
			},
		},

		// Parse errors for size.
		{
			name:   "invalid-size-suffix",
			origin: EphemeralVolumeOrigin,
			stringmap: VolumeContext{
				Size: "1X",
			},
			err: "parameter \"size\": failed to parse \"1X\" as int64: quantities must match the regular expression '^([+-]?[0-9.]+)([eEinumkKMGTP]*[-+]?[0-9]*)$'",
		},
		{
			name:   "invalid-size-string",
			origin: EphemeralVolumeOrigin,
			stringmap: VolumeContext{
				Size: "foo",
			},
			err: "parameter \"size\": failed to parse \"foo\" as int64: quantities must match the regular expression '^([+-]?[0-9.]+)([eEinumkKMGTP]*[-+]?[0-9]*)$'",
		},

		// Legacy state files.
		{
			name:   "model-none",
			origin: NodeVolumeOrigin,
			stringmap: VolumeContext{
				PersistencyModel: "none",
			},
			parameters: Volume{
				Persistency: &normal,
			},
		},
	}
	for _, tt := range tests {
		tt := tt
		filteredMap := func() VolumeContext {
			result := VolumeContext{}
			for key, value := range tt.stringmap {
				switch key {
				case Size:
					quantity := resource.MustParse(value)
					value = fmt.Sprintf("%d", quantity.Value())
				case PersistencyModel:
					if value == "none" {
						value = "normal"
					}
				}
				if key != ProvisionerID &&
					!strings.HasPrefix(key, PodInfoPrefix) {
					result[key] = value
				}
			}
			return result
		}

		t.Run(tt.name, func(t *testing.T) {
			parameters, err := Parse(tt.origin, tt.stringmap)
			switch {
			case tt.err == "":
				if assert.NoError(t, err, "no parse error") &&
					assert.Equal(t, tt.parameters, parameters) {
					stringmap := parameters.ToContext()
					assert.Equal(t, filteredMap(), stringmap, "re-encoded volume context")
				}
			case err == nil:
				assert.Error(t, err, "expected error: "+tt.err)
			default:
				assert.Equal(t, tt.err, err.Error(), "parse error")
			}
		})
	}
}
