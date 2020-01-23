/*
Copyright 2020 The Kubernetes Authors.

SPDX-License-Identifier: Apache-2.0
*/

package v1alpha1

import (
	"reflect"

	corev1 "k8s.io/api/core/v1"
	apiextensions "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
)

// DeviceMode type decleration for allowed driver device managers
type DeviceMode string

const (
	// DeviceModeLVM represents 'lvm' device manager
	DeviceModeLVM DeviceMode = "lvm"
	// DeviceModeDirect represents 'direct' device manager
	DeviceModeDirect DeviceMode = "direct"
)

// DeploymentSpec defines the desired state of Deployment
type DeploymentSpec struct {
	// INSERT ADDITIONAL SPEC FIELDS - desired state of cluster
	// Important: Run "make operator-generate-k8s" to regenerate code after modifying this file

	// DriverName represents the name of the PMEM-CSI driver to be deployed
	DriverName string `json:"driverName,omitempty"`
	// Image holds container image options
	Image string `json:"image,omitempty"`
	// PullPolicy image pull policy one of Always, Never, IfNotPresent
	PullPolicy corev1.PullPolicy `json:"imagePullPolicy,omitempty"`
	// ProvisionerImage CSI provisioner sidecar image
	ProvisionerImage string `json:"provisionerImage,omitempty"`
	// NodeRegistrarImage CSI node driver registrar sidecar image
	NodeRegistrarImage string `json:"nodeRegistrarImage,omitempty"`
	// ControllerResources Compute resources required by Controller driver
	ControllerResources *corev1.ResourceRequirements `json:"controllerResources,omitempty"`
	// NodeResources Compute resources required by Node driver
	NodeResources *corev1.ResourceRequirements `json:"nodeResources,omitempty"`
	// DeviceMode to use to manage PMEM devices. One of lvm, direct
	DeviceMode DeviceMode `json:"deviceMode,omitempty"`
	// LogLevel number for the log verbosity
	LogLevel uint16 `json:"logLevel,omitempty"`
	// RegistryCert encoded certificate signed by a CA for registry server authentication
	// If not provided, provisioned one by the operator using self-signed CA
	RegistryCert []byte `json:"registryCert,omitempty"`
	// RegistryPrivateKey encoded private key used for registry server certificate
	// If not provided, provisioned one by the operator
	RegistryPrivateKey []byte `json:"registryKey,omitempty"`
	// NodeControllerCert encoded certificate signed by a CA for node controller server authentication
	// If not provided, provisioned one by the operator using self-signed CA
	NodeControllerCert []byte `json:"nodeControllerCert,omitempty"`
	// NodeControllerPrivateKey encoded private key used for node controller server certificate
	// If not provided, provisioned one by the operator
	NodeControllerPrivateKey []byte `json:"nodeControllerKey,omitempty"`
	// CACert encoded root certificate of the CA by which the registry and node controller certificates are signed
	// If not provided operator uses a self-signed CA certificate
	CACert []byte `json:"caCert,omitempty"`
	// NodeSelector node labels to use for selection of driver node
	NodeSelector map[string]string `json:"nodeSelector,omitempty"`
}

// DeploymentStatus defines the observed state of Deployment
type DeploymentStatus struct {
	// INSERT ADDITIONAL STATUS FIELD - define observed state of cluster
	// Important: Run "make operator-generate-k8s" to regenerate code after modifying this file

	// Phase indicates the state of the deployment
	Phase DeploymentPhase `json:"phase,omitempty"`
}

// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object

// Deployment is the Schema for the deployments API
// +kubebuilder:subresource:status
// +kubebuilder:resource:path=deployments,scope=Namespaced
type Deployment struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   DeploymentSpec   `json:"spec,omitempty"`
	Status DeploymentStatus `json:"status,omitempty"`
}

// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object

// DeploymentList contains a list of Deployment
type DeploymentList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []Deployment `json:"items"`
}

func init() {
	SchemeBuilder.Register(&Deployment{}, &DeploymentList{})
}

const (
	// DefaultDriverName default driver name to be used for a deployment
	DefaultDriverName = "pmem-csi.intel.com"
	// DefaultLogLevel default logging level used for the driver
	DefaultLogLevel = uint16(5)
	// DefaultImagePullPolicy default image pull policy for all the images used by the deployment
	DefaultImagePullPolicy = corev1.PullIfNotPresent

	defaultDriverImageName = "intel/pmem-csi-driver"
	defaultDriverImageTag  = "canary"
	// DefaultDriverImage default PMEM-CSI driver docker image
	DefaultDriverImage = defaultDriverImageName + ":" + defaultDriverImageTag

	defaultProvisionerImageName = "quay.io/k8scsi/csi-provisioner"
	defaultProvisionerImageTag  = "v1.2.1"
	// DefaultProvisionerImage default external provisioner image to use
	DefaultProvisionerImage = defaultProvisionerImageName + ":" + defaultProvisionerImageTag

	defaultRegistrarImageName = "quay.io/k8scsi/csi-node-driver-registrar"
	defaultRegistrarImageTag  = "v1.1.0"
	// DefaultRegistrarImage default node driver registrar image to use
	DefaultRegistrarImage = defaultRegistrarImageName + ":" + defaultRegistrarImageTag

	// DefaultControllerResourceCPU default CPU resource limit used for controller pod
	DefaultControllerResourceCPU = "100m" // MilliSeconds
	// DefaultControllerResourceMemory default memory resource limit used for controller pod
	DefaultControllerResourceMemory = "250Mi" // MB
	// DefaultNodeResourceCPU default CPU resource limit used for node driver pod
	DefaultNodeResourceCPU = "100m" // MilliSeconds
	// DefaultNodeResourceMemory default memory resource limit used for node driver pod
	DefaultNodeResourceMemory = "250Mi" // MB
	// DefaultDeviceMode default device manger used for deployment
	DefaultDeviceMode = DeviceModeLVM
)

var (
	// DefaultNodeSelector default node label used for node selection
	DefaultNodeSelector = map[string]string{"storage": "pmem"}
)

// DeploymentPhase represents the status phase of a driver deployment
type DeploymentPhase string

const (
	// DeploymentPhaseNew indicates a new deployment
	DeploymentPhaseNew DeploymentPhase = ""
	// DeploymentPhaseInitializing indicates deployment initialization is in progress
	DeploymentPhaseInitializing DeploymentPhase = "Initializing"
	// DeploymentPhaseRunning indicates that the deployment was successful
	DeploymentPhaseRunning DeploymentPhase = "Running"
	// DeploymentPhaseFailed indicates that the deployment was failed
	DeploymentPhaseFailed DeploymentPhase = "Failed"
)

// DeploymentChange type declaration for changes between two deployments
type DeploymentChange int

const (
	DriverName DeploymentChange = iota + 1
	DriverMode
	DriverImage
	PullPolicy
	LogLevel
	ProvisionerImage
	NodeRegistrarImage
	ControllerResources
	NodeResources
	NodeSelector
)

func (c DeploymentChange) String() string {
	return map[DeploymentChange]string{
		DriverName:          "driverName",
		DriverMode:          "deviceMode",
		DriverImage:         "image",
		PullPolicy:          "imagePullPolicy",
		LogLevel:            "logLevel",
		ProvisionerImage:    "provisionerImage",
		NodeRegistrarImage:  "nodeRegistrarImage",
		ControllerResources: "controllerResources",
		NodeResources:       "nodeResources",
		NodeSelector:        "nodeSelector",
	}[c]
}

// EnsureDefaults make sure that the deployment object has all defaults set properly
func (d *Deployment) EnsureDefaults() {
	if d.Spec.DriverName == "" {
		d.Spec.DriverName = DefaultDriverName
	}
	if d.Spec.Image == "" {
		d.Spec.Image = DefaultDriverImage
	}
	if d.Spec.PullPolicy == "" {
		d.Spec.PullPolicy = DefaultImagePullPolicy
	}
	if d.Spec.LogLevel == 0 {
		d.Spec.LogLevel = DefaultLogLevel
	}

	/* Controller Defaults */

	if d.Spec.ProvisionerImage == "" {
		d.Spec.ProvisionerImage = DefaultProvisionerImage
	}

	if d.Spec.ControllerResources == nil {
		d.Spec.ControllerResources = &corev1.ResourceRequirements{
			Limits: corev1.ResourceList{
				corev1.ResourceCPU:    resource.MustParse(DefaultControllerResourceCPU),
				corev1.ResourceMemory: resource.MustParse(DefaultControllerResourceMemory),
			},
		}
	}

	/* Node Defaults */

	if d.Spec.DeviceMode == "" {
		d.Spec.DeviceMode = DefaultDeviceMode
	}

	if d.Spec.NodeRegistrarImage == "" {
		d.Spec.NodeRegistrarImage = DefaultRegistrarImage
	}

	if d.Spec.NodeResources == nil {
		d.Spec.NodeResources = &corev1.ResourceRequirements{
			Limits: corev1.ResourceList{
				corev1.ResourceCPU:    resource.MustParse(DefaultNodeResourceCPU),
				corev1.ResourceMemory: resource.MustParse(DefaultNodeResourceMemory),
			},
		}
	}

	if d.Spec.NodeSelector == nil {
		d.Spec.NodeSelector = DefaultNodeSelector
	}
}

// Compare compares 'other' deployment spec with current deployment and returns
// the all the changes. If len(changes) == 0 represents both deployment spec
// are equivalent.
func (d *Deployment) Compare(other *Deployment) map[DeploymentChange]struct{} {
	changes := map[DeploymentChange]struct{}{}
	if d == nil || other == nil {
		return changes
	}

	if d.Spec.DriverName != other.Spec.DriverName {
		changes[DriverName] = struct{}{}
	}
	if d.Spec.DeviceMode != other.Spec.DeviceMode {
		changes[DriverMode] = struct{}{}
	}
	if d.Spec.Image != other.Spec.Image {
		changes[DriverImage] = struct{}{}
	}
	if d.Spec.PullPolicy != other.Spec.PullPolicy {
		changes[PullPolicy] = struct{}{}
	}
	if d.Spec.LogLevel != other.Spec.LogLevel {
		changes[LogLevel] = struct{}{}
	}
	if d.Spec.ProvisionerImage != other.Spec.ProvisionerImage {
		changes[ProvisionerImage] = struct{}{}
	}
	if d.Spec.NodeRegistrarImage != other.Spec.NodeRegistrarImage {
		changes[NodeRegistrarImage] = struct{}{}
	}
	if !compareResources(d.Spec.ControllerResources, other.Spec.ControllerResources) {
		changes[ControllerResources] = struct{}{}
	}
	if !compareResources(d.Spec.NodeResources, other.Spec.NodeResources) {
		changes[NodeResources] = struct{}{}
	}

	if !reflect.DeepEqual(d.Spec.NodeSelector, other.Spec.NodeSelector) {
		changes[NodeSelector] = struct{}{}
	}

	return changes
}

func (d *Deployment) GetNamespacedName() types.NamespacedName {
	return types.NamespacedName{
		Name:      d.Name,
		Namespace: d.Namespace,
	}
}

func GetDeploymentCRDSchema() *apiextensions.JSONSchemaProps {
	return &apiextensions.JSONSchemaProps{
		Type:        "object",
		Description: "https://github.com/intel/pmem-csi.git",
		Properties: map[string]apiextensions.JSONSchemaProps{
			"spec": apiextensions.JSONSchemaProps{
				Type:        "object",
				Description: "DeploymentSpec defines the desired state of Deployment",
				Properties: map[string]apiextensions.JSONSchemaProps{
					"driverName": apiextensions.JSONSchemaProps{
						Type:        "string",
						Description: "CSI Driver name",
					},
					"logLevel": apiextensions.JSONSchemaProps{
						Type:        "integer",
						Description: "logging level",
					},
					"deviceMode": apiextensions.JSONSchemaProps{
						Type:        "string",
						Description: "CSI Driver mode for device management: 'lvm' or 'direct'",
						Enum: []apiextensions.JSON{
							apiextensions.JSON{Raw: []byte("\"" + DeviceModeLVM + "\"")},
							apiextensions.JSON{Raw: []byte("\"" + DeviceModeDirect + "\"")},
						},
					},
					"image": apiextensions.JSONSchemaProps{
						Type:        "string",
						Description: "PMEM-CSI driver docker image",
					},
					"provisionerImage": apiextensions.JSONSchemaProps{
						Type:        "string",
						Description: "CSI provisioner docker image",
					},
					"nodeRegistrarImage": apiextensions.JSONSchemaProps{
						Type:        "string",
						Description: "CSI node driver registrar docker image",
					},
					"imagePullPolicy": apiextensions.JSONSchemaProps{
						Type:        "string",
						Description: "Docker image pull policy: Always, Never, IfNotPresent",
						Enum: []apiextensions.JSON{
							apiextensions.JSON{Raw: []byte("\"" + corev1.PullAlways + "\"")},
							apiextensions.JSON{Raw: []byte("\"" + corev1.PullIfNotPresent + "\"")},
							apiextensions.JSON{Raw: []byte("\"" + corev1.PullNever + "\"")},
						},
					},
					"controllerResources": getResourceRequestsSchema(),
					"nodeResources":       getResourceRequestsSchema(),
					"caCert": apiextensions.JSONSchemaProps{
						Type:        "string",
						Description: "Encoded CA certificate",
					},
					"registryCert": apiextensions.JSONSchemaProps{
						Type:        "string",
						Description: "Encoded pmem-registry certificate",
					},
					"registryKey": apiextensions.JSONSchemaProps{
						Type:        "string",
						Description: "Encoded private key used for generating pmem-registry certificate",
					},
					"nodeControllerCert": apiextensions.JSONSchemaProps{
						Type:        "string",
						Description: "Encoded pmem-node-controller certificate",
					},
					"nodeControllerKey": apiextensions.JSONSchemaProps{
						Type:        "string",
						Description: "Encoded private key used for generating pmem-node-controller certificate",
					},
					"nodeSelector": apiextensions.JSONSchemaProps{
						Type:        "object",
						Description: "Set of node labels to use to select a node to run PMEM-CSI driver",
						AdditionalProperties: &apiextensions.JSONSchemaPropsOrBool{
							Allows: true,
							Schema: &apiextensions.JSONSchemaProps{
								Type: "string",
							},
						},
					},
				},
			},
			"status": apiextensions.JSONSchemaProps{
				Type:        "object",
				Description: "State of the deployment",
				Properties: map[string]apiextensions.JSONSchemaProps{
					"phase": apiextensions.JSONSchemaProps{
						Type:        "string",
						Description: "deployment phase",
					},
				},
			},
		},
	}
}

func getResourceRequestsSchema() apiextensions.JSONSchemaProps {
	return apiextensions.JSONSchemaProps{
		Type:        "object",
		Description: "Compute resource requirements for controller driver Pod",
		Properties: map[string]apiextensions.JSONSchemaProps{
			"limits": apiextensions.JSONSchemaProps{
				Type:        "object",
				Description: "The maximum amount of compute resources allowed",
				AdditionalProperties: &apiextensions.JSONSchemaPropsOrBool{
					Allows: true,
					Schema: &apiextensions.JSONSchemaProps{
						Type: "string",
					},
				},
			},
			"requests": apiextensions.JSONSchemaProps{
				Type:        "object",
				Description: "The minimum amount of compute resources required",
				AdditionalProperties: &apiextensions.JSONSchemaPropsOrBool{
					Allows: true,
					Schema: &apiextensions.JSONSchemaProps{
						Type: "string",
					},
				},
			},
		},
	}
}

func compareResources(rsA *corev1.ResourceRequirements, rsB *corev1.ResourceRequirements) bool {
	if rsA == nil {
		return rsB == nil
	}
	if rsB == nil {
		return false
	}
	if rsA == nil && rsB != nil {
		return false
	}
	if !rsA.Limits.Cpu().Equal(*rsB.Limits.Cpu()) ||
		!rsA.Limits.Memory().Equal(*rsB.Limits.Memory()) ||
		!rsA.Requests.Cpu().Equal(*rsB.Requests.Cpu()) ||
		!rsA.Requests.Memory().Equal(*rsB.Requests.Memory()) {
		return false
	}

	return true
}
