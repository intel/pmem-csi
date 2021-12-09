/*
Copyright 2019,2020 Intel Corporation

SPDX-License-Identifier: Apache-2.0
*/

package parameters

import (
	"fmt"
	"strconv"
	"strings"

	api "github.com/intel/pmem-csi/pkg/apis/pmemcsi/v1beta1"
	"k8s.io/apimachinery/pkg/api/resource"
)

type Persistency string
type Origin int
type Usage string

// Beware of API and backwards-compatibility breaking when changing these string constants!
const (
	EraseAfter       = "eraseafter"
	KataContainers   = "kataContainers"
	Name             = "name"
	PersistencyModel = "persistencyModel"
	Size             = "size"
	DeviceMode       = "deviceMode"

	// Added in PMEM-CSI 1.1.0.
	UsageModel           = "usage"
	UsageAppDirect Usage = "AppDirect"
	UsageFileIO    Usage = "FileIO"

	// Kubernetes v1.16+ adds this key to NodePublishRequest.VolumeContext
	// while provisioning ephemeral volume.
	Ephemeral = "csi.storage.k8s.io/ephemeral"

	// Additional, unknown parameters that are okay.
	PodInfoPrefix = "csi.storage.k8s.io/"

	// Added by https://github.com/kubernetes-csi/external-provisioner/blob/feb67766f5e6af7db5c03ac0f0b16255f696c350/pkg/controller/controller.go#L584
	ProvisionerID = "storage.kubernetes.io/csiProvisionerIdentity"

	PersistencyNormal    Persistency = "normal"    // In releases <= 0.6.x this was called "none", but not documented.
	PersistencyEphemeral Persistency = "ephemeral" // only used internally

	//CreateVolumeOrigin is for parameters from the storage class in controller CreateVolume.
	CreateVolumeOrigin Origin = iota
	// CreateVolumeInternalOrigin is for the node CreateVolume parameters.
	CreateVolumeInternalOrigin
	// EphemeralVolumeOrigin represents parameters for an ephemeral volume in NodePublishVolume.
	EphemeralVolumeOrigin
	// PersistentVolumeOrigin represents parameters for a persistent volume in NodePublishVolume.
	PersistentVolumeOrigin
	// NodeVolumeOrigin is for the parameters stored in node volume list.
	NodeVolumeOrigin
)

// valid is a whitelist of which parameters are valid in which context.
var valid = map[Origin][]string{
	// Parameters from Kubernetes and users for a persistent volume.
	CreateVolumeOrigin: []string{
		EraseAfter,
		KataContainers,
		UsageModel,
		PersistencyModel,
	},

	// Parameters from Kubernetes and users.
	EphemeralVolumeOrigin: []string{
		EraseAfter,
		KataContainers,
		UsageModel,
		PodInfoPrefix,
		Size,
	},

	// The volume context prepared by CreateVolume. We replicate
	// the CreateVolume parameters in the context because a future
	// version of PMEM-CSI might need them (the current one
	// doesn't) and add the volume name for logging purposes.
	// Kubernetes adds pod info and provisioner ID.
	PersistentVolumeOrigin: []string{
		EraseAfter,
		KataContainers,
		PersistencyModel,
		UsageModel,

		Name,
		PodInfoPrefix,
		ProvisionerID,
	},

	// Internally we store everything except the volume ID,
	// which is handled separately.
	NodeVolumeOrigin: []string{
		EraseAfter,
		KataContainers,
		UsageModel,
		Name,
		PersistencyModel,
		Size,
		DeviceMode,
	},
}

// Volume represents all settings for a volume.
// Values can be unset or set explicitly to some value.
// The accessor functions always return a value, if unset
// the default.
type Volume struct {
	EraseAfter     *bool
	KataContainers *bool
	Name           *string
	Persistency    *Persistency
	Size           *int64
	DeviceMode     *api.DeviceMode
	Usage          *Usage
}

// VolumeContext represents the same settings as a string map.
type VolumeContext map[string]string

// Parse converts the string map that PMEM-CSI is given
// in CreateVolume (master and node) and NodePublishVolume. Depending
// on the origin of the string map, different keys are valid. An
// error is returned for invalid keys and values and invalid
// combinations of parameters.
func Parse(origin Origin, stringmap map[string]string) (Volume, error) {
	var result Volume
	validKeys := valid[origin]
	for key, value := range stringmap {
		valid := false
		for _, validKey := range validKeys {
			if validKey == key ||
				strings.HasPrefix(key, PodInfoPrefix) && validKey == PodInfoPrefix {
				valid = true
				break
			}
		}
		if !valid {
			return result, fmt.Errorf("parameter %q invalid in this context", key)
		}

		value := value // Ensure that we get a new instance in case that we take the address below.
		switch key {
		case Name:
			result.Name = &value
		case PersistencyModel:
			p := Persistency(value)
			switch p {
			case PersistencyNormal:
				result.Persistency = &p
			case PersistencyEphemeral:
				if origin != NodeVolumeOrigin {
					return result, fmt.Errorf("parameter %q: value invalid in this context: %q", key, value)
				}
				result.Persistency = &p
			case "none":
				// Legacy alias from PMEM-CSI <= 0.5.0.
				p := PersistencyNormal
				result.Persistency = &p
			default:
				return result, fmt.Errorf("parameter %q: unknown value: %q", key, value)
			}
		case KataContainers:
			b, err := strconv.ParseBool(value)
			if err != nil {
				return result, fmt.Errorf("parameter %q: failed to parse %q as boolean: %v", key, value, err)
			}
			result.KataContainers = &b
		case UsageModel:
			u := Usage(value)
			switch u {
			case UsageAppDirect, UsageFileIO:
				result.Usage = &u
			case "":
			default:
				return result, fmt.Errorf("parameter %q: unknown value: %s", key, value)
			}
		case Size:
			quantity, err := resource.ParseQuantity(value)
			if err != nil {
				return result, fmt.Errorf("parameter %q: failed to parse %q as int64: %v", key, value, err)
			}
			s := quantity.Value()
			result.Size = &s
		case EraseAfter:
			b, err := strconv.ParseBool(value)
			if err != nil {
				return result, fmt.Errorf("parameter %q: failed to parse %q as boolean: %v", key, value, err)
			}
			result.EraseAfter = &b
		case Ephemeral:
			b, err := strconv.ParseBool(value)
			if err != nil {
				return result, fmt.Errorf("parameter %q: failed to parse %q as boolean: %v", key, value, err)
			}
			if b {
				p := PersistencyEphemeral
				result.Persistency = &p
			}
		case DeviceMode:
			var mode api.DeviceMode
			if err := mode.Set(value); err != nil {
				return result, fmt.Errorf("parameter %q: failed to parse %q as DeviceMode: %v", key, value, err)
			}
			result.DeviceMode = &mode
		case ProvisionerID:
		default:
			if !strings.HasPrefix(key, PodInfoPrefix) {
				return result, fmt.Errorf("unknown parameter: %q", key)
			}
		}
	}

	// Some sanity checks.
	if origin == EphemeralVolumeOrigin && result.Size == nil {
		return result, fmt.Errorf("required parameter %q not specified", Size)
	}

	if result.GetKataContainers() && result.GetUsage() != UsageAppDirect {
		return result, fmt.Errorf("Kata Container support and usage %q are mutually exclusive", result.GetUsage())
	}

	return result, nil
}

// ToContext converts back to a string map for use in
// CreateVolumeResponse.Volume.VolumeContext and for storing in the
// node's volume list.
//
// Both the volume context and the volume list are persisted outside
// of PMEM-CSI (one in etcd, the other on disk), so beware when making
// backwards incompatible changes!
func (v Volume) ToContext() VolumeContext {
	result := VolumeContext{}

	// Intentionally not stored:
	// - volumeID

	if v.EraseAfter != nil {
		result[EraseAfter] = fmt.Sprintf("%v", *v.EraseAfter)
	}
	if v.Name != nil {
		result[Name] = *v.Name
	}
	if v.Persistency != nil {
		result[PersistencyModel] = string(*v.Persistency)
	}
	if v.Size != nil {
		result[Size] = fmt.Sprintf("%d", *v.Size)
	}
	if v.KataContainers != nil {
		result[KataContainers] = fmt.Sprintf("%v", *v.KataContainers)
	}
	if v.DeviceMode != nil {
		result[DeviceMode] = string(*v.DeviceMode)
	}
	if v.Usage != nil {
		result[UsageModel] = string(*v.Usage)
	}

	return result
}

func (v Volume) GetEraseAfter() bool {
	if v.EraseAfter != nil {
		return *v.EraseAfter
	}
	return true
}

func (v Volume) GetPersistency() Persistency {
	if v.Persistency != nil {
		return *v.Persistency
	}
	return PersistencyNormal
}

func (v Volume) GetName() string {
	if v.Name != nil {
		return *v.Name
	}
	return ""
}

func (v Volume) GetSize() int64 {
	if v.Size != nil {
		return *v.Size
	}
	return 0
}

func (v Volume) GetKataContainers() bool {
	if v.KataContainers != nil {
		return *v.KataContainers
	}
	return false
}

func (v Volume) GetDeviceMode() api.DeviceMode {
	if v.DeviceMode != nil {
		return *v.DeviceMode
	}

	return ""
}

func (v Volume) GetUsage() Usage {
	if v.Usage != nil {
		return *v.Usage
	}
	return UsageAppDirect
}
