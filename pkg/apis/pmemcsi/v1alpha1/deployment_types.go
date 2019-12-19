package v1alpha1

import (
	corev1 "k8s.io/api/core/v1"
	apiextensions "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
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
	// Important: Run "operator-sdk generate k8s" to regenerate code after modifying this file
	// Add custom validation using kubebuilder tags: https://book-v1.book.kubebuilder.io/beyond_basics/generating_crd.html

	//DriverName represents the name of the PMEM-CSI driver to be deployed
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
}

// DeploymentStatus defines the observed state of Deployment
type DeploymentStatus struct {
	// INSERT ADDITIONAL STATUS FIELD - define observed state of cluster
	// Important: Run "operator-sdk generate k8s" to regenerate code after modifying this file
	// Add custom validation using kubebuilder tags: https://book-v1.book.kubebuilder.io/beyond_basics/generating_crd.html
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
