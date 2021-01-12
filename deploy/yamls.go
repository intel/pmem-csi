/*
Copyright 2020 Intel Corporation.

SPDX-License-Identifier: Apache-2.0
*/

package deploy

import (
	"fmt"
	"regexp"

	api "github.com/intel/pmem-csi/pkg/apis/pmemcsi/v1beta1"
	"github.com/intel/pmem-csi/pkg/version"
)

// YamlFile contains all objects of a certain deployment.
type YamlFile struct {
	// Name is the unique string which identifies the deployment.
	Name string

	// Kubernetes is the <major>.<minor> version the deployment
	// was written for.
	Kubernetes version.Version

	// Flavor is a variant of the normal deployment for the Kubernetes version.
	// Empty or a string leading with a hyphen.
	Flavor string

	// DeviceMode defines in which mode the deployed driver will
	// operate.
	DeviceMode api.DeviceMode
}

var yamls []YamlFile

var re = regexp.MustCompile(`^deploy/kubernetes-([0-9\.]*)([^/]*)/([^/]*)/pmem-csi.yaml$`)

func init() {
	for _, file := range AssetNames() {
		parts := re.FindStringSubmatch(file)
		if parts == nil {
			continue
		}
		kubernetes, err := version.Parse(parts[1])
		if err != nil {
			panic(fmt.Sprintf("unexpected version in %s: %v", file, err))
		}
		yamls = append(yamls, YamlFile{
			Name:       file,
			Kubernetes: kubernetes,
			Flavor:     parts[3],
			DeviceMode: api.DeviceMode(parts[3]),
		})
	}
}

// ListAll returns information about all embedded YAML files.
func ListAll() []YamlFile {
	return yamls
}
