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

package storage

import (
	"context"
	"fmt"
	"strings"

	storagev1 "k8s.io/api/storage/v1"
	"k8s.io/apimachinery/pkg/util/sets"
	clientset "k8s.io/client-go/kubernetes"
	"k8s.io/kubernetes/test/e2e/framework"
	"k8s.io/kubernetes/test/e2e/framework/podlogs"
	"k8s.io/kubernetes/test/e2e/storage/testpatterns"
	"k8s.io/kubernetes/test/e2e/storage/testsuites"
	"k8s.io/kubernetes/test/e2e/storage/utils"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
)

func csiTunePattern(patterns []testpatterns.TestPattern) []testpatterns.TestPattern {
	tunedPatterns := []testpatterns.TestPattern{}

	for _, pattern := range patterns {
		// Skip inline volume and pre-provsioned PV tests for csi drivers
		if pattern.VolType == testpatterns.InlineVolume || pattern.VolType == testpatterns.PreprovisionedPV {
			continue
		}
		tunedPatterns = append(tunedPatterns, pattern)
	}

	return tunedPatterns
}

var _ = Describe("PMEM Volumes", func() {
	f := framework.NewDefaultFramework("pmem")

	var (
		cs     clientset.Interface
		cancel context.CancelFunc
	)

	BeforeEach(func() {
		cs = f.ClientSet
		// Must be done this way to keep "go vet" happy.
		ctx, cncl := context.WithCancel(context.Background())
		cancel = cncl

		// This assumes that the pmem-csi driver got installed in the default namespace.
		to := podlogs.LogOutput{
			StatusWriter: GinkgoWriter,
			LogWriter:    GinkgoWriter,
		}
		podlogs.CopyAllLogs(ctx, cs, "default", to)
		podlogs.WatchPods(ctx, cs, "default", GinkgoWriter)
	})

	AfterEach(func() {
		if cancel != nil {
			cancel()
		}
	})

	// List of testDrivers to be executed in below loop
	var csiTestDrivers = []func() testsuites.TestDriver{
		// pmem-csi
		func() testsuites.TestDriver {
			return &manifestDriver{
				driverInfo: testsuites.DriverInfo{
					Name:        "pmem-csi",
					MaxFileSize: testpatterns.FileSizeMedium,
					SupportedFsType: sets.NewString(
						"", // Default fsType
					),
					Capabilities: map[testsuites.Capability]bool{
						testsuites.CapPersistence: true,
						testsuites.CapFsGroup:     true,
						testsuites.CapExec:        true,
					},

					Config: testsuites.TestConfig{
						Framework: f,
						Prefix:    "pmem",
						// Ensure that all pods land on the same node. Works around
						// https://github.com/intel/pmem-csi/issues/132.
						ClientNodeName: "host-1",
					},
				},
				scManifest: "deploy/kubernetes-1.13/pmem-storageclass.yaml",
				// Renaming of the driver *not* enabled. It doesn't support
				// that because there is only one instance of the registry
				// and on each node the driver assumes that it has exclusive
				// control of the PMEM. As a result, tests have to be run
				// sequentially becaust each test creates and removes
				// the driver deployment.
				claimSize: "1Mi",
			}
		},
	}

	// List of testSuites to be executed in below loop
	var csiTestSuites = []func() testsuites.TestSuite{
		// TODO: investigate how useful these tests are and enable them.
		// testsuites.InitVolumesTestSuite,
		// testsuites.InitVolumeIOTestSuite,
		// testsuites.InitVolumeModeTestSuite,
		// testsuites.InitSubPathTestSuite,
		testsuites.InitProvisioningTestSuite,
	}

	for _, initDriver := range csiTestDrivers {
		curDriver := initDriver()
		Context(testsuites.GetDriverNameWithFeatureTags(curDriver), func() {
			driver := curDriver

			BeforeEach(func() {
				// setupDriver
				driver.CreateDriver()
			})

			AfterEach(func() {
				// Cleanup driver
				driver.CleanupDriver()
			})

			testsuites.RunTestSuite(f, driver, csiTestSuites, csiTunePattern)
		})
	}
})

type manifestDriver struct {
	driverInfo   testsuites.DriverInfo
	patchOptions utils.PatchCSIOptions
	manifests    []string
	scManifest   string
	claimSize    string
	cleanup      func()
}

var _ testsuites.TestDriver = &manifestDriver{}
var _ testsuites.DynamicPVTestDriver = &manifestDriver{}

func (m *manifestDriver) GetDriverInfo() *testsuites.DriverInfo {
	return &m.driverInfo
}

func (m *manifestDriver) SkipUnsupportedTest(testpatterns.TestPattern) {
}

func (m *manifestDriver) GetDynamicProvisionStorageClass(fsType string) *storagev1.StorageClass {
	f := m.driverInfo.Config.Framework

	items, err := f.LoadFromManifests(m.scManifest)
	Expect(err).NotTo(HaveOccurred())
	Expect(len(items)).To(Equal(1), "exactly one item from %s", m.scManifest)

	err = f.PatchItems(items...)
	Expect(err).NotTo(HaveOccurred())
	err = utils.PatchCSIDeployment(f, m.finalPatchOptions(), items[0])

	sc, ok := items[0].(*storagev1.StorageClass)
	Expect(ok).To(BeTrue(), "storage class from %s", m.scManifest)
	return sc
}

func (m *manifestDriver) GetClaimSize() string {
	return m.claimSize
}

func (m *manifestDriver) CreateDriver() {
	By(fmt.Sprintf("deploying %s driver", m.driverInfo.Name))
	f := m.driverInfo.Config.Framework

	// TODO (?): the storage.csi.image.version and storage.csi.image.registry
	// settings are ignored for this test. We could patch the image definitions.
	cleanup, err := f.CreateFromManifests(func(item interface{}) error {
		return utils.PatchCSIDeployment(f, m.finalPatchOptions(), item)
	},
		m.manifests...,
	)
	m.cleanup = cleanup
	if err != nil {
		framework.Failf("deploying %s driver: %v", m.driverInfo.Name, err)
	}
}

func (m *manifestDriver) CleanupDriver() {
	if m.cleanup != nil {
		By(fmt.Sprintf("uninstalling %s driver", m.driverInfo.Name))
		m.cleanup()
	}
}

func (m *manifestDriver) finalPatchOptions() utils.PatchCSIOptions {
	o := m.patchOptions
	// Unique name not available yet when configuring the driver.
	if strings.HasSuffix(o.NewDriverName, "-") {
		o.NewDriverName += m.driverInfo.Config.Framework.UniqueName
	}
	return o
}
