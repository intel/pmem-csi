/*
Copyright 2020 Intel Corporation.

SPDX-License-Identifier: Apache-2.0
*/

package deploy

import (
	"embed"
	"fmt"
	"path"
	"regexp"

	api "github.com/intel/pmem-csi/pkg/apis/pmemcsi/v1beta1"
	"github.com/intel/pmem-csi/pkg/version"
)

//go:embed kubernetes-*/*/pmem-csi.yaml
//go:embed kustomize/webhook/webhook.yaml
//go:embed kustomize/scheduler/scheduler-service.yaml
var assets embed.FS

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

var re = regexp.MustCompile(`^kubernetes-([0-9\.]*)([^/]*)/([^/]*)$`)

func init() {
	deployDir, err := assets.ReadDir(".")
	if err != nil {
		panic(err)
	}
	for _, item := range deployDir {
		if !item.IsDir() {
			continue
		}
		kubernetesDir, err := assets.ReadDir(item.Name())
		if err != nil {
			panic(err)
		}
		for _, item2 := range kubernetesDir {
			name := path.Join(item.Name(), item2.Name())
			parts := re.FindStringSubmatch(name)
			if parts == nil {
				continue
			}
			kubernetes, err := version.Parse(parts[1])
			if err != nil {
				panic(fmt.Sprintf("unexpected version in %s: %v", name, err))
			}
			yamls = append(yamls, YamlFile{
				Name:       name,
				Kubernetes: kubernetes,
				Flavor:     parts[3],
				DeviceMode: api.DeviceMode(parts[3]),
			})
		}
	}
}

// ListAll returns information about all embedded YAML files.
func ListAll() []YamlFile {
	return yamls
}

// Asset returns the content of an embedded file.
// The path must be relative to the "deploy" dir.
func Asset(path string) ([]byte, error) {
	return assets.ReadFile(path)
}
