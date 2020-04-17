/*
Copyright 2018 The Kubernetes Authors.

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

package scheduler

import (
	"fmt"
	"os"

	v1 "k8s.io/api/core/v1"
	storagev1 "k8s.io/api/storage/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/kubernetes/test/e2e/framework"
	e2epod "k8s.io/kubernetes/test/e2e/framework/pod"
	"k8s.io/kubernetes/test/e2e/framework/volume"
	"k8s.io/kubernetes/test/e2e/storage/testpatterns"
	"k8s.io/kubernetes/test/e2e/storage/testsuites"

	"github.com/intel/pmem-csi/test/e2e/ephemeral"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
)

type schedulerTestSuite struct {
	tsInfo testsuites.TestSuiteInfo
}

var _ testsuites.TestSuite = &schedulerTestSuite{}

// InitSchedulerTestSuite returns a test suite which verifies that the scheduler extender and
// webhook work.
func InitSchedulerTestSuite() testsuites.TestSuite {
	// We test with an ephemeral inline volume and a PVC with late
	// binding. The webhook works reliably only for the inline
	// volume. With PVCs there are race conditions (PVC created,
	// but controller not informed yet when webhook is called), so
	// we may have to wait until eventually it works.
	lateBinding := testpatterns.DefaultFsDynamicPV
	lateBinding.BindingMode = storagev1.VolumeBindingWaitForFirstConsumer

	suite := &schedulerTestSuite{
		tsInfo: testsuites.TestSuiteInfo{
			Name: "scheduler",
			TestPatterns: []testpatterns.TestPattern{
				lateBinding,
			},
		},
	}
	if ephemeral.Supported {
		suite.tsInfo.TestPatterns = append(suite.tsInfo.TestPatterns,
			testpatterns.DefaultFsEphemeralVolume,
		)
	}
	return suite
}

func (p *schedulerTestSuite) GetTestSuiteInfo() testsuites.TestSuiteInfo {
	return p.tsInfo
}

func (p *schedulerTestSuite) SkipRedundantSuite(driver testsuites.TestDriver, pattern testpatterns.TestPattern) {
}

type local struct {
	config      *testsuites.PerTestConfig
	testCleanup func()

	resource *testsuites.VolumeResource
}

func (p *schedulerTestSuite) DefineTests(driver testsuites.TestDriver, pattern testpatterns.TestPattern) {
	var l local

	f := framework.NewDefaultFramework("scheduler")

	init := func() {
		l = local{}

		// Now do the more expensive test initialization.
		l.config, l.testCleanup = driver.PrepareTest(f)
		l.resource = testsuites.CreateVolumeResource(driver, l.config, pattern, volume.SizeRange{})
	}

	cleanup := func() {
		if l.resource != nil {
			l.resource.CleanupResource()
			l.resource = nil
		}

		if l.testCleanup != nil {
			l.testCleanup()
			l.testCleanup = nil
		}
	}

	It("should call PMEM-CSI controller", func() {
		init()
		defer cleanup()

		l.testSchedulerInPod(f, l.resource.Pattern.VolType, l.resource.VolSource, l.config)
	})
}

func (l local) testSchedulerInPod(
	f *framework.Framework,
	volumeType testpatterns.TestVolType,
	source *v1.VolumeSource,
	config *testsuites.PerTestConfig) {

	const (
		volPath       = "/vol1"
		volName       = "vol1"
		containerName = "scheduler-container"
	)
	pod := &v1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "dax-volume-test",
			Namespace: f.Namespace.Name,
		},
		Spec: v1.PodSpec{
			Containers: []v1.Container{
				{
					Name:    containerName,
					Image:   os.Getenv("PMEM_CSI_IMAGE"),
					Command: []string{"sleep", "1000000"},
					VolumeMounts: []v1.VolumeMount{
						v1.VolumeMount{
							Name:      volName,
							MountPath: "/mnt",
						},
					},
				},
			},
			Volumes: []v1.Volume{
				{
					Name:         volName,
					VolumeSource: *source,
				},
			},
			RestartPolicy: v1.RestartPolicyNever,
		},
	}
	e2epod.SetNodeSelection(&pod.Spec, config.ClientNodeSelection)

	By(fmt.Sprintf("Creating pod %s", pod.Name))
	ns := f.Namespace.Name
	podClient := f.PodClientNS(ns)
	createdPod := podClient.Create(pod)
	defer func() {
		By("delete the pod")
		podClient.DeleteSync(createdPod.Name, metav1.DeleteOptions{}, framework.DefaultPodDeletionTimeout)
	}()

	Expect(createdPod.Spec.Containers[0].Resources).NotTo(BeNil(), "pod resources")
	Expect(createdPod.Spec.Containers[0].Resources.Requests).NotTo(BeNil(), "pod resource requests")
	_, ok := createdPod.Spec.Containers[0].Resources.Requests["pmem-csi.intel.com/scheduler"]
	Expect(ok).To(BeTrue(), "PMEM-CSI extended resource request")
	Expect(createdPod.Spec.Containers[0].Resources.Limits).NotTo(BeNil(), "pod resource requests")
	_, ok = createdPod.Spec.Containers[0].Resources.Requests["pmem-csi.intel.com/scheduler"]
	Expect(ok).To(BeTrue(), "PMEM-CSI extended resource limit")

	podErr := e2epod.WaitForPodRunningInNamespace(f.ClientSet, createdPod)
	framework.ExpectNoError(podErr, "running pod")

	// If we get here, we know that the scheduler extender
	// worked. If it wasn't active, kube-scheduler would have
	// tried to handle pmem-csi.intel.com/scheduler itself, which
	// can't work because there is no node provising that
	// resource.

	By(fmt.Sprintf("Deleting pod %s", pod.Name))
	err := e2epod.DeletePodWithWait(f.ClientSet, pod)
	framework.ExpectNoError(err, "while deleting pod")
}
