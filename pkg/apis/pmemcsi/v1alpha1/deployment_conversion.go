/*
Copyright The Kubernetes Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package v1alpha1

import (
	"k8s.io/klog/v2"
	"sigs.k8s.io/controller-runtime/pkg/conversion"

	"github.com/intel/pmem-csi/pkg/apis/pmemcsi/v1beta1"
)

var _ conversion.Convertible = &Deployment{}

// ConvertTo converts to Hub(v1beta1) type
func (d *Deployment) ConvertTo(dst conversion.Hub) error {
	in := d.DeepCopy()
	out := dst.(*v1beta1.Deployment)

	// Use v1alpha1 `spec.{node,controller}Resources` for setting pmem-driver
	// container resources, other container resources are set to default.
	out.Spec.NodeDriverResources = in.Spec.NodeResources
	out.Spec.ControllerDriverResources = in.Spec.ControllerResources

	// no change in other fields
	out.ObjectMeta = in.ObjectMeta
	out.Spec.LogLevel = in.Spec.LogLevel
	out.Spec.Image = in.Spec.Image
	out.Spec.CACert = in.Spec.CACert
	out.Spec.RegistryCert = in.Spec.RegistryCert
	out.Spec.RegistryPrivateKey = in.Spec.RegistryPrivateKey
	out.Spec.NodeControllerCert = in.Spec.NodeControllerCert
	out.Spec.NodeControllerPrivateKey = in.Spec.NodeControllerPrivateKey
	out.Spec.NodeSelector = in.Spec.NodeSelector
	out.Spec.Labels = in.Spec.Labels

	out.Status.Components = nil
	for _, s := range in.Status.Components {
		out.Status.Components = append(out.Status.Components, v1beta1.DriverStatus{
			DriverComponent: s.DriverComponent,
			Status:          s.Status,
			Reason:          s.Reason,
			LastUpdated:     s.LastUpdated,
		})
	}
	out.Status.Conditions = nil
	for _, c := range in.Status.Conditions {
		out.Status.Conditions = append(out.Status.Conditions, v1beta1.DeploymentCondition{
			Type:           v1beta1.DeploymentConditionType(string(c.Type)),
			Status:         c.Status,
			Reason:         c.Reason,
			LastUpdateTime: c.LastUpdateTime,
		})
	}
	out.Status.Phase = v1beta1.DeploymentPhase(string(in.Status.Phase))
	out.Status.Reason = in.Status.Reason
	out.Status.LastUpdated = in.Status.LastUpdated

	// +kubebuilder:docs-gen:collapse=rote conversion

	klog.Infof("Converted Object: %+v", *out)

	return nil
}

// ConvertFrom converts from Hub type to current type
func (d *Deployment) ConvertFrom(src conversion.Hub) error {
	in := src.(*v1beta1.Deployment).DeepCopy()
	out := d

	// Use v1beta1 `spec.{node,controller}DriverResources` as setting the
	// pod resources
	out.Spec.NodeResources = in.Spec.NodeDriverResources
	out.Spec.ControllerResources = in.Spec.ControllerDriverResources

	// no change in other fields
	out.ObjectMeta = in.ObjectMeta
	out.Spec.LogLevel = in.Spec.LogLevel
	out.Spec.Image = in.Spec.Image
	out.Spec.CACert = in.Spec.CACert
	out.Spec.RegistryCert = in.Spec.RegistryCert
	out.Spec.RegistryPrivateKey = in.Spec.RegistryPrivateKey
	out.Spec.NodeControllerCert = in.Spec.NodeControllerCert
	out.Spec.NodeControllerPrivateKey = in.Spec.NodeControllerPrivateKey
	out.Spec.NodeSelector = in.Spec.NodeSelector
	out.Spec.Labels = in.Spec.Labels

	out.Status.Components = nil
	for _, s := range in.Status.Components {
		out.Status.Components = append(out.Status.Components, DriverStatus{
			DriverComponent: s.DriverComponent,
			Status:          s.Status,
			Reason:          s.Reason,
			LastUpdated:     s.LastUpdated,
		})
	}
	out.Status.Conditions = nil
	for _, c := range in.Status.Conditions {
		out.Status.Conditions = append(out.Status.Conditions, DeploymentCondition{
			Type:           DeploymentConditionType(string(c.Type)),
			Status:         c.Status,
			Reason:         c.Reason,
			LastUpdateTime: c.LastUpdateTime,
		})
	}
	out.Status.Phase = DeploymentPhase(string(in.Status.Phase))
	out.Status.Reason = in.Status.Reason
	out.Status.LastUpdated = in.Status.LastUpdated
	// +kubebuilder:docs-gen:collapse=rote conversion

	return nil
}
