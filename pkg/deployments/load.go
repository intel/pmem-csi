/*
Copyright 2020 Intel Corporation.

SPDX-License-Identifier: Apache-2.0
*/

package deployments

import (
	"bytes"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"

	"github.com/intel/pmem-csi/deploy"
	api "github.com/intel/pmem-csi/pkg/apis/pmemcsi/v1alpha1"
	"github.com/intel/pmem-csi/pkg/version"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/client-go/kubernetes/scheme"
)

// LoadObjects reads all objects stored in a pmem-csi.yaml reference file.
func LoadObjects(kubernetes version.Version, deviceMode api.DeviceMode) ([]unstructured.Unstructured, error) {
	return loadObjects(kubernetes, deviceMode, nil, nil)
}

var pmemImage = regexp.MustCompile(`image: intel/pmem-csi-driver(-test)?:\w+`)
var nameRegex = regexp.MustCompile(`(name|app|secretName|serviceName|serviceAccountName): pmem-csi`)

// LoadAndCustomizeObjects reads all objects stored in a pmem-csi.yaml reference file
// and updates them on-the-fly according to the deployment spec, namespace and name.
func LoadAndCustomizeObjects(kubernetes version.Version, deviceMode api.DeviceMode,
	namespace string, deployment api.Deployment) ([]unstructured.Unstructured, error) {

	// Conceptually this function is similar to calling "kustomize" for
	// our deployments. But because we controll the input, we can do some
	// things like renaming with a simple text search/replace.
	patchYAML := func(yaml *[]byte) {
		// This renames the objects. A hyphen is used instead of a dot,
		// except for CSIDriver which needs the exact name.
		*yaml = nameRegex.ReplaceAll(*yaml, []byte("$1: "+deployment.GetHyphenedName()))

		// Update the driver name inside the state dir.
		*yaml = bytes.ReplaceAll(*yaml, []byte("path: /var/lib/pmem-csi.intel.com"), []byte("path: /var/lib/"+deployment.Name))
		*yaml = bytes.ReplaceAll(*yaml, []byte("mountPath: /var/lib/pmem-csi.intel.com"), []byte("mountPath: /var/lib/"+deployment.Name))

		// This assumes that all namespaced objects actually have "namespace: default".
		*yaml = bytes.ReplaceAll(*yaml, []byte("namespace: default"), []byte("namespace: "+namespace))

		// Also rename the prefix inside the registry endpoint.
		*yaml = bytes.ReplaceAll(*yaml,
			[]byte("tcp://pmem-csi"),
			[]byte("tcp://"+deployment.GetHyphenedName()))

		*yaml = bytes.ReplaceAll(*yaml,
			[]byte("imagePullPolicy: IfNotPresent"),
			[]byte("imagePullPolicy: "+deployment.Spec.PullPolicy))

		*yaml = bytes.ReplaceAll(*yaml,
			[]byte("-v=3"),
			[]byte(fmt.Sprintf("-v=%d", deployment.Spec.LogLevel)))

		*yaml = pmemImage.ReplaceAll(*yaml, []byte("image: "+deployment.Spec.Image))
	}

	patchUnstructured := func(obj *unstructured.Unstructured) {
		if deployment.Spec.Labels != nil {
			labels := obj.GetLabels()
			for key, value := range deployment.Spec.Labels {
				labels[key] = value
			}
			obj.SetLabels(labels)
		}

		switch obj.GetKind() {
		case "CSIDriver":
			obj.SetName(deployment.GetName())
		case "StatefulSet":
			if err := patchPodTemplate(obj, deployment, deployment.Spec.ControllerResources); err != nil {
				// TODO: avoid panic
				panic(fmt.Errorf("set controller resources: %v", err))
			}
		case "DaemonSet":
			if err := patchPodTemplate(obj, deployment, deployment.Spec.NodeResources); err != nil {
				// TODO: avoid panic
				panic(fmt.Errorf("set node resources: %v", err))
			}
			outerSpec := obj.Object["spec"].(map[string]interface{})
			template := outerSpec["template"].(map[string]interface{})
			spec := template["spec"].(map[string]interface{})
			if deployment.Spec.NodeSelector != nil {
				selector := map[string]interface{}{}
				for key, value := range deployment.Spec.NodeSelector {
					selector[key] = value
				}
				spec["nodeSelector"] = selector
			}
		}
	}

	return loadObjects(kubernetes, deviceMode, patchYAML, patchUnstructured)
}

func patchPodTemplate(obj *unstructured.Unstructured, deployment api.Deployment, resources *corev1.ResourceRequirements) error {
	if resources == nil {
		return nil
	}

	outerSpec := obj.Object["spec"].(map[string]interface{})
	template := outerSpec["template"].(map[string]interface{})
	spec := template["spec"].(map[string]interface{})
	metadata := template["metadata"].(map[string]interface{})

	if deployment.Spec.Labels != nil {
		labels := metadata["labels"]
		var labelsMap map[string]interface{}
		if labels == nil {
			labelsMap = map[string]interface{}{}
		} else {
			labelsMap = labels.(map[string]interface{})
		}
		for key, value := range deployment.Spec.Labels {
			labelsMap[key] = value
		}
		metadata["labels"] = labelsMap
	}

	// Convert through JSON.
	resourcesObj := map[string]interface{}{}
	data, err := json.Marshal(resources)
	if err != nil {
		return err
	}
	if err := json.Unmarshal(data, &resourcesObj); err != nil {
		return err
	}

	containers := spec["containers"].([]interface{})
	for _, container := range containers {
		// Mimick the current operator behavior
		// (https://github.com/intel/pmem-csi/issues/616) and apply
		// the resource requirements to all containers.
		container := container.(map[string]interface{})
		container["resources"] = resourcesObj

		// Override driver name in env var.
		env := container["env"]
		if env != nil {
			env := env.([]interface{})
			for _, entry := range env {
				entry := entry.(map[string]interface{})
				if entry["name"].(string) == "PMEM_CSI_DRIVER_NAME" {
					entry["value"] = deployment.GetName()
					break
				}
			}
		}

		var image string
		switch container["name"].(string) {
		case "external-provisioner":
			image = deployment.Spec.ProvisionerImage
		case "driver-registrar":
			image = deployment.Spec.NodeRegistrarImage
		case "pmem-driver":
			cmd := container["command"].([]interface{})
			for i := range cmd {
				arg := cmd[i].(string)
				if strings.HasPrefix(arg, "-pmemPercentage=") {
					cmd[i] = fmt.Sprintf("-pmemPercentage=%d", deployment.Spec.PMEMPercentage)
					break
				}
			}
		}
		if image != "" {
			container["image"] = image
		}
	}

	return nil
}

func loadObjects(kubernetes version.Version, deviceMode api.DeviceMode,
	patchYAML func(yaml *[]byte),
	patchUnstructured func(obj *unstructured.Unstructured)) ([]unstructured.Unstructured, error) {
	path := fmt.Sprintf("deploy/kubernetes-%s/%s/pmem-csi.yaml", kubernetes, deviceMode)

	// We load the builtin yaml files.
	yaml, err := deploy.Asset(path)
	if err != nil {
		return nil, fmt.Errorf("read reference yaml file: %w", err)
	}

	// Split at the "---" separator before working on individual
	// item. Only works for .yaml.
	//
	// We need to split ourselves because we need access to each
	// original chunk of data for decoding.  kubectl has its own
	// infrastructure for this, but that is a lot of code with
	// many dependencies.
	items := bytes.Split(yaml, []byte("\n---"))
	deserializer := scheme.Codecs.UniversalDeserializer()
	var objects []unstructured.Unstructured
	for _, item := range items {
		obj := unstructured.Unstructured{}
		if patchYAML != nil {
			patchYAML(&item)
		}
		_, _, err := deserializer.Decode(item, nil, &obj)
		if err != nil {
			return nil, fmt.Errorf("decode item %q from file %q: %v", item, path, err)
		}
		if patchUnstructured != nil {
			patchUnstructured(&obj)
		}
		objects = append(objects, obj)
	}
	return objects, nil
}
