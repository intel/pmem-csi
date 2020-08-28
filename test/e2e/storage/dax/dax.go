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

package dax

import (
	"fmt"
	"os"
	"os/exec"
	"strings"

	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/kubernetes/test/e2e/framework"
	e2epod "k8s.io/kubernetes/test/e2e/framework/pod"
	"k8s.io/kubernetes/test/e2e/framework/volume"
	"k8s.io/kubernetes/test/e2e/storage/testpatterns"
	"k8s.io/kubernetes/test/e2e/storage/testsuites"

	"github.com/intel/pmem-csi/test/e2e/ephemeral"
	pmempod "github.com/intel/pmem-csi/test/e2e/pod"

	. "github.com/onsi/ginkgo"
)

type daxTestSuite struct {
	tsInfo testsuites.TestSuiteInfo
}

var _ testsuites.TestSuite = &daxTestSuite{}

// InitDaxTestSuite returns daxTestSuite that implements TestSuite interface
func InitDaxTestSuite() testsuites.TestSuite {
	suite := &daxTestSuite{
		tsInfo: testsuites.TestSuiteInfo{
			Name: "dax",
			TestPatterns: []testpatterns.TestPattern{
				testpatterns.DefaultFsDynamicPV,
				testpatterns.Ext4DynamicPV,
				testpatterns.XfsDynamicPV,

				testpatterns.BlockVolModeDynamicPV,
			},
		},
	}
	if ephemeral.Supported {
		suite.tsInfo.TestPatterns = append(suite.tsInfo.TestPatterns,
			testpatterns.DefaultFsCSIEphemeralVolume,
			testpatterns.Ext4CSIEphemeralVolume,
			testpatterns.XfsCSIEphemeralVolume,
		)
	}
	return suite
}

func (p *daxTestSuite) GetTestSuiteInfo() testsuites.TestSuiteInfo {
	return p.tsInfo
}

func (p *daxTestSuite) SkipRedundantSuite(driver testsuites.TestDriver, pattern testpatterns.TestPattern) {
}

type local struct {
	config      *testsuites.PerTestConfig
	testCleanup func()

	resource *testsuites.VolumeResource
	root     string
}

const (
	daxCheckBinary = "_work/pmem-dax-check"
)

func (p *daxTestSuite) DefineTests(driver testsuites.TestDriver, pattern testpatterns.TestPattern) {
	var l local

	f := framework.NewDefaultFramework("dax")

	init := func() {
		l = local{}

		// Build pmem-dax-check helper binary.
		l.root = os.Getenv("REPO_ROOT")
		build := exec.Command("/bin/sh", "-c", os.Getenv("GO")+" build -o "+daxCheckBinary+" ./test/cmd/pmem-dax-check")
		build.Stdout = GinkgoWriter
		build.Stderr = GinkgoWriter
		build.Dir = l.root
		By("Compiling with: " + strings.Join(build.Args, " "))
		err := build.Run()
		framework.ExpectNoError(err, "compile ./test/cmd/pmem-dax-check")

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

	withKataContainers := strings.HasSuffix(driver.GetDriverInfo().Name, "-kata")

	It("should support MAP_SYNC", func() {
		init()
		defer cleanup()

		testDaxInPod(f, l.root, l.resource.Pattern.VolMode, l.resource.VolSource, l.config, withKataContainers)
	})
}

func testDaxInPod(
	f *framework.Framework,
	root string,
	volumeMode v1.PersistentVolumeMode,
	source *v1.VolumeSource,
	config *testsuites.PerTestConfig,
	withKataContainers bool,
) {
	pod := CreatePod(f, "dax-volume-test", volumeMode, source, config, withKataContainers)
	defer func() {
		DeletePod(f, pod)
	}()
	checkWithNormalRuntime := testDax(f, pod, root, volumeMode, source, withKataContainers)
	DeletePod(f, pod)
	if checkWithNormalRuntime {
		testDaxOutside(f, pod, root)
	}
}

func CreatePod(
	f *framework.Framework,
	name string,
	volumeMode v1.PersistentVolumeMode,
	source *v1.VolumeSource,
	config *testsuites.PerTestConfig,
	withKataContainers bool,
) *v1.Pod {
	const (
		volPath       = "/vol1"
		volName       = "vol1"
		containerName = "dax-container"
	)
	privileged := volumeMode == v1.PersistentVolumeBlock
	root := int64(0)
	pod := &v1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: f.Namespace.Name,
		},
		Spec: v1.PodSpec{
			Containers: []v1.Container{
				{
					SecurityContext: &v1.SecurityContext{
						Privileged: &privileged,
						RunAsUser:  &root,
						RunAsGroup: &root,
					},
					Name:    containerName,
					Image:   os.Getenv("PMEM_CSI_IMAGE"),
					Command: []string{"sleep", "1000000"},
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
	if withKataContainers {
		pod.Name += "-kata"
		runtimeClassName := "kata-qemu"
		pod.Spec.RuntimeClassName = &runtimeClassName
		pod.Spec.NodeSelector = map[string]string{
			"katacontainers.io/kata-runtime": "true",
		}
		if pod.Labels == nil {
			pod.Labels = map[string]string{}
		}
		pod.Labels["io.katacontainers.config.hypervisor.memory_offset"] = "2147483648" // large enough for all test volumes
	} else {
		e2epod.SetNodeSelection(&pod.Spec, config.ClientNodeSelection)
	}
	switch volumeMode {
	case v1.PersistentVolumeBlock:
		// This is what we would like to use:
		//
		// pod.Spec.Containers[0].VolumeDevices = append(pod.Spec.Containers[0].VolumeDevices,
		// 	v1.VolumeDevice{
		// 		Name:       volName,
		// 		DevicePath: "/dax-dev",
		// 	})

		// But because of https://github.com/kubernetes/kubernetes/issues/85624, /dax-dev
		// then is silently ignored.
		//
		// Instead we have to use the workaround mentioned in that issue:
		// - bring up an unprivileged init container with /dax-dev and a shared empty volume on /data
		// - get major/minor number of that device and put it into a script
		// - re-create the device in the privileged container with that script
		emptyDirName := "data"
		pod.Spec.InitContainers = []v1.Container{
			{
				Name:  "copy-dax-dev",
				Image: os.Getenv("PMEM_CSI_IMAGE"),
				Command: []string{"sh", "-c",
					"(echo '#!/bin/sh' && stat --format 'mknod /dax-dev b 0x%t 0x%T' /dax-dev) >/data/create-dax-dev.sh && chmod a+x /data/create-dax-dev.sh",
				},
				SecurityContext: &v1.SecurityContext{
					RunAsUser:  &root,
					RunAsGroup: &root,
				},
				VolumeMounts: []v1.VolumeMount{
					{
						Name:      emptyDirName,
						MountPath: "/data",
					},
				},
				VolumeDevices: []v1.VolumeDevice{
					{
						Name:       volName,
						DevicePath: "/dax-dev",
					},
				},
			},
		}
		pod.Spec.Containers[0].VolumeMounts = append(pod.Spec.Containers[0].VolumeMounts,
			v1.VolumeMount{
				Name:      emptyDirName,
				MountPath: "/data",
			},
		)
		pod.Spec.Volumes = append(pod.Spec.Volumes, v1.Volume{
			Name: emptyDirName,
			VolumeSource: v1.VolumeSource{
				EmptyDir: &v1.EmptyDirVolumeSource{},
			},
		})
	default:
		pod.Spec.Containers[0].VolumeMounts = append(pod.Spec.Containers[0].VolumeMounts,
			v1.VolumeMount{
				Name:      volName,
				MountPath: "/mnt",
			},
		)
	}

	By(fmt.Sprintf("Creating pod %s", pod.Name))
	ns := f.Namespace.Name
	podClient := f.PodClientNS(ns)
	createdPod := podClient.Create(pod)
	podErr := e2epod.WaitForPodRunningInNamespace(f.ClientSet, createdPod)
	framework.ExpectNoError(podErr, "running pod")

	return createdPod
}

func testDax(
	f *framework.Framework,
	pod *v1.Pod,
	root string,
	volumeMode v1.PersistentVolumeMode,
	source *v1.VolumeSource,
	withKataContainers bool,
) bool {
	ns := f.Namespace.Name
	containerName := pod.Spec.Containers[0].Name
	if volumeMode == v1.PersistentVolumeBlock {
		By("mounting raw block device")
		// TODO: remove the workaround above and script invocation here.
		pmempod.RunInPod(f, root, nil, "/data/create-dax-dev.sh && mkfs.ext4 -b 4096 /dax-dev && mkdir -p /mnt && mount -odax /dax-dev /mnt", ns, pod.Name, containerName)
	}

	By("checking that missing DAX support is detected")
	pmempod.RunInPod(f, root, []string{daxCheckBinary}, daxCheckBinary+" /no-dax; if [ $? -ne 1 ]; then echo should have reported missing DAX >&2; exit 1; fi", ns, pod.Name, containerName)

	By("checking volume for DAX support")
	pmempod.RunInPod(f, root, []string{daxCheckBinary}, "lsblk; mount | grep /mnt; "+daxCheckBinary+" /mnt/daxtest", ns, pod.Name, containerName)

	// Data written in a container running under Kata Containers
	// should be visible also in a normal container, unless the
	// volume itself is ephemeral of course.  We currently don't
	// have DAX support there, though.
	checkWithNormalRuntime := withKataContainers && source.CSI == nil
	if checkWithNormalRuntime {
		By("creating file for usage under normal pod")
		pmempod.RunInPod(f, root, nil, "touch /mnt/hello-world", ns, pod.Name, containerName)
	}

	return checkWithNormalRuntime
}

func DeletePod(
	f *framework.Framework,
	pod *v1.Pod,
) {
	By(fmt.Sprintf("Deleting pod %s", pod.Name))
	err := e2epod.DeletePodWithWait(f.ClientSet, pod)
	framework.ExpectNoError(err, "while deleting pod")
}

func testDaxOutside(
	f *framework.Framework,
	pod *v1.Pod,
	root string,
) {
	// Check for data written earlier.
	pod.Spec.RuntimeClassName = nil
	pod.Name = "data-volume-test"

	By(fmt.Sprintf("Creating pod %s", pod.Name))
	ns := f.Namespace.Name
	podClient := f.PodClientNS(ns)
	pod = podClient.Create(pod)
	podErr := e2epod.WaitForPodRunningInNamespace(f.ClientSet, pod)
	framework.ExpectNoError(podErr, "running second pod")
	By("checking for previously created file under normal pod")
	containerName := pod.Spec.Containers[0].Name
	pmempod.RunInPod(f, root, nil, "ls -l /mnt/hello-world", ns, pod.Name, containerName)

	By(fmt.Sprintf("Deleting pod %s", pod.Name))
	err := e2epod.DeletePodWithWait(f.ClientSet, pod)
	framework.ExpectNoError(err, "while deleting pod")
}
