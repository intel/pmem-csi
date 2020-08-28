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
	"sync"
	"sync/atomic"

	"github.com/intel/pmem-csi/test/e2e/deploy"
	"github.com/intel/pmem-csi/test/e2e/driver"
	"github.com/intel/pmem-csi/test/e2e/ephemeral"
	"github.com/intel/pmem-csi/test/e2e/storage/dax"
	"github.com/intel/pmem-csi/test/e2e/storage/scheduler"
	"github.com/intel/pmem-csi/test/e2e/versionskew"

	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/kubernetes/test/e2e/framework"
	"k8s.io/kubernetes/test/e2e/framework/skipper"
	"k8s.io/kubernetes/test/e2e/storage/podlogs"
	"k8s.io/kubernetes/test/e2e/storage/testsuites"

	. "github.com/onsi/ginkgo"
)

var (
	numWorkers = flag.Int("pmem.latebinding.workers", 10, "number of worker creating volumes in parallel and thus also the maximum number of volumes at any time")
	numVolumes = flag.Int("pmem.latebinding.volumes", 100, "number of total volumes to create")
)

var _ = deploy.DescribeForAll("E2E", func(d *deploy.Deployment) {
	csiTestDriver := driver.New(d.Name(), "pmem-csi.intel.com", nil, nil)

	// List of testSuites to be added below.
	var csiTestSuites = []func() testsuites.TestSuite{
		// TODO: investigate how useful these tests are and enable them.
		// testsuites.InitMultiVolumeTestSuite,
		testsuites.InitProvisioningTestSuite,
		// testsuites.InitSnapshottableTestSuite,
		// testsuites.InitSubPathTestSuite,
		testsuites.InitVolumeIOTestSuite,
		testsuites.InitVolumeModeTestSuite,
		testsuites.InitVolumesTestSuite,
		dax.InitDaxTestSuite,
		scheduler.InitSchedulerTestSuite,
		versionskew.InitSkewTestSuite,
	}

	if ephemeral.Supported {
		csiTestSuites = append(csiTestSuites, testsuites.InitEphemeralTestSuite)
	}

	testsuites.DefineTestSuite(csiTestDriver, csiTestSuites)

	Context("late binding", func() {
		var (
			storageClassLateBindingName = "pmem-csi-sc-late-binding" // from deploy/common/pmem-storageclass-late-binding.yaml
			claim                       v1.PersistentVolumeClaim
		)
		f := framework.NewDefaultFramework("latebinding")
		BeforeEach(func() {
			// Check whether storage class exists before trying to use it.
			_, err := f.ClientSet.StorageV1().StorageClasses().Get(context.Background(), storageClassLateBindingName, metav1.GetOptions{})
			if errors.IsNotFound(err) {
				skipper.Skipf("storage class %s not found, late binding not supported", storageClassLateBindingName)
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

	// Also run some limited tests with Kata Containers, using different
	// storage classes than usual.
	kataDriver := driver.New(d.Name()+"-pmem-csi-kata", "pmem-csi.intel.com",
		[]string{"xfs", "ext4"},
		map[string]string{
			"ext4": "deploy/common/pmem-storageclass-ext4-kata.yaml",
			"xfs":  "deploy/common/pmem-storageclass-xfs-kata.yaml",
		},
	)
	Context("Kata Containers", func() {
		testsuites.DefineTestSuite(kataDriver, []func() testsuites.TestSuite{
			dax.InitDaxTestSuite,
		})
	})
})
