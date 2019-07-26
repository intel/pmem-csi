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
	"flag"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"

	v1 "k8s.io/api/core/v1"
	storagev1 "k8s.io/api/storage/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/sets"
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
	// List of testDrivers to be executed in below loop
	var csiTestDrivers = []func() testsuites.TestDriver{
		// pmem-csi
		func() testsuites.TestDriver {
			return &manifestDriver{
				driverInfo: testsuites.DriverInfo{
					Name:        "pmem-csi",
					MaxFileSize: testpatterns.FileSizeMedium,
					SupportedFsType: sets.NewString(
						"ext4", "xfs",
					),
					Capabilities: map[testsuites.Capability]bool{
						testsuites.CapPersistence: true,
						testsuites.CapFsGroup:     true,
						testsuites.CapExec:        true,
						testsuites.CapBlock:       true,
					},
				},
				scManifest: map[string]string{
					"ext4": "deploy/kubernetes-1.13/pmem-storageclass-ext4.yaml",
					"xfs":  "deploy/kubernetes-1.13/pmem-storageclass-xfs.yaml",
				},
				// We use 16Mi size volumes because this is the minimum size supported
				// by xfs filesystem's allocation group
				// Ref: http://man7.org/linux/man-pages/man8/mkfs.xfs.8.html
				claimSize: "110Mi",
				// VolumeIO test suite requires at least 102 MB volume.
			}
		},
	}

	// List of testSuites to be executed in below loop
	var csiTestSuites = []func() testsuites.TestSuite{
		// TODO: investigate how useful these tests are and enable them.
		// testsuites.InitMultiVolumeTestSuite,
		testsuites.InitProvisioningTestSuite,
		// testsuites.InitSnapshottableTestSuite,
		// testsuites.InitSubPathTestSuite,
		testsuites.InitVolumeIOTestSuite,
		testsuites.InitVolumeModeTestSuite,
		testsuites.InitVolumesTestSuite,
	}

	for _, initDriver := range csiTestDrivers {
		curDriver := initDriver()
		Context(testsuites.GetDriverNameWithFeatureTags(curDriver), func() {
			testsuites.DefineTestSuite(curDriver, csiTestSuites)
		})
	}

	Context("late binding", func() {
		var (
			storageClassLateBindingName = "pmem-csi-sc-late-binding" // from deploy/common/pmem-storageclass-late-binding.yaml
			claim                       v1.PersistentVolumeClaim
		)
		f := framework.NewDefaultFramework("latebinding")
		BeforeEach(func() {
			// Check whether storage class exists before trying to use it.
			_, err := f.ClientSet.StorageV1().StorageClasses().Get(storageClassLateBindingName, metav1.GetOptions{})
			if errors.IsNotFound(err) {
				framework.Skipf("storage class %s not found, late binding not supported", storageClassLateBindingName)
			}
			framework.ExpectNoError(err, "get storage class %s", storageClassLateBindingName)

			claim = v1.PersistentVolumeClaim{
				ObjectMeta: metav1.ObjectMeta{
					GenerateName: "pvc-",
					Namespace:    f.Namespace.Name,
				},
				Spec: v1.PersistentVolumeClaimSpec{
					AccessModes: []v1.PersistentVolumeAccessMode{
						v1.ReadWriteOnce,
					},
					Resources: v1.ResourceRequirements{
						Requests: v1.ResourceList{
							v1.ResourceName(v1.ResourceStorage): resource.MustParse("1Mi"),
						},
					},
					StorageClassName: &storageClassLateBindingName,
				},
			}
		})

		It("works", func() {
			TestDynamicLateBindingProvisioning(f.ClientSet, &claim, "latebinding")
		})

		var (
			numWorkers = flag.Int("pmem.latebinding.workers", 10, "number of worker creating volumes in parallel and thus also the maximum number of volumes at any time")
			numVolumes = flag.Int("pmem.latebinding.volumes", 100, "number of total volumes to create")
		)

		// This test is pending because pod startup itself failed
		// occasionally for reasons that are out of our control
		// (https://github.com/clearlinux/distribution/issues/966).
		PIt("stress test", func() {
			// We cannot test directly whether pod and
			// volume were created on the same node by
			// chance or because the code enforces it.
			// But if it works reliably under load, then
			// we can be reasonably sure that it works not
			// by chance.
			//
			// The load here consists of n workers which
			// create and test volumes in parallel until
			// we've tested m volumes.

			// Because this test creates a lot of pods, it is useful to
			// log their progress.
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			to := podlogs.LogOutput{
				StatusWriter: GinkgoWriter,
				LogWriter:    GinkgoWriter,
			}
			podlogs.CopyAllLogs(ctx, f.ClientSet, f.Namespace.Name, to)
			podlogs.WatchPods(ctx, f.ClientSet, f.Namespace.Name, GinkgoWriter)

			wg := sync.WaitGroup{}
			volumes := int64(0)
			wg.Add(*numWorkers)
			for i := 0; i < *numWorkers; i++ {
				i := i
				go func() {
					defer wg.Done()
					defer GinkgoRecover()

					for {
						volume := atomic.AddInt64(&volumes, 1)
						if volume > int64(*numVolumes) {
							return
						}
						id := fmt.Sprintf("worker-%d-volume-%d", i, volume)
						TestDynamicLateBindingProvisioning(f.ClientSet, &claim, id)
					}
				}()
			}
			wg.Wait()
		})
	})
})

type manifestDriver struct {
	driverInfo   testsuites.DriverInfo
	patchOptions utils.PatchCSIOptions
	manifests    []string
	scManifest   map[string]string
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

func (m *manifestDriver) GetDynamicProvisionStorageClass(config *testsuites.PerTestConfig, fsType string) *storagev1.StorageClass {
	f := config.Framework

	scManifest, ok := m.scManifest[fsType]
	Expect(ok).To(BeTrue(), "Unsupported filesystem type %s", fsType)

	items, err := f.LoadFromManifests(scManifest)
	Expect(err).NotTo(HaveOccurred())
	Expect(len(items)).To(Equal(1), "exactly one item from %s", scManifest)

	err = f.PatchItems(items...)
	Expect(err).NotTo(HaveOccurred())
	err = utils.PatchCSIDeployment(f, m.finalPatchOptions(f), items[0])

	sc, ok := items[0].(*storagev1.StorageClass)
	Expect(ok).To(BeTrue(), "storage class from %s", scManifest)
	return sc
}

func (m *manifestDriver) GetClaimSize() string {
	return m.claimSize
}

func (m *manifestDriver) PrepareTest(f *framework.Framework) (*testsuites.PerTestConfig, func()) {
	By(fmt.Sprintf("deploying %s driver", m.driverInfo.Name))
	config := &testsuites.PerTestConfig{
		Driver:    m,
		Prefix:    "pmem",
		Framework: f,
	}
	cleanup, err := f.CreateFromManifests(func(item interface{}) error {
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
