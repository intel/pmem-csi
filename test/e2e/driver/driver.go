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
	e2evolume "k8s.io/kubernetes/test/e2e/framework/volume"
	"k8s.io/kubernetes/test/e2e/storage/testpatterns"
	"k8s.io/kubernetes/test/e2e/storage/testsuites"
	"k8s.io/kubernetes/test/e2e/storage/utils"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
)

func New(name, csiDriverName string, fsTypes ...string) testsuites.TestDriver {
	if len(fsTypes) == 0 {
		fsTypes = []string{"", "ext4", "xfs"}
	}
	return &manifestDriver{
		driverInfo: testsuites.DriverInfo{
			Name:            name,
			MaxFileSize:     testpatterns.FileSizeMedium,
			SupportedFsType: sets.NewString(fsTypes...),
			Capabilities: map[testsuites.Capability]bool{
				testsuites.CapPersistence: true,
				testsuites.CapFsGroup:     true,
				testsuites.CapExec:        true,
				testsuites.CapBlock:       true,
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
		scManifest: map[string]string{
			"":     "deploy/common/pmem-storageclass-ext4.yaml",
			"ext4": "deploy/common/pmem-storageclass-ext4.yaml",
			"xfs":  "deploy/common/pmem-storageclass-xfs.yaml",
		},
		csiDriverName: csiDriverName,
	}
}

type manifestDriver struct {
	driverInfo    testsuites.DriverInfo
	csiDriverName string
	patchOptions  utils.PatchCSIOptions
	manifests     []string
	scManifest    map[string]string
	cleanup       func()
}

var _ testsuites.TestDriver = &manifestDriver{}
var _ testsuites.DynamicPVTestDriver = &manifestDriver{}

func (m *manifestDriver) GetDriverInfo() *testsuites.DriverInfo {
	return &m.driverInfo
}

func (m *manifestDriver) SkipUnsupportedTest(testpatterns.TestPattern) {
}

func (m *manifestDriver) GetDynamicProvisionStorageClass(config *testsuites.PerTestConfig, fsType string) *storagev1.StorageClass {
	f := config.Framework

	scManifest, ok := m.scManifest[fsType]
	Expect(ok).To(BeTrue(), "Unsupported filesystem type %s", fsType)

	items, err := utils.LoadFromManifests(scManifest)
	Expect(err).NotTo(HaveOccurred())
	Expect(len(items)).To(Equal(1), "exactly one item from %s", scManifest)

	err = utils.PatchItems(f, items...)
	Expect(err).NotTo(HaveOccurred())
	err = utils.PatchCSIDeployment(f, m.finalPatchOptions(f), items[0])

	sc, ok := items[0].(*storagev1.StorageClass)
	Expect(ok).To(BeTrue(), "storage class from %s", scManifest)
	sc.Provisioner = m.csiDriverName
	return sc
}

func (m *manifestDriver) PrepareTest(f *framework.Framework) (*testsuites.PerTestConfig, func()) {
	config := &testsuites.PerTestConfig{
		Driver:    m,
		Prefix:    "pmem",
		Framework: f,
	}
	if len(m.manifests) == 0 {
		// Nothing todo.
		return config, func() {}
	}

	By(fmt.Sprintf("deploying %s driver", m.driverInfo.Name))
	cleanup, err := utils.CreateFromManifests(f, func(item interface{}) error {
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

func (m *manifestDriver) GetVolume(config *testsuites.PerTestConfig, volumeNumber int) (map[string]string, bool, bool) {
	attributes := map[string]string{"size": m.driverInfo.SupportedSizeRange.Min}
	shared := false
	readOnly := false

	return attributes, shared, readOnly
}

func (m *manifestDriver) GetCSIDriverName(config *testsuites.PerTestConfig) string {
	// Return real driver name.
	// We can't use m.driverInfo.Name as its not necessarily the real driver name
	return m.csiDriverName
}
