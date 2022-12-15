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
	"sync"
	"sync/atomic"

	"github.com/intel/pmem-csi/test/e2e/deploy"
	"github.com/intel/pmem-csi/test/e2e/driver"

	v1 "k8s.io/api/core/v1"
	storagev1 "k8s.io/api/storage/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/kubernetes/test/e2e/framework"
	storageframework "k8s.io/kubernetes/test/e2e/storage/framework"

	. "github.com/onsi/ginkgo/v2"
)

func DefineImmediateBindingTests(d *deploy.Deployment, f *framework.Framework) {
	Context("immediate binding", func() {
		var (
			sc    *storagev1.StorageClass
			claim v1.PersistentVolumeClaim
		)

		BeforeEach(func() {
			csiTestDriver := driver.New(d.Name(), d.DriverName, nil, nil, nil)
			config := csiTestDriver.PrepareTest(f)
			sc = csiTestDriver.(storageframework.DynamicPVTestDriver).GetDynamicProvisionStorageClass(config, "ext4")
			immediateBindingMode := storagev1.VolumeBindingImmediate
			sc.VolumeBindingMode = &immediateBindingMode

			// Create or replace storage class.
			err := f.ClientSet.StorageV1().StorageClasses().Delete(context.Background(), sc.Name, metav1.DeleteOptions{})
			if !errors.IsNotFound(err) {
				framework.ExpectNoError(err, "delete old storage class %s", sc.Name)
			}
			_, err = f.ClientSet.StorageV1().StorageClasses().Create(context.Background(), sc, metav1.CreateOptions{})
			framework.ExpectNoError(err, "create storage class %s", sc.Name)

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
					StorageClassName: &sc.Name,
				},
			}
		})

		AfterEach(func() {
			err := f.ClientSet.StorageV1().StorageClasses().Delete(context.Background(), sc.Name, metav1.DeleteOptions{})
			framework.ExpectNoError(err, "delete old storage class %s", sc.Name)
		})

		It("works", func() {
			TestDynamicProvisioning(f.ClientSet, f.Timeouts, &claim, *sc.VolumeBindingMode, "immediatebinding")
		})

		It("stress test [Slow]", func() {
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
						TestDynamicProvisioning(f.ClientSet, f.Timeouts, &claim, *sc.VolumeBindingMode, id)
					}
				}()
			}
			wg.Wait()
		})
	})
}
