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
	five := uint(5)
	yes := true
	no := false
	cache := PersistencyCache
	normal := PersistencyNormal
	foo := "foo"
	gig := "1Gi"
	gigNum := int64(1 * 1024 * 1024 * 1024)
	name := "joe"

	tests := []struct {
		name       string
		origin     Origin
		stringmap  map[string]string
		parameters Volume
		err        string
	}{
		{
			name:   "createvolume",
			origin: CreateVolumeOrigin,
			stringmap: VolumeContext{
				CacheSize:        "5",
				EraseAfter:       "false",
				PersistencyModel: "cache",
			},
			parameters: Volume{
				CacheSize:   &five,
				EraseAfter:  &no,
				Persistency: &cache,
			},
		},
		{
			name:   "bad-volumeid",
			origin: CreateVolumeOrigin,
			stringmap: VolumeContext{
				VolumeID: foo,
			},
			err: `parameter "_id" invalid in this context`,
		},
		{
			name:   "good-volumeid",
			origin: CreateVolumeInternalOrigin,
			stringmap: VolumeContext{
				VolumeID: "foo",
			},
			parameters: Volume{
				VolumeID: &foo,
			},
		},
		{
			name:   "createvolumeinternal",
			origin: CreateVolumeInternalOrigin,
			stringmap: VolumeContext{
				CacheSize:        "5",
				EraseAfter:       "false",
				PersistencyModel: "cache",
				VolumeID:         "foo",
			},
			parameters: Volume{
				CacheSize:   &five,
				EraseAfter:  &no,
				Persistency: &cache,
				VolumeID:    &foo,
			},
		},
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
		{
			name:   "publishpersistent",
			origin: PersistentVolumeOrigin,
			stringmap: VolumeContext{
				CacheSize:        "5",
				EraseAfter:       "false",
				PersistencyModel: "cache",

				Name:                     name,
				"csi.storage.k8s.io/foo": "bar",
				ProvisionerID:            "provisioner XYZ",
			},
			parameters: Volume{
				CacheSize:   &five,
				EraseAfter:  &no,
				Persistency: &cache,
				Name:        &name,
			},
		},
		{
			name:   "node",
			origin: NodeVolumeOrigin,
			stringmap: VolumeContext{
				CacheSize:        "5",
				EraseAfter:       "false",
				PersistencyModel: "cache",
				Size:             gig,
				Name:             name,
			},
			parameters: Volume{
				CacheSize:   &five,
				EraseAfter:  &no,
				Persistency: &cache,
				Size:        &gigNum,
				Name:        &name,
			},
		},

		// Various parameters which are not allowed in this context.
		{
			name:   "invalid-parameter-create",
			origin: CreateVolumeOrigin,
			stringmap: VolumeContext{
				VolumeID: "volume-id-chosen-by-attacker",
			},
			err: "parameter \"_id\" invalid in this context",
		},
		{
			name:   "invalid-parameter-create-internal",
			origin: CreateVolumeInternalOrigin,
			stringmap: VolumeContext{
				Ephemeral: "false",
			},
			err: "parameter \"csi.storage.k8s.io/ephemeral\" invalid in this context",
		},
		{
			name:   "invalid-ephemeral-context",
			origin: EphemeralVolumeOrigin,
			stringmap: VolumeContext{
				CacheSize: gig,
			},
			err: "parameter \"cacheSize\" invalid in this context",
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
				VolumeID: "volume-id",
			},
			err: "parameter \"_id\" invalid in this context",
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
				if key != VolumeID &&
					key != ProvisionerID &&
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
