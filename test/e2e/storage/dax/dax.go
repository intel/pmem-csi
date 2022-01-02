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
	"context"
	"fmt"
	"os"
	"os/exec"
	"path"
	"strings"

	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/kubernetes/test/e2e/framework"
	e2epod "k8s.io/kubernetes/test/e2e/framework/pod"
	"k8s.io/kubernetes/test/e2e/framework/volume"
	storageframework "k8s.io/kubernetes/test/e2e/storage/framework"

	api "github.com/intel/pmem-csi/pkg/apis/pmemcsi/v1beta1"
	"github.com/intel/pmem-csi/test/e2e/deploy"
	"github.com/intel/pmem-csi/test/e2e/ephemeral"
	pmempod "github.com/intel/pmem-csi/test/e2e/pod"

	. "github.com/onsi/ginkgo"
)

type daxTestSuite struct {
	tsInfo storageframework.TestSuiteInfo
}

var _ storageframework.TestSuite = &daxTestSuite{}

// InitDaxTestSuite returns daxTestSuite that implements TestSuite interface
func InitDaxTestSuite() storageframework.TestSuite {
	suite := &daxTestSuite{
		tsInfo: storageframework.TestSuiteInfo{
			Name: "dax",
			TestPatterns: []storageframework.TestPattern{
				storageframework.DefaultFsDynamicPV,
				storageframework.Ext4DynamicPV,
				storageframework.XfsDynamicPV,

				storageframework.BlockVolModeDynamicPV,
			},
		},
	}
	if ephemeral.Supported {
		suite.tsInfo.TestPatterns = append(suite.tsInfo.TestPatterns,
			storageframework.DefaultFsCSIEphemeralVolume,
			storageframework.Ext4CSIEphemeralVolume,
			storageframework.XfsCSIEphemeralVolume,
		)
	}
	return suite
}

func (p *daxTestSuite) GetTestSuiteInfo() storageframework.TestSuiteInfo {
	return p.tsInfo
}

func (p *daxTestSuite) SkipUnsupportedTests(driver storageframework.TestDriver, pattern storageframework.TestPattern) {
}

type local struct {
	config      *storageframework.PerTestConfig
	testCleanup func()

	resource *storageframework.VolumeResource
	root     string
}

const (
	daxCheckBinary = "_work/pmem-dax-check"
)

func (p *daxTestSuite) DefineTests(driver storageframework.TestDriver, pattern storageframework.TestPattern) {
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
		l.resource = storageframework.CreateVolumeResource(driver, l.config, pattern, volume.SizeRange{})
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

		testDaxInPod(f, l.root, l.resource.Pattern.VolMode, l.resource.VolSource, l.config, withKataContainers, l.resource.Pattern.FsType)
	})
}

func testDaxInPod(
	f *framework.Framework,
	root string,
	volumeMode v1.PersistentVolumeMode,
	source *v1.VolumeSource,
	config *storageframework.PerTestConfig,
	withKataContainers bool,
	fstype string,
) {
	expectDax := true
	if withKataContainers {
		nodes, err := f.ClientSet.CoreV1().Nodes().List(context.Background(), metav1.ListOptions{
			LabelSelector: "katacontainers.io/kata-runtime=true",
		})
		framework.ExpectNoError(err, "list nodes")
		if len(nodes.Items) == 0 {
			// This is a simplified version of the full test where we don't
			// attempt to use Kata Containers.
			framework.Logf("no nodes found with Kata Container runtime, skipping testing with it")
			expectDax = false
			withKataContainers = false
		}
	}

	// Workaround for https://github.com/kubernetes/kubernetes/issues/107286:
	// the storage framework should set FSType but doesn't.
	if source.CSI != nil &&
		source.CSI.FSType == nil &&
		fstype != "" {
		source.CSI.FSType = &fstype
	}

	pod := CreatePod(f, "dax-volume-test", volumeMode, source, config, withKataContainers)
	defer func() {
		DeletePod(f, pod)
	}()
	checkWithNormalRuntime := testDax(f, pod, root, volumeMode, source, withKataContainers, expectDax, fstype)
	DeletePod(f, pod)
	if checkWithNormalRuntime {
		testDaxOutside(f, pod, root)
	}
}

func getPod(
	f *framework.Framework,
	name string,
	volumeMode v1.PersistentVolumeMode,
	source *v1.VolumeSource,
	config *storageframework.PerTestConfig,
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
		if pod.Annotations == nil {
			pod.Annotations = map[string]string{}
		}

		// The additional memory range must be large enough for all test volumes.
		// https://github.com/kata-containers/kata-containers/blob/main/docs/how-to/how-to-set-sandbox-config-kata.md#hypervisor-options
		// Must be an uint32.
		pod.Annotations["io.katacontainers.config.hypervisor.memory_offset"] = "2147483648" // 2GiB

		// FSGroup not supported (?) by Kata Containers
		// (https://github.com/intel/pmem-csi/issues/987#issuecomment-858350521),
		// we must run as root.
		pod.Spec.SecurityContext = &v1.PodSecurityContext{
			RunAsUser:  &root,
			RunAsGroup: &root,
		}
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

	if source.CSI != nil || volumeMode == v1.PersistentVolumeBlock {
		// No FSGroup support for CSI ephemeral volumes and need root-privileges for raw block devices.
		pod.Spec.Containers[0].SecurityContext = &v1.SecurityContext{
			Privileged: &privileged,
			RunAsUser:  &root,
			RunAsGroup: &root,
		}
	}
	return pod
}

func CreatePod(f *framework.Framework,
	name string,
	volumeMode v1.PersistentVolumeMode,
	source *v1.VolumeSource,
	config *storageframework.PerTestConfig,
	withKataContainers bool,
) *v1.Pod {
	pod := getPod(f, name, volumeMode, source, config, withKataContainers)

	By(fmt.Sprintf("Creating pod %s", pod.Name))
	ns := f.Namespace.Name
	podClient := f.PodClientNS(ns)
	createdPod := podClient.Create(pod)
	defer func() {
		if r := recover(); r != nil {
			// Delete pod before raising the panic again,
			// because the caller will not do it when this
			// function doesn't return normally.
			DeletePod(f, createdPod)
			panic(r)
		}
	}()
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
	expectDax bool,
	fstype string,
) bool {
	ns := f.Namespace.Name
	containerName := pod.Spec.Containers[0].Name
	if volumeMode == v1.PersistentVolumeBlock {
		By("mounting raw block device")
		// TODO: remove the workaround above and script invocation here.
		pmempod.RunInPod(f, root, nil, "/data/create-dax-dev.sh && mkfs.ext4 -b 4096 /dax-dev && mkdir -p /mnt && mount -odax /dax-dev /mnt", ns, pod.Name, containerName)
	}

	By("checking that missing DAX support is detected")
	pmempod.RunInPod(f, root, []string{daxCheckBinary}, "/tmp/"+path.Base(daxCheckBinary)+" /tmp/no-dax; if [ $? -ne 1 ]; then echo should have reported missing DAX >&2; exit 1; fi", ns, pod.Name, containerName)

	if expectDax {
		By("checking volume for DAX support")
		pmempod.RunInPod(f, root, []string{daxCheckBinary}, "lsblk; mount | grep /mnt; /tmp/"+path.Base(daxCheckBinary)+" /mnt/daxtest", ns, pod.Name, containerName)
		if fstype == "xfs" {
			By("checking volume for extsize 2m")
			// "xfs_io -c extsize" prints "[2097152] /mnt".
			pmempod.RunInPod(f, root, nil, "xfs_io -c extsize /mnt | tee /dev/stderr | grep -q -w 2097152", ns, pod.Name, containerName)
		}
	} else {
		By("checking volume for missing DAX support")
		pmempod.RunInPod(f, root, []string{daxCheckBinary}, "lsblk; mount | grep /mnt; /tmp/"+path.Base(daxCheckBinary)+" /mnt/daxtest; if [ $? -ne 1 ]; then echo should have reported missing DAX >&2; exit 1; fi", ns, pod.Name, containerName)
	}

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

// Hugepage page fault testing part starts here
const (
	accessHugepagesBinary = "_work/pmem-access-hugepages"
)

var _ = deploy.DescribeForSome("dax", func(d *deploy.Deployment) bool {
	// Run these tests for all driver deployments that were created
	// through the operator, where device mode is Direct.
	return !d.HasOperator && d.HasDriver && d.Mode == api.DeviceModeDirect
}, func(d *deploy.Deployment) {
	var l local
	f := framework.NewDefaultFramework("dax")
	init := func() {
		l = local{}

		// Build pmem-access-hugepages helper binary.
		l.root = os.Getenv("REPO_ROOT")
		build := exec.Command("/bin/sh", "-c", os.Getenv("GO")+" build -o "+accessHugepagesBinary+" ./test/cmd/pmem-access-hugepages")
		build.Stdout = GinkgoWriter
		build.Stderr = GinkgoWriter
		build.Dir = l.root
		By("Compiling with: " + strings.Join(build.Args, " "))
		err := build.Run()
		framework.ExpectNoError(err, "compile ./test/cmd/pmem-access-hugepages")
	}

	config := &storageframework.PerTestConfig{
		Driver:    nil,
		Prefix:    "pmem",
		Framework: f,
	}
	fstype := ""
	vsource := v1.VolumeSource{
		CSI: &v1.CSIVolumeSource{
			Driver: "pmem-csi.intel.com",
			FSType: &fstype,
			VolumeAttributes: map[string]string{
				"size": "110Mi",
			},
		},
	}
	It("should cause hugepage paging event with default fs", func() {
		init()
		testHugepageInPod(f, l.root, &vsource, config)
	})
	/* there is issue in kubelet causing panic and retry loop when fsType is set to ext4 or xfs.
		   The following 2 items can be enabled after that gets fixed.
	           https://github.com/kubernetes/kubernetes/issues/102651
		        It("should cause hugepage paging event with ext4", func() {
				init()
				fsExt4 := "ext4"
				vsource.CSI.FSType = &fsExt4
				testHugepageInPod(f, l.root, &vsource, config)
			})
			It("should cause hugepage paging event with xfs", func() {
				init()
				fsXFS := "xfs"
				vsource.CSI.FSType = &fsXFS
				testHugepageInPod(f, l.root, &vsource, config)
			})*/
})

func testHugepageInPod(
	f *framework.Framework,
	root string,
	source *v1.VolumeSource,
	config *storageframework.PerTestConfig,
) {
	pod := CreatePod(f, "hugepage-test", v1.PersistentVolumeFilesystem, source, config, false)
	defer func() {
		DeletePod(f, pod)
	}()
	testHugepage(f, pod, root, source)
	DeletePod(f, pod)
}

func testHugepage(
	f *framework.Framework,
	pod *v1.Pod,
	root string,
	source *v1.VolumeSource,
) {
	ns := f.Namespace.Name
	containerName := pod.Spec.Containers[0].Name

	// run trace monitor on all workers
	for worker := 1; ; worker++ {
		sshcmd := fmt.Sprintf("%s/_work/%s/ssh.%d", os.Getenv("REPO_ROOT"), os.Getenv("CLUSTER"), worker)
		if _, err := os.Stat(sshcmd); err == nil {
			ssh := exec.Command(sshcmd, "sudo sh -c 'echo 1 > /sys/kernel/debug/tracing/events/fs_dax/dax_pmd_fault_done/enable; echo 1 > /sys/kernel/debug/tracing/tracing_on; cat /sys/kernel/debug/tracing/trace_pipe > /tmp/tracetmp 2>&1 &'")
			_, err = ssh.Output()
			if err != nil {
				framework.Failf("Failed to start pagefault tracing: %v", err)
			}
		} else {
			// ssh wrapper does not exist: all nodes handled.
			break
		}
	}

	accessOutput, _ := pmempod.RunInPod(f, root, []string{accessHugepagesBinary}, "/tmp/"+path.Base(accessHugepagesBinary), ns, pod.Name, containerName)
	By(fmt.Sprintf("Output from pmem-access-hugepages pod:[%s]", accessOutput))
	for worker := 1; ; worker++ {
		sshcmd := fmt.Sprintf("%s/_work/%s/ssh.%d", os.Getenv("REPO_ROOT"), os.Getenv("CLUSTER"), worker)
		if _, err := os.Stat(sshcmd); err == nil {
			ssh := exec.Command(sshcmd, "sudo sh -c 'echo 0 > /sys/kernel/debug/tracing/events/fs_dax/dax_pmd_fault_done/enable; echo 0 > /sys/kernel/debug/tracing/tracing_on; pkill -f cat\\ /sys/kernel/debug/tracing/trace_pipe'")
			_, err = ssh.Output()
			if err != nil {
				framework.Failf("Failed to stop pagefault tracing: %v", err)
			}

			// There may be garbage (zero values) at start, we get better results by counting from end, thats why we use NF-relative fields in awk
			ssh = exec.Command(sshcmd, "cat /tmp/tracetmp|awk '{print $(NF-13) $(NF-9)}'")
			traceOutput, _ := ssh.Output()
			if len(traceOutput) > 0 { // there was output from trace, get fault type
				ssh := exec.Command(sshcmd, "cat /tmp/tracetmp|awk '{print $NF}'")
				faultType, _ := ssh.Output()
				By(fmt.Sprintf("Worker %d has tracer output: fault type:[%s] inode+addr:[%s]",
					worker, strings.TrimSpace(string(faultType)), strings.TrimSpace(string(traceOutput))))

				// Trace event NOPAGE means, hugepage fault happened. Trace event FALLBACK means, no page fault.
				framework.ExpectEqual(strings.TrimSpace(string(faultType)), "NOPAGE", "page fault type has to be NOPAGE")
				framework.ExpectEqual(accessOutput, strings.TrimSpace(string(traceOutput)), "mapped inode and addr must match traced values")
				break
			}
		} else {
			// ssh wrapper does not exist: all nodes handled.
			// If we reach this break here instead of one above, no node had tracer output, this is not what we planned.
			framework.Fail("No worker had trace output")
			break
		}
	}
}
