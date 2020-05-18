/*
Copyright 2020 Intel Corporation.

SPDX-License-Identifier: Apache-2.0
*/

package deploy

import (
	"fmt"
	"regexp"

	api "github.com/intel/pmem-csi/pkg/apis/pmemcsi/v1alpha1"
	"github.com/intel/pmem-csi/pkg/version"
)

// YamlFile contains all objects of a certain deployment.
type YamlFile struct {
	// Name is the unique string which identifies the deployment.
	Name string

	// Kubernetes is the <major>.<minor> version the deployment
	// was written for.
	Kubernetes version.Version

	// DeviceMode defines in which mode the deployed driver will
	// operate.
	DeviceMode api.DeviceMode
}

var yamls []YamlFile

var re = regexp.MustCompile(`^deploy/kubernetes-([^/]*)/([^/]*)/pmem-csi.yaml$`)

func init() {
	for _, file := range AssetNames() {
		parts := re.FindStringSubmatch(file)
		if parts == nil {
			panic(fmt.Sprintf("unexpected deployment asset: %s", file))
		}
		kubernetes, err := version.Parse(parts[1])
		if err != nil {
			panic(fmt.Sprintf("unexpected version in %s: %v", file, err))
		}
		yamls = append(yamls, YamlFile{
			Name:       file,
			Kubernetes: kubernetes,
			DeviceMode: api.DeviceMode(parts[2]),
		})
	}
}

// ListAll returns information about all embedded YAML files.
func ListAll() []YamlFile {
	return yamls
}
