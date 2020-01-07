/*
Copyright 2019 Intel Corporation

SPDX-License-Identifier: Apache-2.0
*/

package pmemcsidriver

import (
	"fmt"
	"strconv"
	"strings"

	"k8s.io/apimachinery/pkg/api/resource"
)

type persistencyModel string
type parameterOrigin int

// Beware of API and backwards-compatibility breaking when changing these string constants!
const (
	parameterCacheSize        = "cacheSize"
	parameterEraseAfter       = "eraseafter"
	parameterName             = "name"
	parameterPersistencyModel = "persistencyModel"
	parameterVolumeID         = "_id"
	parameterSize             = "size"

	// Kubernetes v1.16+ adds this key to NodePublishRequest.VolumeContext
	// while provisioning ephemeral volume.
	parameterEphemeral = "csi.storage.k8s.io/ephemeral"

	// Additional, unknown parameters that are okay.
	parameterPodInfoPrefix = "csi.storage.k8s.io/"

	// Added by https://github.com/kubernetes-csi/external-provisioner/blob/feb67766f5e6af7db5c03ac0f0b16255f696c350/pkg/controller/controller.go#L584
	parameterProvisionerID = "storage.kubernetes.io/csiProvisionerIdentity"

	persistencyNormal    persistencyModel = "normal" // In releases <= 0.6.x this was called "none", but not documented.
	persistencyCache     persistencyModel = "cache"
	persistencyEphemeral persistencyModel = "ephemeral" // only used internally

	// Parameters are from storage class in controller CreateVolume.
	createVolumeParameters parameterOrigin = iota
	// Parameters are from master in node CreateVolume.
	createVolumeInternalParameters
	// Parameters are for an ephemeral volume in NodePublishVolume.
	ephemeralVolumeParameters
	// Parameters are for a persistent volume in NodePublishVolume.
	persistentVolumeParameters
	// Parameters as stored in node volume list.
	nodeVolumeParameters
)

// validParameters is a whitelist of which parameters are valid in which context.
var validParameters = map[parameterOrigin][]string{
	// Parameters from Kubernetes and users for a persistent volume.
	createVolumeParameters: []string{
		parameterCacheSize,
		parameterEraseAfter,
		parameterPersistencyModel,
	},

	// These parameters are prepared by the master controller.
	createVolumeInternalParameters: []string{
		parameterCacheSize,
		parameterEraseAfter,
		parameterPersistencyModel,

		parameterVolumeID,
	},

	// Parameters from Kubernetes and users.
	ephemeralVolumeParameters: []string{
		parameterEraseAfter,
		parameterPodInfoPrefix,
		parameterSize,
	},

	// The volume context prepared by CreateVolume. We replicate
	// the CreateVolume parameters in the context because a future
	// version of PMEM-CSI might need them (the current one
	// doesn't) and add the volume name for logging purposes.
	// Kubernetes adds pod info and provisioner ID.
	persistentVolumeParameters: []string{
		parameterCacheSize,
		parameterEraseAfter,
		parameterPersistencyModel,

		parameterName,
		parameterPodInfoPrefix,
		parameterProvisionerID,
	},

	// Internally we store everything except the volume ID,
	// which is handled separately.
	nodeVolumeParameters: []string{
		parameterCacheSize,
		parameterEraseAfter,
		parameterName,
		parameterPersistencyModel,
		parameterSize,
	},
}

// volumeParameters represents all settings for a volume.
// Values can be unset or set explicitly to some value.
// The accessor functions always return a value, if unset
// the default.
type volumeParameters struct {
	cacheSize   *uint
	eraseAfter  *bool
	name        *string
	persistency *persistencyModel
	size        *int64
	volumeID    *string
}

// volumeContext represents the same settings as a string map.
type volumeContext map[string]string

// parseVolumeParameters converts the string map that PMEM-CSI is given
// in CreateVolume (master and node) and NodePublishVolume. Depending
// on the origin of the string map, different keys are valid. An
// error is returned for invalid keys and values and invalid
// combinations of parameters.
func parseVolumeParameters(origin parameterOrigin, stringmap map[string]string) (volumeParameters, error) {
	var result volumeParameters
	validKeys := validParameters[origin]
	for key, value := range stringmap {
		valid := false
		for _, validKey := range validKeys {
			if validKey == key ||
				strings.HasPrefix(key, parameterPodInfoPrefix) && validKey == parameterPodInfoPrefix {
				valid = true
				break
			}
		}
		if !valid {
			return result, fmt.Errorf("parameter %q invalid in this context", key)
		}

		value := value // Ensure that we get a new instance in case that we take the address below.
		switch key {
		case parameterName:
			result.name = &value
		case parameterVolumeID:
			/* volume id provided by master controller (needed for cache volumes) */
			result.volumeID = &value
		case parameterPersistencyModel:
			p := persistencyModel(value)
			switch p {
			case persistencyNormal, persistencyCache:
				result.persistency = &p
			case persistencyEphemeral:
				if origin != nodeVolumeParameters {
					return result, fmt.Errorf("parameter %q: value invalid in this context: %q", key, value)
				}
				result.persistency = &p
			default:
				return result, fmt.Errorf("parameter %q: unknown value: %q", key, value)
			}
		case parameterCacheSize:
			c, err := strconv.ParseUint(value, 10, 32)
			if err != nil {
				return result, fmt.Errorf("parameter %q: failed to parse %q as uint: %v", key, value, err)
			}
			u := uint(c)
			result.cacheSize = &u
		case parameterSize:
			quantity, err := resource.ParseQuantity(value)
			if err != nil {
				return result, fmt.Errorf("parameter %q: failed to parse %q as int64: %v", key, value, err)
			}
			s := quantity.Value()
			result.size = &s
		case parameterEraseAfter:
			b, err := strconv.ParseBool(value)
			if err != nil {
				return result, fmt.Errorf("parameter %q: failed to parse %q as boolean: %v", key, value, err)
			}
			result.eraseAfter = &b
		case parameterEphemeral:
			b, err := strconv.ParseBool(value)
			if err != nil {
				return result, fmt.Errorf("parameter %q: failed to parse %q as boolean: %v", key, value, err)
			}
			if b {
				p := persistencyEphemeral
				result.persistency = &p
			}
		case parameterProvisionerID:
		default:
			if !strings.HasPrefix(key, parameterPodInfoPrefix) {
				return result, fmt.Errorf("unknown parameter: %q", key)
			}
		}
	}

	// Some sanity checks.
	if result.cacheSize != nil && result.getPersistency() != persistencyCache {
		return result, fmt.Errorf("parameter %q: invalid for %q = %q", parameterCacheSize, parameterPersistencyModel, result.getPersistency())
	}
	if origin == ephemeralVolumeParameters && result.size == nil {
		return result, fmt.Errorf("required parameter %q not specified", parameterSize)
	}

	return result, nil
}

// toVolumeContext converts back to a string map for use in
// CreateVolumeResponse.Volume.VolumeContext and for storing in the
// node's volume list.
//
// Both the volume context and the volume list are persisted outside
// of PMEM-CSI (one in etcd, the other on disk), so beware when making
// backwards incompatible changes!
func (p volumeParameters) toVolumeContext() map[string]string {
	result := map[string]string{}

	// Intentionally not stored:
	// - volumeID

	if p.cacheSize != nil {
		result[parameterCacheSize] = fmt.Sprintf("%d", *p.cacheSize)
	}
	if p.eraseAfter != nil {
		result[parameterEraseAfter] = fmt.Sprintf("%v", *p.eraseAfter)
	}
	if p.name != nil {
		result[parameterName] = *p.name
	}
	if p.persistency != nil {
		result[parameterPersistencyModel] = string(*p.persistency)
	}
	if p.size != nil {
		result[parameterSize] = fmt.Sprintf("%d", *p.size)
	}

	return result
}

func (p volumeParameters) getCacheSize() uint {
	if p.cacheSize != nil {
		return *p.cacheSize
	}
	return 1
}

func (p volumeParameters) getEraseAfter() bool {
	if p.eraseAfter != nil {
		return *p.eraseAfter
	}
	return true
}

func (p volumeParameters) getPersistency() persistencyModel {
	if p.persistency != nil {
		return *p.persistency
	}
	return persistencyNormal
}

func (p volumeParameters) getName() string {
	if p.name != nil {
		return *p.name
	}
	return ""
}

func (p volumeParameters) getSize() int64 {
	if p.size != nil {
		return *p.size
	}
	return 0
}

func (p volumeParameters) getVolumeID() string {
	if p.volumeID != nil {
		return *p.volumeID
	}
	return ""
}
