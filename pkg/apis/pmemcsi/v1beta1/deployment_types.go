/*
Copyright 2020 The Kubernetes Authors.

SPDX-License-Identifier: Apache-2.0
*/

package v1beta1

import (
	"errors"
	"fmt"
	"strings"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// DeviceMode type decleration for allowed driver device managers
type DeviceMode string

// Set sets the value
func (mode *DeviceMode) Set(value string) error {
	switch value {
	case string(DeviceModeLVM), string(DeviceModeDirect), string(DeviceModeFake):
		*mode = DeviceMode(value)
	case "ndctl":
		// For backwards-compatibility.
		*mode = DeviceModeDirect
	default:
		return errors.New("invalid device manager mode")
	}
	return nil
}

func (mode *DeviceMode) String() string {
	return string(*mode)
}

// +kubebuilder:validation:Enum=lvm,direct
const (
	// DeviceModeLVM represents 'lvm' device manager
	DeviceModeLVM DeviceMode = "lvm"
	// DeviceModeDirect represents 'direct' device manager
	DeviceModeDirect DeviceMode = "direct"
	// DeviceModeFake represents a device manager for testing:
	// volume creation and deletion is just recorded in memory,
	// without any actual backing store. Such fake volumes cannot
	// be used for pods.
	DeviceModeFake DeviceMode = "fake"
)

type LogFormat string

const (
	// LogFormatText selects logging via the traditional glog (aka klog) plain text format.
	LogFormatText LogFormat = "text"
	// LogFormatJSON selects logging via the zap JSON format.
	LogFormatJSON LogFormat = "json"
)

type MutatePods string

const (
	// MutatePodsAlways enables the mutating pod webhook so that a failure is considered fatal.
	MutatePodsAlways MutatePods = "Always"

	// MutatePodsTry enables the mutating pod webhook so that it a pod can be created even
	// when the webhook fails.
	MutatePodsTry MutatePods = "Try"

	// MutatePodsNever disables the mutating pod webhook.
	MutatePodsNever MutatePods = "Never"
)

// +k8s:deepcopy-gen=true
// DeploymentSpec defines the desired state of Deployment
type DeploymentSpec struct {
	// Important: Run "make operator-generate-k8s" to regenerate code after modifying this file

	// PMEM-CSI driver container image
	Image string `json:"image,omitempty"`
	// PullPolicy image pull policy one of Always, Never, IfNotPresent
	PullPolicy corev1.PullPolicy `json:"imagePullPolicy,omitempty"`
	// ProvisionerImage CSI provisioner sidecar image
	ProvisionerImage string `json:"provisionerImage,omitempty"`
	// NodeRegistrarImage CSI node driver registrar sidecar image
	NodeRegistrarImage string `json:"nodeRegistrarImage,omitempty"`
	// ProvisionerResources Compute resources required by provisioner sidecar container
	ProvisionerResources *corev1.ResourceRequirements `json:"provisionerResources,omitempty"`
	// NodeRegistrarResources Compute resources required by node registrar sidecar container
	NodeRegistrarResources *corev1.ResourceRequirements `json:"nodeRegistrarResources,omitempty"`
	// NodeDriverResources Compute resources required by driver container running on worker nodes
	NodeDriverResources *corev1.ResourceRequirements `json:"nodeDriverResources,omitempty"`
	// ControllerDriverResources Compute resources required by central driver container
	ControllerDriverResources *corev1.ResourceRequirements `json:"controllerDriverResources,omitempty"`
	// ControllerTLSSecret is the name of a secret which contains ca.crt, tls.crt and tls.key data
	// for the scheduler extender and pod mutation webhook. A controller is started if (and only if)
	// this secret is specified.
	ControllerTLSSecret string `json:"controllerTLSSecret,omitempty"`
	// MutatePod defines how a mutating pod webhook is configured if a controller
	// is started. The field is ignored if the controller is not enabled.
	// The default is "Try".
	// +kubebuilder:validation:Enum=Always;Try;Never
	MutatePods MutatePods `json:"mutatePods,omitempty"`
	// SchedulerNodePort, if non-zero, ensures that the "scheduler" service
	// is created as a NodeService with that fixed port number. Otherwise
	// that service is created as a cluster service. The number must be
	// from the range reserved by Kubernetes for
	// node ports. This is useful if the kube-scheduler cannot reach the scheduler
	// extender via a cluster service.
	SchedulerNodePort int32 `json:"schedulerNodePort,omitempty"`
	// DeviceMode to use to manage PMEM devices.
	// +kubebuilder:validation:Enum=lvm;direct
	DeviceMode DeviceMode `json:"deviceMode,omitempty"`
	// LogLevel number for the log verbosity
	LogLevel uint16 `json:"logLevel,omitempty"`
	// LogFormat
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:Enum=text;json
	LogFormat LogFormat `json:"logFormat,omitempty"`
	// NodeSelector node labels to use for selection of driver node
	NodeSelector map[string]string `json:"nodeSelector,omitempty"`
	// PMEMPercentage represents the percentage of space to be used by the driver in each PMEM region
	// on every node. Unset (= zero) selects the default of 100%.
	// This is only valid for driver in LVM mode.
	// +kubebuilder:validation:Minimum=0
	// +kubebuilder:validation:Maximum=100
	PMEMPercentage uint16 `json:"pmemPercentage,omitempty"`
	// Labels contains additional labels for all objects created by the operator.
	Labels map[string]string `json:"labels,omitempty"`
	// KubeletDir kubelet's root directory path
	KubeletDir string `json:"kubeletDir,omitempty"`
}

// DeploymentConditionType type for representing a deployment status condition
type DeploymentConditionType string

const (
	// DriverDeployed means that the all the sub-resources required for the deployment CR
	// got created
	DriverDeployed DeploymentConditionType = "DriverDeployed"
)

// +k8s:deepcopy-gen=true
type DeploymentCondition struct {
	// Type of condition.
	Type DeploymentConditionType `json:"type"`
	// Status of the condition, one of True, False, Unknown.
	Status corev1.ConditionStatus `json:"status"`
	// Message human readable text that explain why this condition is in this state
	Reason string `json:"reason,omitempty"`
	// Last time the condition was probed.
	// +nullable
	LastUpdateTime metav1.Time `json:"lastUpdateTime,omitempty"`
}

type DriverType int

const (
	ControllerDriver DriverType = iota
	NodeDriver
)

func (t DriverType) String() string {
	switch t {
	case ControllerDriver:
		return "Controller"
	case NodeDriver:
		return "Node"
	}
	return ""
}

// +k8s:deepcopy-gen=true
type DriverStatus struct {
	// DriverComponent represents type of the driver: controller or node
	DriverComponent string `json:"component"`
	// Status represents the state of the component; one of `Ready` or `NotReady`.
	// Component becomes `Ready` if all the instances(Pods) of the driver component
	// are in running state. Otherwise, `NotReady`.
	Status string `json:"status"`
	// Reason represents the human readable text that explains why the
	// driver is in this state.
	Reason string `json:"reason"`
	// LastUpdated time of the driver status
	// +nullable
	LastUpdated metav1.Time `json:"lastUpdated,omitempty"`
}

// +k8s:deepcopy-gen=true

// DeploymentStatus defines the observed state of Deployment
type DeploymentStatus struct {
	// INSERT ADDITIONAL STATUS FIELD - define observed state of cluster
	// Important: Run "make operator-generate-k8s" to regenerate code after modifying this file

	// Phase indicates the state of the deployment
	Phase  DeploymentPhase `json:"phase,omitempty"`
	Reason string          `json:"reason,omitempty"`
	// Conditions
	Conditions []DeploymentCondition `json:"conditions,omitempty"`
	Components []DriverStatus        `json:"driverComponents,omitempty"`
	// LastUpdated time of the deployment status
	// +nullable
	LastUpdated metav1.Time `json:"lastUpdated,omitempty"`
}

// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object

// PmemCSIDeployment is the Schema for the deployments API
// +kubebuilder:subresource:status
// +kubebuilder:resource:path=pmemcsideployments,scope=Cluster,shortName=pcd,singular=pmemcsideployment
// +kubebuilder:printcolumn:name="DeviceMode",type=string,JSONPath=`.spec.deviceMode`
// +kubebuilder:printcolumn:name="NodeSelector",type=string,JSONPath=`.spec.nodeSelector`
// +kubebuilder:printcolumn:name="Image",type=string,JSONPath=`.spec.image`
// +kubebuilder:printcolumn:name="Status",type=string,JSONPath=`.status.phase`
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`
// +kubebuilder:storageversion
type PmemCSIDeployment struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   DeploymentSpec   `json:"spec,omitempty"`
	Status DeploymentStatus `json:"status,omitempty"`
}

// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object

// PmemCSIDeploymentList contains a list of PmemCSIDeployment objects
type PmemCSIDeploymentList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []PmemCSIDeployment `json:"items"`
}

func init() {
	SchemeBuilder.Register(&PmemCSIDeployment{}, &PmemCSIDeploymentList{})
}

const (
	// EventReasonNew new driver deployment found
	EventReasonNew = "NewDeployment"
	// EventReasonRunning driver has been successfully deployed
	EventReasonRunning = "Running"
	// EventReasonFailed driver deployment failed, Event.Message holds detailed information
	EventReasonFailed = "Failed"
)

const (
	// DefaultLogLevel default logging level used for the driver
	DefaultLogLevel = uint16(3)
	// DefaultImagePullPolicy default image pull policy for all the images used by the deployment
	DefaultImagePullPolicy = corev1.PullIfNotPresent

	defaultDriverImageName = "intel/pmem-csi-driver"
	defaultDriverImageTag  = "canary"
	// DefaultDriverImage default PMEM-CSI driver docker image
	DefaultDriverImage = defaultDriverImageName + ":" + defaultDriverImageTag

	DefaultMutatePods = MutatePodsTry

	// The sidecar versions must be kept in sync with the
	// deploy/kustomize YAML files! hack/bump-image-versions.sh
	// can be used to update both.

	// DefaultProvisionerImage default external provisioner image to use
	DefaultProvisionerImage = "k8s.gcr.io/sig-storage/csi-provisioner:v2.2.1"

	// DefaultRegistrarImage default node driver registrar image to use
	DefaultRegistrarImage = "k8s.gcr.io/sig-storage/csi-node-driver-registrar:v2.2.0"

	// Below resource requests and limits are derived(with minor adjustments) from
	// recommendations reported by VirtualPodAutoscaler(LowerBound -> Requests and UpperBound -> Limits)

	// DefaultControllerResourceRequestCPU default CPU resource request used for controller driver container
	DefaultControllerResourceRequestCPU = "12m"
	// DefaultControllerResourceRequestMemory default memory resource request used for controller driver container
	DefaultControllerResourceRequestMemory = "128Mi"

	// DefaultNodeResourceRequestCPU default CPU resource request used for node driver container
	DefaultNodeResourceRequestCPU = "100m"
	// DefaultNodeResourceRequestMemory default memory resource request used for node driver container
	DefaultNodeResourceRequestMemory = "250Mi"

	// DefaultNodeRegistrarRequestCPU default CPU resource request used for node registrar container
	DefaultNodeRegistrarRequestCPU = "12m"
	// DefaultNodeRegistrarRequestMemory default memory resource request used for node registrar container
	DefaultNodeRegistrarRequestMemory = "128Mi"

	// DefaultProvisionerRequestCPU default CPU resource request used for provisioner container
	DefaultProvisionerRequestCPU = "12m"
	// DefaultProvisionerRequestMemory default memory resource request used for node registrar container
	DefaultProvisionerRequestMemory = "128Mi"

	// DefaultDeviceMode default device manger used for deployment
	DefaultDeviceMode = DeviceModeLVM
	// DefaultPMEMPercentage PMEM space to reserve for the driver
	DefaultPMEMPercentage = 100
	// DefaultKubeletDir default kubelet's path
	DefaultKubeletDir = "/var/lib/kubelet"
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
	// DeploymentPhaseRunning indicates that the deployment was successful
	DeploymentPhaseRunning DeploymentPhase = "Running"
	// DeploymentPhaseFailed indicates that the deployment was failed
	DeploymentPhaseFailed DeploymentPhase = "Failed"
)

// A TLS secret must contain three data items.
const (
	// TLSSecretCA is the CA bundle.
	TLSSecretCA = "ca.crt"
	// TLSSecretKey is the secret key to be used by the server.
	TLSSecretKey = "tls.key"
	// TLSSecretCert is the public key to used by the server.
	TLSSecretCert = "tls.crt"
)

func (d *PmemCSIDeployment) SetCondition(t DeploymentConditionType, state corev1.ConditionStatus, reason string) {
	for _, c := range d.Status.Conditions {
		if c.Type == t {
			c.Status = state
			c.Reason = reason
			c.LastUpdateTime = metav1.Now()
			return
		}
	}
	d.Status.Conditions = append(d.Status.Conditions, DeploymentCondition{
		Type:           t,
		Status:         state,
		Reason:         reason,
		LastUpdateTime: metav1.Now(),
	})
}

func (d *PmemCSIDeployment) SetDriverStatus(t DriverType, status, reason string) {
	if d.Status.Components == nil {
		d.Status.Components = make([]DriverStatus, 2)
	}
	d.Status.Components[t] = DriverStatus{
		DriverComponent: t.String(),
		Status:          status,
		Reason:          reason,
		LastUpdated:     metav1.Now(),
	}
}

// EnsureDefaults make sure that the deployment object has all defaults set properly
func (d *PmemCSIDeployment) EnsureDefaults(operatorImage string) error {
	// Validate the given driver mode.
	// In a realistic case this check might not needed as it should be
	// handled by JSON schema as we defined deviceMode as enumeration.
	switch d.Spec.DeviceMode {
	case "":
		d.Spec.DeviceMode = DefaultDeviceMode
	case DeviceModeDirect, DeviceModeLVM:
	default:
		return fmt.Errorf("invalid device mode %q", d.Spec.DeviceMode)
	}

	switch d.Spec.MutatePods {
	case "":
		d.Spec.MutatePods = DefaultMutatePods
	case MutatePodsAlways, MutatePodsTry, MutatePodsNever:
	default:
		return fmt.Errorf("invalid MutatePods value: %s", d.Spec.MutatePods)
	}

	if d.Spec.Image == "" {
		// If provided use operatorImage
		if operatorImage != "" {
			d.Spec.Image = operatorImage
		} else {
			d.Spec.Image = DefaultDriverImage
		}
	}
	if d.Spec.PullPolicy == "" {
		d.Spec.PullPolicy = DefaultImagePullPolicy
	}
	if d.Spec.LogLevel == 0 {
		d.Spec.LogLevel = DefaultLogLevel
	}
	if d.Spec.LogFormat == "" {
		d.Spec.LogFormat = LogFormatText
	}

	if d.Spec.ProvisionerImage == "" {
		d.Spec.ProvisionerImage = DefaultProvisionerImage
	}

	if d.Spec.NodeRegistrarImage == "" {
		d.Spec.NodeRegistrarImage = DefaultRegistrarImage
	}

	if d.Spec.NodeSelector == nil {
		d.Spec.NodeSelector = DefaultNodeSelector
	}

	if d.Spec.PMEMPercentage == 0 {
		d.Spec.PMEMPercentage = DefaultPMEMPercentage
	}

	if d.Spec.KubeletDir == "" {
		d.Spec.KubeletDir = DefaultKubeletDir
	}

	if d.Spec.ControllerDriverResources == nil {
		d.Spec.ControllerDriverResources = &corev1.ResourceRequirements{
			Requests: corev1.ResourceList{
				corev1.ResourceCPU:    resource.MustParse(DefaultControllerResourceRequestCPU),
				corev1.ResourceMemory: resource.MustParse(DefaultControllerResourceRequestMemory),
			},
		}
	}

	if d.Spec.ProvisionerResources == nil {
		d.Spec.ProvisionerResources = &corev1.ResourceRequirements{
			Requests: corev1.ResourceList{
				corev1.ResourceCPU:    resource.MustParse(DefaultProvisionerRequestCPU),
				corev1.ResourceMemory: resource.MustParse(DefaultProvisionerRequestMemory),
			},
		}
	}

	if d.Spec.NodeDriverResources == nil {
		d.Spec.NodeDriverResources = &corev1.ResourceRequirements{
			Requests: corev1.ResourceList{
				corev1.ResourceCPU:    resource.MustParse(DefaultNodeResourceRequestCPU),
				corev1.ResourceMemory: resource.MustParse(DefaultNodeResourceRequestMemory),
			},
		}
	}

	if d.Spec.NodeRegistrarResources == nil {
		d.Spec.NodeRegistrarResources = &corev1.ResourceRequirements{
			Requests: corev1.ResourceList{
				corev1.ResourceCPU:    resource.MustParse(DefaultNodeRegistrarRequestCPU),
				corev1.ResourceMemory: resource.MustParse(DefaultNodeRegistrarRequestMemory),
			},
		}
	}

	return nil
}

// GetHyphenedName returns the name of the deployment with dots replaced by hyphens.
// Most objects created for the deployment will use hyphens in the name, sometimes
// with an additional suffix like -controller, but others must use the original
// name (like the CSIDriver object).
func (d *PmemCSIDeployment) GetHyphenedName() string {
	return strings.ReplaceAll(d.GetName(), ".", "-")
}

// RegistrySecretName returns the name of the registry
// Secret object used by the deployment
func (d *PmemCSIDeployment) RegistrySecretName() string {
	return d.GetHyphenedName() + "-registry-secrets"
}

// NodeSecretName returns the name of the node-controller
// Secret object used by the deployment
func (d *PmemCSIDeployment) NodeSecretName() string {
	return d.GetHyphenedName() + "-node-secrets"
}

// CSIDriverName returns the name of the CSIDriver
// object name for the deployment
func (d *PmemCSIDeployment) CSIDriverName() string {
	return d.GetName()
}

// ControllerServiceName returns the name of the controller
// Service object used by the deployment
func (d *PmemCSIDeployment) ControllerServiceName() string {
	return d.GetHyphenedName() + "-controller"
}

// MetricsServiceName returns the name of the controller metrics
// Service object used by the deployment
func (d *PmemCSIDeployment) MetricsServiceName() string {
	return d.GetHyphenedName() + "-metrics"
}

// SchedulerServiceName returns the name of the controller's scheduler
// Service object
func (d *PmemCSIDeployment) SchedulerServiceName() string {
	return d.GetHyphenedName() + "-scheduler"
}

// WebhooksServiceAccountName returns the name of the service account
// used by the StatefulSet with the webhooks.
func (d *PmemCSIDeployment) WebhooksServiceAccountName() string {
	return d.GetHyphenedName() + "-webhooks"
}

// WebhooksRoleName returns the name of the webhooks'
// RBAC Role object name used by the deployment
func (d *PmemCSIDeployment) WebhooksRoleName() string {
	return d.GetHyphenedName() + "-webhooks-cfg"
}

// WebhooksRoleBindingName returns the name of the webhooks'
// RoleBinding object name used by the deployment
func (d *PmemCSIDeployment) WebhooksRoleBindingName() string {
	return d.GetHyphenedName() + "-webhooks-role-cfg"
}

// WebhooksClusterRoleName returns the name of the
// webhooks' ClusterRole object name used by the deployment
func (d *PmemCSIDeployment) WebhooksClusterRoleName() string {
	return d.GetHyphenedName() + "-webhooks-runner"
}

// WebhooksClusterRoleBindingName returns the name of the
// webhooks' ClusterRoleBinding object name used by the deployment
func (d *PmemCSIDeployment) WebhooksClusterRoleBindingName() string {
	return d.GetHyphenedName() + "-webhooks-role"
}

// MutatingWebhookName returns the name of the
// MutatingWebhookConfiguration
func (d *PmemCSIDeployment) MutatingWebhookName() string {
	return d.GetHyphenedName() + "-hook"
}

// NodeServiceAccountName returns the name of the service account
// used by the DaemonSet with the external-provisioner
func (d *PmemCSIDeployment) ProvisionerServiceAccountName() string {
	return d.GetHyphenedName() + "-controller"
}

// ProvisionerRoleName returns the name of the provisioner's
// RBAC Role object name used by the deployment
func (d *PmemCSIDeployment) ProvisionerRoleName() string {
	return d.GetHyphenedName() + "-external-provisioner-cfg"
}

// ProvisionerRoleBindingName returns the name of the provisioner's
// RoleBinding object name used by the deployment
func (d *PmemCSIDeployment) ProvisionerRoleBindingName() string {
	return d.GetHyphenedName() + "-csi-provisioner-role-cfg"
}

// ProvisionerClusterRoleName returns the name of the
// provisioner's ClusterRole object name used by the deployment
func (d *PmemCSIDeployment) ProvisionerClusterRoleName() string {
	return d.GetHyphenedName() + "-external-provisioner-runner"
}

// ProvisionerClusterRoleBindingName returns the name of the
// provisioner ClusterRoleBinding object name used by the deployment
func (d *PmemCSIDeployment) ProvisionerClusterRoleBindingName() string {
	return d.GetHyphenedName() + "-csi-provisioner-role"
}

// NodeDriverName returns the name of the driver
// DaemonSet object name used by the deployment
func (d *PmemCSIDeployment) NodeDriverName() string {
	return d.GetHyphenedName() + "-node"
}

// ControllerDriverName returns the name of the controller
// StatefulSet object name used by the deployment
func (d *PmemCSIDeployment) ControllerDriverName() string {
	return d.GetHyphenedName() + "-controller"
}

// NodeSetupServiceAccountName returns the name of the service account
// used by the StatefulSet with the webhooks.
func (d *PmemCSIDeployment) NodeSetupServiceAccountName() string {
	return d.GetHyphenedName() + "-node-setup"
}

// NodeSetupClusterRoleName returns the name of the
// webhooks' ClusterRole object name used by the deployment
func (d *PmemCSIDeployment) NodeSetupClusterRoleName() string {
	return d.GetHyphenedName() + "-node-setup-runner"
}

// NodeSetupClusterRoleBindingName returns the name of the
// webhooks' ClusterRoleBinding object name used by the deployment
func (d *PmemCSIDeployment) NodeSetupClusterRoleBindingName() string {
	return d.GetHyphenedName() + "-node-setup-role"
}

// NodeSetupName returns the name of the node setup
// DaemonSet object name used by the deployment
func (d *PmemCSIDeployment) NodeSetupName() string {
	return d.GetHyphenedName() + "-node-setup"
}

// GetOwnerReference returns self owner reference could be used by other object
// to add this deployment to it's owner reference list.
func (d *PmemCSIDeployment) GetOwnerReference() metav1.OwnerReference {
	blockOwnerDeletion := true
	isController := true
	return metav1.OwnerReference{
		APIVersion:         d.APIVersion,
		Kind:               d.Kind,
		Name:               d.GetName(),
		UID:                d.GetUID(),
		BlockOwnerDeletion: &blockOwnerDeletion,
		Controller:         &isController,
	}
}
