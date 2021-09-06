/*
Copyright 2017 The Kubernetes Authors.

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

package driver

import (
	"fmt"
	"strings"

	storagev1 "k8s.io/api/storage/v1"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/kubernetes/test/e2e/framework"
	"k8s.io/kubernetes/test/e2e/framework/skipper"
	e2evolume "k8s.io/kubernetes/test/e2e/framework/volume"
	storageframework "k8s.io/kubernetes/test/e2e/storage/framework"
	"k8s.io/kubernetes/test/e2e/storage/utils"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
)

// DynamicDriver has the ability to return a modified copy of itself with additional options set.
type DynamicDriver interface {
	storageframework.TestDriver

	// WithStorageClassNameSuffix sets a suffix which gets added
	// to the name of all future storage classes that
	// GetDynamicProvisionStorageClass creates. Can be used to
	// create more than one class per test.
	WithStorageClassNameSuffix(suffix string) DynamicDriver

	// WithParameters sets parameters that are used in future
	// storage classes and CSI inline volumes.
	WithParameters(parameters map[string]string) DynamicDriver
}

// CSIDriver exposes the CSI driver name, something that is normally hidden.
type CSIDriver interface {
	GetCSIDriverName(config *storageframework.PerTestConfig) string
}

func New(name, csiDriverName string, fsTypes []string, scManifests map[string]string) storageframework.TestDriver {
	if fsTypes == nil {
		fsTypes = []string{"", "ext4", "xfs"}
	}
	if scManifests == nil {
		scManifests = map[string]string{
			"":     "deploy/common/pmem-storageclass-default.yaml",
			"ext4": "deploy/common/pmem-storageclass-ext4.yaml",
			"xfs":  "deploy/common/pmem-storageclass-xfs.yaml",
		}
	}
	return &manifestDriver{
		driverInfo: storageframework.DriverInfo{
			Name:            name,
			MaxFileSize:     storageframework.FileSizeMedium,
			SupportedFsType: sets.NewString(fsTypes...),
			Capabilities: map[storageframework.Capability]bool{
				storageframework.CapPersistence: true,
				storageframework.CapFsGroup:     true,
				storageframework.CapExec:        true,
				storageframework.CapBlock:       true,
			},
			SupportedSizeRange: e2evolume.SizeRange{
				// There is test in VolumeIO suite creating 102 MB of content
				// so we use 110 MB as minimum size to fit that with some margin.
				// TODO: fix that upstream test to have a suitable minimum size
				//
				// Without VolumeIO suite, 16Mi would be enough as smallest xfs system size.
				// Ref: http://man7.org/linux/man-pages/man8/mkfs.xfs.8.html
				Min: "110Mi",
			},
		},
		scManifest:    scManifests,
		csiDriverName: csiDriverName,
	}
}

type manifestDriver struct {
	driverInfo    storageframework.DriverInfo
	csiDriverName string
	patchOptions  utils.PatchCSIOptions
	manifests     []string
	scManifest    map[string]string
	scSuffix      string
	parameters    map[string]string
}

var _ storageframework.TestDriver = &manifestDriver{}
var _ storageframework.DynamicPVTestDriver = &manifestDriver{}
var _ storageframework.EphemeralTestDriver = &manifestDriver{}
var _ DynamicDriver = &manifestDriver{}

func (m *manifestDriver) GetDriverInfo() *storageframework.DriverInfo {
	return &m.driverInfo
}

func (m *manifestDriver) SkipUnsupportedTest(pattern storageframework.TestPattern) {
	if !m.driverInfo.SupportedFsType.Has(pattern.FsType) {
		skipper.Skipf("fsType %q not supported", pattern.FsType)
	}
}

func (m *manifestDriver) GetDynamicProvisionStorageClass(config *storageframework.PerTestConfig, fsType string) *storagev1.StorageClass {
	f := config.Framework

	scManifest, ok := m.scManifest[fsType]
	Expect(ok).To(BeTrue(), "Unsupported filesystem type %s", fsType)

	items, err := utils.LoadFromManifests(scManifest)
	Expect(err).NotTo(HaveOccurred())
	Expect(len(items)).To(Equal(1), "exactly one item from %s", scManifest)

	err = utils.PatchItems(f, f.Namespace, items...)
	Expect(err).NotTo(HaveOccurred())
	err = utils.PatchCSIDeployment(f, m.finalPatchOptions(f), items[0])
	Expect(err).NotTo(HaveOccurred())

	sc, ok := items[0].(*storagev1.StorageClass)
	Expect(ok).To(BeTrue(), "storage class from %s", scManifest)
	sc.Provisioner = m.csiDriverName
	sc.Name = config.Prefix + "-" + sc.Name

	// Add additional parameters, if any.
	for name, value := range m.parameters {
		if sc.Parameters == nil {
			sc.Parameters = map[string]string{}
		}
		sc.Parameters[name] = value
	}
	sc.Name += m.scSuffix

	return sc
}

func (m *manifestDriver) PrepareTest(f *framework.Framework) (*storageframework.PerTestConfig, func()) {
	config := &storageframework.PerTestConfig{
		Driver:    m,
		Prefix:    "pmem",
		Framework: f,
	}
	if len(m.manifests) == 0 {
		// Nothing todo.
		return config, func() {}
	}

	By(fmt.Sprintf("deploying %s driver", m.driverInfo.Name))
	cleanup, err := utils.CreateFromManifests(f, f.Namespace, func(item interface{}) error {
		return utils.PatchCSIDeployment(f, m.finalPatchOptions(f), item)
	},
		m.manifests...,
	)
	framework.ExpectNoError(err, "deploying driver %s", m.driverInfo.Name)
	return config, func() {
		By(fmt.Sprintf("uninstalling %s driver", m.driverInfo.Name))
		cleanup()
	}
}

func (m *manifestDriver) finalPatchOptions(f *framework.Framework) utils.PatchCSIOptions {
	o := m.patchOptions
	// Unique name not available yet when configuring the driver.
	if strings.HasSuffix(o.NewDriverName, "-") {
		o.NewDriverName += f.UniqueName
	}
	return o
}

func (m *manifestDriver) GetVolume(config *storageframework.PerTestConfig, volumeNumber int) (map[string]string, bool, bool) {
	attributes := map[string]string{"size": m.driverInfo.SupportedSizeRange.Min}
	shared := false
	readOnly := false
	// TODO (?): this trick with the driver name might no longer be necessary.
	if strings.HasSuffix(m.driverInfo.Name, "-kata") {
		attributes["kataContainers"] = "true"
	}
	for name, value := range m.parameters {
		attributes[name] = value
	}

	return attributes, shared, readOnly
}

func (m *manifestDriver) GetCSIDriverName(config *storageframework.PerTestConfig) string {
	// Return real driver name.
	// We can't use m.driverInfo.Name as its not necessarily the real driver name
	return m.csiDriverName
}

func (m *manifestDriver) WithParameters(parameters map[string]string) DynamicDriver {
	m2 := *m
	m2.parameters = parameters
	return &m2
}

func (m *manifestDriver) WithStorageClassNameSuffix(suffix string) DynamicDriver {
	m2 := *m
	m2.scSuffix = suffix
	return &m2
}
