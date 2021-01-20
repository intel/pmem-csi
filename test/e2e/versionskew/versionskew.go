/*
Copyright 2020 Intel Corporation.

SPDX-License-Identifier: Apache-2.0
*/

/* Version skew testing ensures that APIs and state is compatible
across up- and downgrades. The driver for older releases is installed
by checking out the deployment YAML files from an older release.

The operator is not covered yet.
*/
package versionskew

import (
	"context"
	"fmt"
	"strings"

	"k8s.io/kubernetes/test/e2e/framework"
	"k8s.io/kubernetes/test/e2e/framework/skipper"
	"k8s.io/kubernetes/test/e2e/storage/testpatterns"
	"k8s.io/kubernetes/test/e2e/storage/testsuites"

	"github.com/container-storage-interface/spec/lib/go/csi"
	"github.com/intel/pmem-csi/pkg/k8sutil"
	"github.com/intel/pmem-csi/pkg/version"
	"github.com/intel/pmem-csi/test/e2e/deploy"
	"github.com/intel/pmem-csi/test/e2e/driver"
	"github.com/intel/pmem-csi/test/e2e/storage/dax"
	"google.golang.org/grpc"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	utilerrors "k8s.io/apimachinery/pkg/util/errors"
	e2estatefulset "k8s.io/kubernetes/test/e2e/framework/statefulset"
	e2evolume "k8s.io/kubernetes/test/e2e/framework/volume"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
)

const (
	// TODO: remove this and all code using it when no longer testing against 0.8
	base_08 = "0.8"

	// base is the release branch used for version skew testing. Empty if none.
	base = base_08
)

func baseSupportsKubernetes(ver version.Version) bool {
	// 0.8 supports the same version as "devel".
	switch ver {
	default:
		return true
	}
}

type skewTestSuite struct {
	tsInfo testsuites.TestSuiteInfo
}

var _ testsuites.TestSuite = &skewTestSuite{}

var (
	// The version skew tests run with combinations of the
	// following volume parameters.
	fsTypes = []string{"", "ext4"}
	// CSIInlineVolume cannot be tested because the current cluster setup for the PMEM-CSI scheduler
	// extender used NodeCacheSupported=true and PMEM-CSI 0.7.x doesn't support that. Immediate binding
	// works.
	volTypes      = []testpatterns.TestVolType{ /* testpatterns.CSIInlineVolume, */ testpatterns.DynamicPV}
	volParameters = []map[string]string{
		nil,
		// We cannot test cache volumes because of https://github.com/intel/pmem-csi/issues/733:
		// the workaround with "wait for n volumes to be known" fails for those because
		// the controller already reports the volume after learning about one volume in the
		// cache set and thus may leak the other ones.
		// {
		// string(parameters.CacheSize):        "2",
		//	string(parameters.PersistencyModel): string(parameters.PersistencyCache),
		// },
	}
	volModes = []v1.PersistentVolumeMode{
		v1.PersistentVolumeFilesystem,
		v1.PersistentVolumeBlock,
	}
)

// InitSkewTestSuite dynamically generates testcases for version skew testing.
// Each test case represents a certain kind of volume supported by PMEM-CSI.
func InitSkewTestSuite() testsuites.TestSuite {
	suite := &skewTestSuite{
		tsInfo: testsuites.TestSuiteInfo{
			Name: "skew",
		},
	}

	haveCSIInline := false
	haveBlock := false
	for _, volType := range volTypes {
		for _, fs := range fsTypes {
			for _, parameters := range volParameters {
				scp := driver.StorageClassParameters{
					FSType:     fs,
					Parameters: parameters,
				}
				for _, volMode := range volModes {
					pattern := testpatterns.TestPattern{
						Name:    driver.EncodeTestPatternName(volType, volMode, scp),
						VolType: volType,
						VolMode: volMode,
						FsType:  fs,
					}
					if volType == testpatterns.CSIInlineVolume {
						if haveCSIInline {
							// Only generate a single test pattern for inline volumes
							// because we don't want the number of testcases to explode.
							continue
						}
						haveCSIInline = true
					}
					if volMode == v1.PersistentVolumeBlock {
						if haveBlock {
							// Same for raw block.
							continue
						}
						haveBlock = true
					}
					suite.tsInfo.TestPatterns = append(suite.tsInfo.TestPatterns, pattern)
				}
			}
		}
	}

	return suite
}

func (p *skewTestSuite) GetTestSuiteInfo() testsuites.TestSuiteInfo {
	return p.tsInfo
}

func (p *skewTestSuite) SkipRedundantSuite(driver testsuites.TestDriver, pattern testpatterns.TestPattern) {
	// We rely here on the driver being named after a deployment
	// (see csi_volumes.go).
	d := deploy.MustParse(driver.GetDriverInfo().Name)

	// The workaround for the volume leak during driver
	// startup (https://github.com/intel/pmem-csi/issues/733)
	// needs access to the controller via socat.
	if !d.Testing {
		skipper.Skipf("need controller socat service")
	}
}

type local struct {
	config      *testsuites.PerTestConfig
	testCleanup func()

	unused, usedBefore, usedAfter *testsuites.VolumeResource
}

const socatPort = 9735

func (p *skewTestSuite) DefineTests(driver testsuites.TestDriver, pattern testpatterns.TestPattern) {
	var l local

	f := framework.NewDefaultFramework("skew")

	// We rely here on the driver being named after a deployment
	// (see csi_volumes.go).
	d := deploy.MustParse(driver.GetDriverInfo().Name)

	BeforeEach(func() {
		ver, err := k8sutil.GetKubernetesVersion(f.ClientConfig())
		framework.ExpectNoError(err, "get Kubernetes version")
		if base == "" {
			skipper.Skipf("version skew testing disabled")
		}
		if !baseSupportsKubernetes(*ver) {
			skipper.Skipf("%s not supported by release-%s", ver, base)
		}
	})

	init := func(all bool) {
		l = local{}
		l.config, l.testCleanup = driver.PrepareTest(f)

		// Now do the more expensive test initialization. We potentially create more than one
		// storage class, so each resource needs a different prefix.
		l.unused = createVolumeResource(driver, l.config, "-unused", pattern)
		if all {
			l.usedBefore = createVolumeResource(driver, l.config, "-before", pattern)
			l.usedAfter = createVolumeResource(driver, l.config, "-after", pattern)
		}
	}

	cleanup := func() {
		var cleanUpErrs []error

		if l.unused != nil {
			cleanUpErrs = append(cleanUpErrs, l.unused.CleanupResource())
			l.unused = nil
		}

		if l.usedBefore != nil {
			cleanUpErrs = append(cleanUpErrs, l.usedBefore.CleanupResource())
			l.usedBefore = nil
		}

		if l.usedAfter != nil {
			cleanUpErrs = append(cleanUpErrs, l.usedAfter.CleanupResource())
			l.usedAfter = nil
		}

		if l.testCleanup != nil {
			l.testCleanup()
			l.testCleanup = nil
		}

		err := utilerrors.NewAggregate(cleanUpErrs)
		framework.ExpectNoError(err, "clean up volumes")
	}

	// waitForVolumes ensures that the controller knows about the right number of volumes.
	// It is used below to work around https://github.com/intel/pmem-csi/issues/733
	waitForVolumes := func(numVolumes int) {
		// The cluster controller service can be reached via
		// any node, what matters is the service port.
		cluster, err := deploy.NewCluster(f.ClientSet, f.DynamicClient)
		port, err := cluster.GetServicePort(context.Background(), "pmem-csi-controller-testing", d.Namespace)
		framework.ExpectNoError(err, "find controller test service")
		controllerAddress := cluster.NodeServiceAddress(0, port)
		framework.Logf("skew: using controller %s", controllerAddress)

		conn, err := grpc.Dial(controllerAddress, grpc.WithInsecure())
		framework.ExpectNoError(err, "connect to controller %s", controllerAddress)
		defer conn.Close()
		controller := csi.NewControllerClient(conn)
		Eventually(func() error {
			resp, err := controller.ListVolumes(context.Background(), &csi.ListVolumesRequest{})
			if err != nil {
				return err
			}
			if len(resp.Entries) != numVolumes {
				return fmt.Errorf("expect %d volumes, got %d", numVolumes, len(resp.Entries))
			}
			return nil
		}, "3m", "10s").ShouldNot(HaveOccurred(), "wait for %d volumes", numVolumes)
	}

	testVersionChange := func(otherName string) {
		withKataContainers := false

		// Create volumes.
		init(true)
		defer cleanup()

		// Use some volume before the up- or downgrade
		By(fmt.Sprintf("creating pod before switching driver to %s", otherName))
		podBefore := dax.CreatePod(f, "pod-before-test", l.usedBefore.Pattern.VolMode, l.usedBefore.VolSource, l.config, withKataContainers)

		// Change driver releases.
		By(fmt.Sprintf("switch driver to %s", otherName))
		deployment, err := deploy.Parse(otherName)
		if err != nil {
			framework.Failf("internal error while parsing %s: %v", otherName, err)
		}
		deploy.EnsureDeploymentNow(f, deployment)

		if strings.Contains(otherName, base_08) {
			// Work around volume leak (https://github.com/intel/pmem-csi/issues/733) by
			// waiting for controller to know about all volumes.
			switch pattern.VolType {
			case testpatterns.CSIInlineVolume:
				// One running pod -> one volume.
				waitForVolumes(1)
			default:
				// Three stand-alone volumes.
				waitForVolumes(3)
			}
		}

		// Use some other volume.
		By(fmt.Sprintf("creating pod after switching driver to %s", otherName))
		podAfter := dax.CreatePod(f, "pod-after-test", l.usedAfter.Pattern.VolMode, l.usedAfter.VolSource, l.config, withKataContainers)

		// Remove everything.
		By("cleaning up")
		dax.DeletePod(f, podBefore)
		dax.DeletePod(f, podAfter)
		cleanup()
	}

	// This changes controller and node versions at the same time.
	It("everything [Slow]", func() {
		// First try the downgrade direction.
		currentName := d.Name()
		oldName := currentName + "-" + base
		testVersionChange(oldName)

		// Now that older driver is running, do the same for
		// an upgrade. When the test is done, the cluster is
		// back in the same state as before.
		testVersionChange(currentName)
	})

	// This test combines controller and node from different releases
	// and checks that they can work together. This can happen when
	// the operator mutates the deployment objects and the change isn't
	// applied everywhere at once.
	//
	// We change the controller because that side is easier to modify
	// (scale down, change spec, scale up) and test only one direction
	// (old nodes, new controller) because that direction is more likely
	// and if there compatibility issues, then hopefully the direction
	// of the skew won't matter.
	It("controller [Slow]", func() {
		if base == base_08 {
			skipper.Skipf("current controller not compatible with PMEM-CSI 0.8")
		}

		withKataContainers := false
		c, err := deploy.NewCluster(f.ClientSet, f.DynamicClient)

		// Get the current controller image.
		//
		// The test has to make some assumptions about our deployments,
		// like "controller is in a statefulset" and what its name is.
		// The test also relies on command line parameters staying
		// compatible. If we ever change that, we need to add some extra
		// logic here.
		controllerSet, err := f.ClientSet.AppsV1().StatefulSets("default").Get(context.Background(), "pmem-csi-controller", metav1.GetOptions{})
		framework.ExpectNoError(err, "get controller")
		currentImage := controllerSet.Spec.Template.Spec.Containers[0].Image
		Expect(currentImage).To(ContainSubstring("pmem-csi"))

		// Now downgrade.
		currentName := driver.GetDriverInfo().Name
		otherName := currentName + "-" + base
		deployment, err := deploy.Parse(otherName)
		if err != nil {
			framework.Failf("internal error while parsing %s: %v", otherName, err)
		}
		deploy.EnsureDeploymentNow(f, deployment)
		deployment, err = deploy.FindDeployment(c)
		framework.ExpectNoError(err, "find downgraded deployment")
		Expect(deployment.Version).NotTo(BeEmpty(), "should be running an old release")

		// Update the controller image.
		setImage := func(newImage string) string {
			By(fmt.Sprintf("changing controller image to %s", newImage))
			controllerSet, err := f.ClientSet.AppsV1().StatefulSets("default").Get(context.Background(), "pmem-csi-controller", metav1.GetOptions{})
			framework.ExpectNoError(err, "get controller")
			oldImage := controllerSet.Spec.Template.Spec.Containers[0].Image
			controllerSet.Spec.Template.Spec.Containers[0].Image = newImage
			controllerSet, err = f.ClientSet.AppsV1().StatefulSets("default").Update(context.Background(), controllerSet, metav1.UpdateOptions{})
			framework.ExpectNoError(err, "update controller")

			// Ensure that the stateful set runs the modified image.
			e2estatefulset.Restart(f.ClientSet, controllerSet)

			return oldImage
		}
		oldImage := setImage(currentImage)
		// Strictly speaking, we could also leave a broken deployment behind because the next
		// test will want to start with a deployment of the current release and thus will
		// reinstall anyway, but it is cleaner this way.
		defer setImage(oldImage)

		// Check that PMEM-CSI is up again. We have to do that with
		// an unset version because WaitForPMEMDriver uses that do determine whether it
		// needs to use http or https with the controller.
		framework.ExpectNoError(err, "get cluster information")
		mixedDeployment := *deployment
		mixedDeployment.Version = ""
		deploy.WaitForPMEMDriver(c, &mixedDeployment)

		// This relies on FindDeployment getting the version number from the image.
		deployment, err = deploy.FindDeployment(c)
		framework.ExpectNoError(err, "find modified deployment")
		Expect(deployment.Version).To(BeEmpty(), "should be running a current release") // TODO: what about testing 0.8?

		// Now that we are in a version skewed state, try some simple interaction between
		// controller and node by creating a volume and using it. This makes sense
		// even for CSI inline volumes because those may invoke the scheduler extensions.
		init(false)
		defer cleanup()
		pod := dax.CreatePod(f, "pod-skew-test", l.unused.Pattern.VolMode, l.unused.VolSource, l.config, withKataContainers)
		dax.DeletePod(f, pod)
	})
}

// createVolumeResource takes one of the test patterns prepared by InitSkewTestSuite and
// creates a volume for it.
func createVolumeResource(pmemDriver testsuites.TestDriver, config *testsuites.PerTestConfig, suffix string, pattern testpatterns.TestPattern) *testsuites.VolumeResource {
	_, _, scp, err := driver.DecodeTestPatternName(pattern.Name)
	Expect(err).NotTo(HaveOccurred(), "decode test pattern name")
	pmemDriver = pmemDriver.(driver.DynamicDriver).WithStorageClassNameSuffix(suffix).WithParameters(scp.Parameters)
	return testsuites.CreateVolumeResource(pmemDriver, config, pattern, e2evolume.SizeRange{})
}
