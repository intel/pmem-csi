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

	v1 "k8s.io/api/core/v1"
	storagev1 "k8s.io/api/storage/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/kubernetes/test/e2e/framework"
	"k8s.io/kubernetes/test/e2e/framework/skipper"
	storageframework "k8s.io/kubernetes/test/e2e/storage/framework"
	"k8s.io/kubernetes/test/e2e/storage/podlogs"
	"k8s.io/kubernetes/test/e2e/storage/testsuites"

	. "github.com/onsi/ginkgo/v2"
)

var (
	numWorkers = flag.Int("pmem.binding.workers", 10, "number of worker creating volumes in parallel and thus also the maximum number of volumes at any time")
	numVolumes = flag.Int("pmem.binding.volumes", 100, "number of total volumes to create")
)

// This is for testing the driver itself and does not run tests with a driver deployed
// with the operator. For tests that really should run for all deployment methods see
// "Deployment" in pmem_csi.go.
var _ = deploy.DescribeForSome("Driver", deploy.RunAllTests, func(d *deploy.Deployment) {
	csiTestDriver := driver.New(d.Name(), d.DriverName, nil, nil, nil)

	Context("AppDirect", func() {
		// List of testSuites to be added below.
		var csiTestSuites = []func() storageframework.TestSuite{
			// TODO: investigate how useful these tests are and enable them.
			// testsuites.InitMultiVolumeTestSuite,
			testsuites.InitProvisioningTestSuite,
			// testsuites.InitSnapshottableTestSuite,
			// testsuites.InitSubPathTestSuite,
			testsuites.InitVolumeIOTestSuite,
			testsuites.InitVolumeModeTestSuite,
			testsuites.InitVolumesTestSuite,
			func() storageframework.TestSuite {
				return dax.InitDaxTestSuite(true)
			},
		}

		if ephemeral.Supported {
			csiTestSuites = append(csiTestSuites, testsuites.InitEphemeralTestSuite)
		}

		storageframework.DefineTestSuites(csiTestDriver, csiTestSuites)
	})

	Context("FileIO", func() {
		// With FileIO as usage we only test DAX.
		csiTestDriver := driver.New(d.Name(), d.DriverName, []string{"ext4", "xfs"}, map[string]string{
			"ext4": "deploy/common/pmem-storageclass-ext4-fileio.yaml",
			"xfs":  "deploy/common/pmem-storageclass-xfs-fileio.yaml",
		}, map[string]string{
			"usage": "FileIO",
		})
		csiTestSuites := []func() storageframework.TestSuite{
			func() storageframework.TestSuite {
				return dax.InitDaxTestSuite(false)
			},
		}
		storageframework.DefineTestSuites(csiTestDriver, csiTestSuites)
	})
})

var _ = deploy.DescribeForSome("E2E", deploy.RunAllTests, func(d *deploy.Deployment) {
	csiTestDriver := driver.New(d.Name(), d.DriverName, nil, nil, nil)

	// List of testSuites to be added below.
	var csiTestSuites = []func() storageframework.TestSuite{
		// TODO: investigate how useful these tests are and enable them.
		// testsuites.InitMultiVolumeTestSuite,
		testsuites.InitProvisioningTestSuite,
		// testsuites.InitSnapshottableTestSuite,
		// testsuites.InitSubPathTestSuite,
		testsuites.InitVolumeIOTestSuite,
		testsuites.InitVolumeModeTestSuite,
		testsuites.InitVolumesTestSuite,
		func() storageframework.TestSuite {
			return dax.InitDaxTestSuite(true)
		},
	}

	if ephemeral.Supported {
		csiTestSuites = append(csiTestSuites, testsuites.InitEphemeralTestSuite)
	}

	storageframework.DefineTestSuites(csiTestDriver, csiTestSuites)
})

func DefineLateBindingTests(d *deploy.Deployment, f *framework.Framework) {
	Context("late binding", func() {
		var (
			sc    *storagev1.StorageClass
			claim v1.PersistentVolumeClaim
		)

		BeforeEach(func(ctx context.Context) {
			csiTestDriver := driver.New(d.Name(), d.DriverName, nil, nil, nil)
			config := csiTestDriver.PrepareTest(ctx, f)
			sc = csiTestDriver.(storageframework.DynamicPVTestDriver).GetDynamicProvisionStorageClass(ctx, config, "ext4")
			lateBindingMode := storagev1.VolumeBindingWaitForFirstConsumer
			sc.VolumeBindingMode = &lateBindingMode

			// Create or replace storage class.
			err := f.ClientSet.StorageV1().StorageClasses().Delete(context.Background(), sc.Name, metav1.DeleteOptions{})
			if !errors.IsNotFound(err) {
				framework.ExpectNoError(err, "delete old storage class %s", sc.Name)
			}
			_, err = f.ClientSet.StorageV1().StorageClasses().Create(context.Background(), sc, metav1.CreateOptions{})
			framework.ExpectNoError(err, "create storage class %s", sc.Name)
			claim = CreateClaim(f.Namespace.Name, sc.Name)
		})

		AfterEach(func(ctx context.Context) {
			err := f.ClientSet.StorageV1().StorageClasses().Delete(ctx, sc.Name, metav1.DeleteOptions{})
			framework.ExpectNoError(err, "delete old storage class %s", sc.Name)
		})

		It("works", func(ctx context.Context) {
			TestDynamicProvisioning(ctx, f.ClientSet, f.Timeouts, &claim, *sc.VolumeBindingMode, "latebinding")
		})

		Context("unsets unsuitable selected node", func() {
			It("with defaults", func(ctx context.Context) {
				TestReschedule(ctx, f.ClientSet, f.Timeouts, &claim, d.DriverName, "latebinding")
			})

			It("with three replicas", func(ctx context.Context) {
				if !d.HasOperator {
					skipper.Skipf("need PMEM-CSI operator to reconfigure driver")
				}

				c, err := deploy.NewCluster(f.ClientSet, f.DynamicClient, f.ClientConfig())
				framework.ExpectNoError(err, "create cluster")

				By("increase replicas")
				deployment := deploy.GetDeploymentCR(f, d.DriverName)
				oldReplicas := deployment.Spec.ControllerReplicas
				newReplicas := 3
				deployment.Spec.ControllerReplicas = newReplicas
				deploy.UpdateDeploymentCR(f, deployment)
				deploy.WaitForPMEMDriver(c, d, int32(newReplicas))

				defer func() {
					By("reset replicas")
					deployment.Spec.ControllerReplicas = oldReplicas
					deploy.UpdateDeploymentCR(f, deployment)
					if oldReplicas == 0 {
						oldReplicas = 1
					}
					deploy.WaitForPMEMDriver(c, d, int32(oldReplicas))
				}()

				TestReschedule(ctx, f.ClientSet, f.Timeouts, &claim, d.DriverName, "latebinding")
			})
		})

		f.It("stress test", f.WithSlow(), func(ctx context.Context) {
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
			to := podlogs.LogOutput{
				StatusWriter: GinkgoWriter,
				LogWriter:    GinkgoWriter,
			}
			framework.ExpectNoError(podlogs.CopyAllLogs(ctx, f.ClientSet, f.Namespace.Name, to))
			framework.ExpectNoError(podlogs.WatchPods(ctx, f.ClientSet, f.Namespace.Name, GinkgoWriter, nil))

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
						TestDynamicProvisioning(ctx, f.ClientSet, f.Timeouts, &claim, *sc.VolumeBindingMode, id)
					}
				}()
			}
			wg.Wait()
		})
	})
}

func DefineKataTests(d *deploy.Deployment) {
	// Also run some limited tests with Kata Containers, using different
	// storage classes than usual.
	kataDriver := driver.New(d.Name()+"-pmem-csi-kata", d.DriverName,
		[]string{"xfs", "ext4"},
		map[string]string{
			"ext4": "deploy/common/pmem-storageclass-ext4-kata.yaml",
			"xfs":  "deploy/common/pmem-storageclass-xfs-kata.yaml",
		},
		nil,
	)
	Context("Kata Containers", func() {
		storageframework.DefineTestSuites(kataDriver, []func() storageframework.TestSuite{
			func() storageframework.TestSuite {
				return dax.InitDaxTestSuite(true)
			},
		})
	})
}
