/*
Copyright 2020 Intel Corporation.

SPDX-License-Identifier: Apache-2.0
*/

// Package versionskew testing ensures that APIs and state is compatible
// across up- and downgrades. The driver for older releases is installed
// by checking out the deployment YAML files from an older release.
// The operator is not covered yet.
package versionskew

import (
	"context"
	"fmt"
	"strconv"

	"k8s.io/kubernetes/test/e2e/framework"
	"k8s.io/kubernetes/test/e2e/framework/skipper"
	storageframework "k8s.io/kubernetes/test/e2e/storage/framework"

	"github.com/intel/pmem-csi/pkg/k8sutil"
	"github.com/intel/pmem-csi/pkg/version"
	"github.com/intel/pmem-csi/test/e2e/deploy"
	"github.com/intel/pmem-csi/test/e2e/driver"
	"github.com/intel/pmem-csi/test/e2e/storage/dax"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	utilerrors "k8s.io/apimachinery/pkg/util/errors"
	e2edeployment "k8s.io/kubernetes/test/e2e/framework/deployment"
	e2evolume "k8s.io/kubernetes/test/e2e/framework/volume"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
)

const (
	// base is the release branch used for version skew testing. Empty if none.
	base = "1.0"

	// revisionAnnotation is the revision annotation of a deployment's replica sets which records its rollout sequence
	revisionAnnotation = "deployment.kubernetes.io/revision"
)

func baseSupportsKubernetes(ver version.Version) bool {
	// v1.0.x only supports Kubernetes 1.22, not 1.23 and higher.
	return ver.CompareVersion(version.NewVersion(1, 22)) <= 0
}

type skewTestSuite struct {
	tsInfo storageframework.TestSuiteInfo
}

var _ storageframework.TestSuite = &skewTestSuite{}

var (
	// The version skew tests run with combinations of the
	// following volume parameters.
	fsTypes = []string{"", "ext4"}
	// CSIInlineVolume cannot be tested because the current cluster setup for the PMEM-CSI scheduler
	// extender used NodeCacheSupported=true and PMEM-CSI 0.7.x doesn't support that. Immediate binding
	// works.
	volTypes      = []storageframework.TestVolType{ /* storageframework.CSIInlineVolume, */ storageframework.DynamicPV}
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
func InitSkewTestSuite() storageframework.TestSuite {
	suite := &skewTestSuite{
		tsInfo: storageframework.TestSuiteInfo{
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
					pattern := storageframework.TestPattern{
						Name:    driver.EncodeTestPatternName(volType, volMode, scp),
						VolType: volType,
						VolMode: volMode,
						FsType:  fs,
					}
					if volType == storageframework.CSIInlineVolume {
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

func (p *skewTestSuite) GetTestSuiteInfo() storageframework.TestSuiteInfo {
	return p.tsInfo
}

func (p *skewTestSuite) SkipUnsupportedTests(driver storageframework.TestDriver, pattern storageframework.TestPattern) {
	// We rely here on the driver being named after a deployment
	// (see csi_volumes.go).
	d := deploy.MustParse(driver.GetDriverInfo().Name)

	if d.Testing {
		// For example, replacing the controller image in a 0.9 testing deployment fails
		// because the 1.0 test image doesn't support the -coverprofile options.
		skipper.Skipf("version skew testing only needs to work for production deployments")
	}
}

type local struct {
	config      *storageframework.PerTestConfig
	testCleanup func()

	unused, usedBefore, usedAfter *storageframework.VolumeResource
}

func (p *skewTestSuite) DefineTests(driver storageframework.TestDriver, pattern storageframework.TestPattern) {
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
		if d.HasOperator {
			skipper.Skipf("cannot change image of the PMEM-CSI controller because it is managed by the operator")
		}

		withKataContainers := false
		c, err := deploy.NewCluster(f.ClientSet, f.DynamicClient, f.ClientConfig())
		framework.ExpectNoError(err, "new cluster")
		// Get the current controller image.
		//
		// The test has to make some assumptions about our deployments,
		// like "controller is in a statefulset" and what its name is.
		// The test also relies on command line parameters staying
		// compatible. If we ever change that, we need to add some extra
		// logic here.
		listOptions := metav1.ListOptions{
			LabelSelector: fmt.Sprintf("%s in (%s)", "pmem-csi.intel.com/deployment", d.Label()),
		}
		items, err := f.ClientSet.AppsV1().Deployments(d.Namespace).List(context.Background(), listOptions)
		framework.ExpectNoError(err, "get controller")
		controller := items.Items[0]
		currentImage := controller.Spec.Template.Spec.Containers[0].Image
		Expect(currentImage).To(ContainSubstring("pmem-csi"))
		By(fmt.Sprintf("Current controller driver image: %s", currentImage))

		// Now downgrade.
		currentName := driver.GetDriverInfo().Name
		otherName := currentName + "-" + base
		deployment, err := deploy.Parse(otherName)
		if err != nil {
			framework.Failf("internal error while parsing %s: %v", otherName, err)
		}
		By(fmt.Sprintf("Ensuring Downgrade Deployment: %s", otherName))
		deploy.EnsureDeploymentNow(f, deployment)
		deployment, err = deploy.FindDeployment(c)
		framework.ExpectNoError(err, "find downgraded deployment")
		Expect(deployment.Version).NotTo(BeEmpty(), "should be running an old release")

		// Update the controller image.
		setImage := func(newImage string) string {
			deployments, err := f.ClientSet.AppsV1().Deployments(d.Namespace).List(context.Background(), listOptions)
			framework.ExpectNoError(err, "get controller Deployment")
			if len(deployments.Items) == 0 {
				framework.Failf("found neither controller StatefulSet nor Deployment")
			}
			controller := &deployments.Items[0]
			oldImage := controller.Spec.Template.Spec.Containers[0].Image
			Expect(oldImage).NotTo(Equal(newImage), "old image is not different from new one")
			controller.Spec.Template.Spec.Containers[0].Image = newImage
			revision, err := strconv.Atoi(controller.Annotations[revisionAnnotation])
			framework.ExpectNoError(err, "parse controller Deployment annotation %s", revisionAnnotation)

			newRevision := fmt.Sprintf("%d", revision+1)
			By(fmt.Sprintf("changing controller image: %s ==> %s with revision %s", oldImage, newImage, newRevision))
			controller, err = f.ClientSet.AppsV1().Deployments(d.Namespace).Update(context.Background(), controller, metav1.UpdateOptions{})
			framework.ExpectNoError(err, "update controller Deployment")

			// Ensure that the stateful set runs the modified image.
			framework.ExpectNoError(e2edeployment.WaitForDeploymentRevisionAndImage(f.ClientSet, controller.Namespace, controller.Name,
				newRevision, newImage),
				"wait for controller Deployment with revision %s and image %s",
				newRevision, newImage)
			return oldImage
		}
		oldImage := setImage(currentImage)
		// Strictly speaking, we could also leave a broken deployment behind because the next
		// test will want to start with a deployment of the current release and thus will
		// reinstall anyway, but it is cleaner this way.
		defer setImage(oldImage)

		// Check that PMEM-CSI is up again.
		framework.ExpectNoError(err, "get cluster information")
		deploy.WaitForPMEMDriver(c, deployment, 1 /* controller replicas */)

		deployments, err := f.ClientSet.AppsV1().Deployments(d.Namespace).List(context.Background(), listOptions)
		framework.ExpectNoError(err, "get controller")
		Expect(deployments.Items[0].Spec.Template.Spec.Containers[0].Image).Should(BeEquivalentTo(currentImage), "controller is using upgraded image")

		dsList, err := f.ClientSet.AppsV1().DaemonSets(d.Namespace).List(context.Background(), listOptions)
		framework.ExpectNoError(err, "get node daemonset")
		Expect(dsList.Items[0].Spec.Template.Spec.Containers[0].Image).Should(BeEquivalentTo(oldImage), "node are using old image")

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
func createVolumeResource(pmemDriver storageframework.TestDriver, config *storageframework.PerTestConfig, suffix string, pattern storageframework.TestPattern) *storageframework.VolumeResource {
	_, _, scp, err := driver.DecodeTestPatternName(pattern.Name)
	Expect(err).NotTo(HaveOccurred(), "decode test pattern name")
	pmemDriver = pmemDriver.(driver.DynamicDriver).WithStorageClassNameSuffix(suffix).WithParameters(scp.Parameters)
	return storageframework.CreateVolumeResource(pmemDriver, config, pattern, e2evolume.SizeRange{})
}
