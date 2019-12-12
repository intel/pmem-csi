package v1alpha1

import (
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

type DeviceMode string

const (
	DeviceModeLVM    DeviceMode = "lvm"
	DeviceModeDirect DeviceMode = "direct"
)

// ControllerDriver options used for driver running at Master as controller
type ControllerDriver struct {
	// ProvisionerImage CSI provisioner sidecar image
	ProvisionerImage ImageInfo `json:"provisionerImage,omitempty"`
	// Resources Compute resources required by Controller driver
	Resources *corev1.ResourceRequirements `json:"resources,omitempty"`
}

// NodeDriver options used for driver running at Node
type NodeDriver struct {
	// RegistrarImage CSI node driver registrar sidecar image
	RegistrarImage ImageInfo `json:"registrarImage,omitempty"`
	// Resources Compute resources required by Node driver
	Resources *corev1.ResourceRequirements `json:"resources,omitempty"`
	// DeviceMode to use to manage PMEM devices. One of lvm, direct
	DeviceMode DeviceMode `json:"deviceMode,omitempty"`
}

// ImageInfo defines the PMEM-CSI driver container image details
type ImageInfo struct {
	// Registry docker registry to use
	Registry string `json:"registry,omitempty"`
	// Name container image name to use, defaults to pmem-csi-driver
	Name string `json:"name,omitempty"`
	// Tag image tag to use
	Tag string `json:"tag,omitempty"`
	// PullPolicy image pull policy one of Always, Never, IfNotPresent
	PullPolicy corev1.PullPolicy `json:"pullPolicy,omitempty"`
}

func (ii ImageInfo) String() string {
	image := ""
	if ii.Registry != "" {
		image = ii.Registry + "/"
	}

	return image + ii.Name + ":" + ii.Tag
}

// DeploymentSpec defines the desired state of Deployment
type DeploymentSpec struct {
	// INSERT ADDITIONAL SPEC FIELDS - desired state of cluster
	// Important: Run "operator-sdk generate k8s" to regenerate code after modifying this file
	// Add custom validation using kubebuilder tags: https://book-v1.book.kubebuilder.io/beyond_basics/generating_crd.html

	// Image holds container image options
	Image ImageInfo `json:"image,omitempty"`
	// Controller holds configuration options for driver running as controller
	Controller ControllerDriver `json:"controller,omitempty"`
	// Controller hodls configuration options for driver running on compute nodes
	Node NodeDriver `json:"node,omitempty"`
	// LogLevel number for the log verbosity
	LogLevel uint16 `json:"logLevel,omitempty"`
	// Namespace in which the new deployment should deploy
	Namespace string `json:"namespace,omitempty"`
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
	defaultLogLevel        = uint16(5)
	defaultImagePullPolicy = corev1.PullIfNotPresent

	defaultDriverImageName = "intel/pmem-csi-driver"
	defaultDriverImageTag  = "canary"

	defaultProvisionerImageRegistry = "quay.io"
	defaultProvisionerImageName     = "k8scsi/csi-provisioner"
	defaultProvisionerImageTag      = "v1.2.1"

	defaultRegistrarImageRegistry = "quay.io"
	defaultRegistrarImageName     = "k8scsi/csi-node-driver-registrar"
	defaultRegistrarImageTag      = "v1.1.0"

	defaultControllerResourceCPU    = int64(100) // MilliSeconds
	defaultControllerResourceMemory = int64(250) // MB
	defaultNodeResourceCPU          = int64(100) // MilliSeconds
	defaultNodeResourceMemory       = int64(250) // MB
	defaultNodeDeviceMode           = DeviceModeLVM
)

// EnsureDefaults make sure that the deployment object has all defaults set properly
func (d *Deployment) EnsureDefaults() {
	if d.Spec.Image.Name == "" {
		d.Spec.Image.Name = defaultDriverImageName
	}
	if d.Spec.Image.Tag == "" {
		d.Spec.Image.Tag = defaultDriverImageTag
	}
	if d.Spec.Image.PullPolicy == "" {
		d.Spec.Image.PullPolicy = defaultImagePullPolicy
	}
	if d.Spec.LogLevel == 0 {
		d.Spec.LogLevel = defaultLogLevel
	}

	/* Controller Defaults */

	if d.Spec.Controller.ProvisionerImage.Registry == "" {
		d.Spec.Controller.ProvisionerImage.Registry = defaultProvisionerImageRegistry
	}
	if d.Spec.Controller.ProvisionerImage.Name == "" {
		d.Spec.Controller.ProvisionerImage.Name = defaultProvisionerImageName
	}
	if d.Spec.Controller.ProvisionerImage.Tag == "" {
		d.Spec.Controller.ProvisionerImage.Tag = defaultProvisionerImageTag
	}
	if d.Spec.Controller.ProvisionerImage.PullPolicy == "" {
		d.Spec.Controller.ProvisionerImage.PullPolicy = defaultImagePullPolicy
	}

	if d.Spec.Controller.Resources == nil {
		d.Spec.Controller.Resources = &corev1.ResourceRequirements{
			Limits: corev1.ResourceList{
				corev1.ResourceCPU:    *resource.NewMilliQuantity(defaultControllerResourceCPU, resource.DecimalSI),
				corev1.ResourceMemory: *resource.NewQuantity(defaultControllerResourceMemory, resource.BinarySI),
			},
		}
	}

	/* Node Defaults */

	if d.Spec.Node.DeviceMode == "" {
		d.Spec.Node.DeviceMode = defaultNodeDeviceMode
	}

	if d.Spec.Node.RegistrarImage.Registry == "" {
		d.Spec.Node.RegistrarImage.Registry = defaultRegistrarImageRegistry
	}
	if d.Spec.Node.RegistrarImage.Name == "" {
		d.Spec.Node.RegistrarImage.Name = defaultRegistrarImageName
	}
	if d.Spec.Node.RegistrarImage.Tag == "" {
		d.Spec.Node.RegistrarImage.Tag = defaultRegistrarImageTag
	}
	if d.Spec.Node.RegistrarImage.PullPolicy == "" {
		d.Spec.Node.RegistrarImage.PullPolicy = defaultImagePullPolicy
	}

	if d.Spec.Node.Resources == nil {
		d.Spec.Controller.Resources = &corev1.ResourceRequirements{
			Limits: corev1.ResourceList{
				corev1.ResourceCPU:    *resource.NewMilliQuantity(defaultNodeResourceCPU, resource.DecimalSI),
				corev1.ResourceMemory: *resource.NewQuantity(defaultNodeResourceMemory, resource.BinarySI),
			},
		}
	}
}
