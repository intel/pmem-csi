/*
Copyright 2019 Intel Corporation

SPDX-License-Identifier: Apache-2.0
*/

package pmemcsidriver

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
	cache := persistencyCache
	foo := "foo"
	gig := "1Gi"
	gigNum := int64(1 * 1024 * 1024 * 1024)
	name := "joe"

	tests := []struct {
		name       string
		origin     parameterOrigin
		stringmap  map[string]string
		parameters volumeParameters
		err        string
	}{
		{
			name:   "createvolume",
			origin: createVolumeParameters,
			stringmap: map[string]string{
				parameterCacheSize:        "5",
				parameterEraseAfter:       "false",
				parameterPersistencyModel: "cache",
			},
			parameters: volumeParameters{
				cacheSize:   &five,
				eraseAfter:  &no,
				persistency: &cache,
			},
		},
		{
			name:   "bad-volumeid",
			origin: createVolumeParameters,
			stringmap: map[string]string{
				parameterVolumeID: foo,
			},
			err: `parameter "_id" invalid in this context`,
		},
		{
			name:   "good-volumeid",
			origin: createVolumeInternalParameters,
			stringmap: map[string]string{
				parameterVolumeID: "foo",
			},
			parameters: volumeParameters{
				volumeID: &foo,
			},
		},
		{
			name:   "createvolumeinternal",
			origin: createVolumeInternalParameters,
			stringmap: map[string]string{
				parameterCacheSize:        "5",
				parameterEraseAfter:       "false",
				parameterPersistencyModel: "cache",
				parameterVolumeID:         "foo",
			},
			parameters: volumeParameters{
				cacheSize:   &five,
				eraseAfter:  &no,
				persistency: &cache,
				volumeID:    &foo,
			},
		},
		{
			name:   "ephemeral",
			origin: ephemeralVolumeParameters,
			stringmap: map[string]string{
				parameterEraseAfter:      "true",
				parameterSize:            gig,
				"csi.storage.k8s.io/foo": "bar",
			},
			parameters: volumeParameters{
				eraseAfter: &yes,
				size:       &gigNum,
			},
		},
		{
			name:   "publishpersistent",
			origin: persistentVolumeParameters,
			stringmap: map[string]string{
				parameterCacheSize:        "5",
				parameterEraseAfter:       "false",
				parameterPersistencyModel: "cache",

				parameterName:            name,
				"csi.storage.k8s.io/foo": "bar",
				parameterProvisionerID:   "provisioner XYZ",
			},
			parameters: volumeParameters{
				cacheSize:   &five,
				eraseAfter:  &no,
				persistency: &cache,
				name:        &name,
			},
		},
		{
			name:   "node",
			origin: nodeVolumeParameters,
			stringmap: map[string]string{
				parameterCacheSize:        "5",
				parameterEraseAfter:       "false",
				parameterPersistencyModel: "cache",
				parameterSize:             gig,
				parameterName:             name,
			},
			parameters: volumeParameters{
				cacheSize:   &five,
				eraseAfter:  &no,
				persistency: &cache,
				size:        &gigNum,
				name:        &name,
			},
		},

		// Various parameters which are not allowed in this context.
		{
			name:   "invalid-parameter-create",
			origin: createVolumeParameters,
			stringmap: map[string]string{
				parameterVolumeID: "volume-id-chosen-by-attacker",
			},
			err: "parameter \"_id\" invalid in this context",
		},
		{
			name:   "invalid-parameter-create-internal",
			origin: createVolumeInternalParameters,
			stringmap: map[string]string{
				parameterEphemeral: "false",
			},
			err: "parameter \"csi.storage.k8s.io/ephemeral\" invalid in this context",
		},
		{
			name:   "invalid-ephemeral-context",
			origin: ephemeralVolumeParameters,
			stringmap: map[string]string{
				parameterCacheSize: gig,
			},
			err: "parameter \"cacheSize\" invalid in this context",
		},
		{
			name:   "invalid-persistent-context",
			origin: persistentVolumeParameters,
			stringmap: map[string]string{
				"foo": "bar",
			},
			err: "parameter \"foo\" invalid in this context",
		},
		{
			name:   "invalid-node-context",
			origin: nodeVolumeParameters,
			stringmap: map[string]string{
				parameterVolumeID: "volume-id",
			},
			err: "parameter \"_id\" invalid in this context",
		},

		// Parse errors for size.
		{
			name:   "invalid-size-suffix",
			origin: ephemeralVolumeParameters,
			stringmap: map[string]string{
				parameterSize: "1X",
			},
			err: "parameter \"size\": failed to parse \"1X\" as int64: quantities must match the regular expression '^([+-]?[0-9.]+)([eEinumkKMGTP]*[-+]?[0-9]*)$'",
		},
		{
			name:   "invalid-size-string",
			origin: ephemeralVolumeParameters,
			stringmap: map[string]string{
				parameterSize: "foo",
			},
			err: "parameter \"size\": failed to parse \"foo\" as int64: quantities must match the regular expression '^([+-]?[0-9.]+)([eEinumkKMGTP]*[-+]?[0-9]*)$'",
		},
	}
	for _, tt := range tests {
		tt := tt
		filteredMap := func() map[string]string {
			result := map[string]string{}
			for key, value := range tt.stringmap {
				if key == parameterSize {
					quantity := resource.MustParse(value)
					value = fmt.Sprintf("%d", quantity.Value())
				}
				if key != parameterVolumeID &&
					key != parameterProvisionerID &&
					!strings.HasPrefix(key, parameterPodInfoPrefix) {
					result[key] = value
				}
			}
			return result
		}

		t.Run(tt.name, func(t *testing.T) {
			parameters, err := parseVolumeParameters(tt.origin, tt.stringmap)
			switch {
			case tt.err == "":
				if assert.NoError(t, err, "no parse error") &&
					assert.Equal(t, tt.parameters, parameters) {
					stringmap := parameters.toVolumeContext()
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
