/*
Copyright 2019  Intel Corporation.

SPDX-License-Identifier: Apache-2.0
*/
package v1alpha1_test

import (
	"testing"

	"github.com/intel/pmem-csi/pkg/apis"
	api "github.com/intel/pmem-csi/pkg/apis/pmemcsi/v1alpha1"
	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	"k8s.io/client-go/kubernetes/scheme"
)

func TestDeploymentType(t *testing.T) {
	RegisterFailHandler(Fail)

	err := apis.AddToScheme(scheme.Scheme)
	Expect(err).Should(BeNil(), "Failed to add api schema")

	RunSpecs(t, "PMEM Operator API test suite")
}

var _ = Describe("Operator", func() {

	BeforeEach(func() {
	})

	Context("API", func() {
		//
		// TODO: Add input-validation tests for the API spec fields
		//
		It("shall set defaults for empty deployment", func() {
			d := api.Deployment{}
			d.EnsureDefaults()

			Expect(d.Spec.DriverName).Should(BeEquivalentTo(api.DefaultDriverName), "default driver name mismatch")
			Expect(d.Spec.LogLevel).Should(BeEquivalentTo(api.DefaultLogLevel), "default logging level mismatch")
			Expect(d.Spec.DeviceMode).Should(BeEquivalentTo(api.DefaultDeviceMode), "default driver mode mismatch")
			Expect(d.Spec.Image).Should(BeEquivalentTo(api.DefaultDriverImage), "default driver image mismatch")
			Expect(d.Spec.PullPolicy).Should(BeEquivalentTo(api.DefaultImagePullPolicy), "default image pull policy mismatch")
			Expect(d.Spec.ProvisionerImage).Should(BeEquivalentTo(api.DefaultProvisionerImage), "default provisioner image mismatch")
			Expect(d.Spec.NodeRegistrarImage).Should(BeEquivalentTo(api.DefaultRegistrarImage), "default node driver registrar image mismatch")

			Expect(d.Spec.ControllerResources).ShouldNot(BeNil(), "default controller resources not set")
			rs := d.Spec.ControllerResources.Limits
			Expect(rs.Cpu().String()).Should(BeEquivalentTo(api.DefaultControllerResourceCPU), "controller driver 'cpu' resource mismatch")
			Expect(rs.Memory().String()).Should(BeEquivalentTo(api.DefaultControllerResourceMemory), "controller driver 'memory' resource mismatch")

			Expect(d.Spec.NodeResources).ShouldNot(BeNil(), "default node resources not set")
			nrs := d.Spec.NodeResources.Limits
			Expect(nrs.Cpu().String()).Should(BeEquivalentTo(api.DefaultNodeResourceCPU), "node driver 'cpu' resource mismatch")
			Expect(nrs.Memory().String()).Should(BeEquivalentTo(api.DefaultNodeResourceMemory), "node driver 'cpu' resource mismatch")
		})

		It("shall be able to set values", func() {
			yaml := `kind: Deployment
apiVersion: pmem-csi.intel.com/v1alpha1
metadata:
  name: test-deployment
spec:
  driverName: test-driver
  logLevel: 10
  deviceMode: direct
  image: test-driver:v0.0.0
  imagePullPolicy: Never
  provisionerImage: test-provisioner:v0.0.0
  nodeRegistrarImage: test-driver-registrar:v0.0.0
  controllerResources:
    requests:
      cpu: 1000m
      memory: 10Mi
  nodeResources:
    requests:
      cpu: 2000m
      memory: 100Mi
`
			decode := scheme.Codecs.UniversalDeserializer().Decode

			obj, _, err := decode([]byte(yaml), nil, nil)
			Expect(err).Should(BeNil(), "Failed to parse deployment")
			Expect(obj).ShouldNot(BeNil(), "Nil deployment object")

			d := obj.(*api.Deployment)
			d.EnsureDefaults()

			Expect(d.Spec.DriverName).Should(BeEquivalentTo("test-driver"), "driver name mismatch")
			Expect(d.Spec.LogLevel).Should(BeEquivalentTo(10), "logging level mismatch")
			Expect(d.Spec.DeviceMode).Should(BeEquivalentTo("direct"), "driver mode mismatch")
			Expect(d.Spec.Image).Should(BeEquivalentTo("test-driver:v0.0.0"), "driver image mismatch")
			Expect(d.Spec.PullPolicy).Should(BeEquivalentTo("Never"), "image pull policy mismatch")
			Expect(d.Spec.ProvisionerImage).Should(BeEquivalentTo("test-provisioner:v0.0.0"), "provisioner image mismatch")
			Expect(d.Spec.NodeRegistrarImage).Should(BeEquivalentTo("test-driver-registrar:v0.0.0"), "node driver registrar image mismatch")

			Expect(d.Spec.ControllerResources).ShouldNot(BeNil(), "controller resources not set")
			rs := d.Spec.ControllerResources.Requests
			Expect(rs.Cpu().Cmp(resource.MustParse("1000m"))).Should(BeZero(), "controller driver 'cpu' resource mismatch")
			Expect(rs.Memory().Cmp(resource.MustParse("10Mi"))).Should(BeZero(), "controller driver 'memory' resource mismatch")

			Expect(d.Spec.NodeResources).ShouldNot(BeNil(), "node resources not set")
			nrs := d.Spec.NodeResources.Requests
			Expect(nrs.Cpu().Cmp(resource.MustParse("2000m"))).Should(BeZero(), "node driver 'cpu' resource mismatch")
			Expect(nrs.Memory().Cmp(resource.MustParse("100Mi"))).Should(BeZero(), "node driver 'cpu' resource mismatch")
		})

		It("compare two deployments", func() {
			d1 := &api.Deployment{}
			d2 := &api.Deployment{}
			changes := map[api.DeploymentChange]struct{}{}
			Expect(d1.Compare(d2)).Should(BeElementOf(changes), "two empty deployments should be equal")

			d1.EnsureDefaults()
			d2.EnsureDefaults()
			Expect(d1.Compare(d2)).Should(BeElementOf(changes), "two default deployments should be equaval")

			d2.Spec.DriverName = "new-driver"
			changes[api.DriverName] = struct{}{}
			Expect(d1.Compare(d2)).Should(BeElementOf(changes), "expected to detect change in driver name")

			d2.Spec.DeviceMode = api.DeviceModeDirect
			changes[api.DriverMode] = struct{}{}
			Expect(d1.Compare(d2)).Should(BeElementOf(changes), "expected to detect chagned device mode")

			d2.Spec.LogLevel = d2.Spec.LogLevel + 1
			changes[api.LogLevel] = struct{}{}
			Expect(d1.Compare(d2)).Should(BeElementOf(changes), "expected to detect change in log level")

			d2.Spec.Image = "new-driver-image"
			changes[api.DriverImage] = struct{}{}
			Expect(d1.Compare(d2)).Should(BeElementOf(changes), "expected to detect change in driver image")

			d2.Spec.PullPolicy = corev1.PullNever
			changes[api.PullPolicy] = struct{}{}
			Expect(d1.Compare(d2)).Should(BeElementOf(changes), "expected to detect change in image pull policy")

			d2.Spec.ProvisionerImage = "new-provisioner-image"
			changes[api.ProvisionerImage] = struct{}{}
			Expect(d1.Compare(d2)).Should(BeElementOf(changes), "expected to detect change in provisioner image")

			d2.Spec.NodeRegistrarImage = "new-node-driver-registrar-image"
			changes[api.NodeRegistrarImage] = struct{}{}
			Expect(d1.Compare(d2)).Should(BeElementOf(changes), "expected to detect change in node registrar image")

			d2.Spec.ControllerResources = &corev1.ResourceRequirements{}
			changes[api.ControllerResources] = struct{}{}
			Expect(d1.Compare(d2)).Should(BeElementOf(changes), "expected to detect change in controller resources")

			d2.Spec.ControllerResources = &corev1.ResourceRequirements{
				Requests: corev1.ResourceList{
					corev1.ResourceCPU:    resource.MustParse("10m"),
					corev1.ResourceMemory: resource.MustParse("5Gi"),
				},
			}
			Expect(d1.Compare(d2)).Should(BeElementOf(changes), "expected to detect change in controller resource requests")

			d2.Spec.ControllerResources = &corev1.ResourceRequirements{
				Limits: corev1.ResourceList{
					corev1.ResourceCPU:    resource.MustParse("10m"),
					corev1.ResourceMemory: resource.MustParse("5Gi"),
				},
			}
			Expect(d1.Compare(d2)).Should(BeElementOf(changes), "expected to detect change in controller resource limits")

			d2.Spec.NodeResources = &corev1.ResourceRequirements{}
			changes[api.NodeResources] = struct{}{}
			Expect(d1.Compare(d2)).Should(BeElementOf(changes), "expected to detect change in node resources")

			d2.Spec.NodeResources = &corev1.ResourceRequirements{
				Limits: corev1.ResourceList{
					corev1.ResourceCPU:    resource.MustParse("10m"),
					corev1.ResourceMemory: resource.MustParse("5Gi"),
				},
			}
			Expect(d1.Compare(d2)).Should(BeElementOf(changes), "expected to detect change in node resource limits")

			d2.Spec.NodeResources = &corev1.ResourceRequirements{
				Requests: corev1.ResourceList{
					corev1.ResourceCPU:    resource.MustParse("10m"),
					corev1.ResourceMemory: resource.MustParse("5Gi"),
				},
			}
			Expect(d1.Compare(d2)).Should(BeElementOf(changes), "expected to detect change in node resource requests")
		})

		It("should have valid json schema", func() {
			crd := api.GetDeploymentCRDSchema()
			Expect(crd).ShouldNot(BeNil(), "Nil CRD schmea")
			Expect(crd.Type).Should(BeEquivalentTo("object"), "Deployment JSON schema type mismatch")
			spec, ok := crd.Properties["spec"]
			Expect(ok).Should(BeTrue(), "Deployment JSON schema does not have 'spec'")
			status, ok := crd.Properties["status"]
			Expect(ok).Should(BeTrue(), "Deployment JSON schema does not have 'status'")

			specProperties := map[string]string{
				"driverName":          "string",
				"logLevel":            "integer",
				"image":               "string",
				"imagePullPolicy":     "string",
				"provisionerImage":    "string",
				"nodeRegistrarImage":  "string",
				"controllerResources": "object",
				"nodeResources":       "object",
			}
			for prop, tipe := range specProperties {
				jsonProp, ok := spec.Properties[prop]
				Expect(ok).Should(BeTrue(), "Missing %q property in deployment spec", prop)
				Expect(jsonProp.Type).Should(BeEquivalentTo(tipe), "%q property type mismatch", prop)
			}

			statusProperties := map[string]string{
				"phase": "string",
			}
			for prop, tipe := range statusProperties {
				jsonProp, ok := status.Properties[prop]
				Expect(ok).Should(BeTrue(), "Missing %q property in deployment status", prop)
				Expect(jsonProp.Type).Should(BeEquivalentTo(tipe), "%q property type mismatch", prop)
			}
		})
	})
})
