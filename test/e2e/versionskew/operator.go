/*
Copyright 2020 Intel Corporation.

SPDX-License-Identifier: Apache-2.0
*/

package versionskew

import (
	"fmt"
	"os"
	"os/exec"
	"sync"
	"time"

	api "github.com/intel/pmem-csi/pkg/apis/pmemcsi/v1beta1"
	pmemexec "github.com/intel/pmem-csi/pkg/exec"
	"github.com/intel/pmem-csi/test/e2e/deploy"
	"golang.org/x/net/context"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/watch"
	"k8s.io/kubernetes/test/e2e/framework"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
)

var _ = deploy.DescribeForSome("versionskew", func(d *deploy.Deployment) bool {
	return d.HasOperator && !d.HasDriver
}, func(d *deploy.Deployment) {
	var (
		evWatcher  watch.Interface
		evWait     sync.WaitGroup
		evCaptured map[types.UID]bool
		evMutex    sync.Mutex
	)

	f := framework.NewDefaultFramework("skew")
	f.SkipNamespaceCreation = true

	BeforeEach(func() {
		var err error

		Expect(f).ShouldNot(BeNil(), "framework init")

		evWatcher, err = f.ClientSet.CoreV1().Events("").Watch(context.TODO(), metav1.ListOptions{})
		Expect(err).ShouldNot(HaveOccurred(), "get events watcher")

		evMutex = sync.Mutex{}
	})

	deployDriver := func(name, image string) api.PmemCSIDeployment {
		cr := api.PmemCSIDeployment{
			ObjectMeta: metav1.ObjectMeta{
				Name: name,
			},
			Spec: api.DeploymentSpec{
				DeviceMode: api.DeviceModeDirect,
			},
		}
		if image != "" {
			cr.Spec.Image = image
		}
		driverDep := deploy.CreateDeploymentCR(f, cr)
		return driverDep
	}

	changeVersion := func(version string) {
		newDeploymentName := d.Name()
		if version != "" {
			// deploy.Parse() expects the deployment is seperated with '-'
			// and the version should be the 4th field.
			newDeploymentName = d.Name() + "---" + version
		} else if d.HasOLM {
			root := os.Getenv("REPO_ROOT")
			semver := current + ".0"
			defer func() {
				// Remove generated bundle
				_, err := pmemexec.RunCommand("rm", "-rf", root+"/deploy/olm-bundle/"+semver)
				framework.ExpectNoError(err, "remove generated bundle: %v", err)
			}()
			// (Re)Generate olm-bundle with future release version number
			// So that OLM sees it as upgrade.
			make := exec.Command("make", "operator-generate-bundle", "VERSION=v"+semver)
			make.Dir = root
			make.Env = os.Environ()
			_, err := pmemexec.Run(make)
			framework.ExpectNoError(err, "%s: generate bundle for operator version %s", d.Name(), d.Version)
		}

		// Change operator release.
		By(fmt.Sprintf("Switching the operator to '%s'", newDeploymentName))
		deployment, err := deploy.Parse(newDeploymentName)
		framework.ExpectNoError(err, "internal error while parsing %s: %v", newDeploymentName, err)
		deploy.EnsureDeploymentNow(f, deployment)
	}

	clearEvents := func() {
		evMutex.Lock()
		defer evMutex.Unlock()
		evCaptured = map[types.UID]bool{}
	}

	captureEvents := func() {
		// Initialize events map
		clearEvents()

		timeStamp := time.Now()
		evWait.Add(1)
		go func() {
			defer evWait.Done()
			for watchEvent := range evWatcher.ResultChan() {
				ev, ok := watchEvent.Object.(*corev1.Event)
				if !ok || !ev.LastTimestamp.After(timeStamp) ||
					ev.Source.Component != "pmem-csi-operator" ||
					ev.Reason != api.EventReasonRunning {
					continue
				}
				evMutex.Lock()
				evCaptured[ev.InvolvedObject.UID] = true
				evMutex.Unlock()
			}
		}()
	}

	stopEventCapture := func() {
		evWatcher.Stop()
	}

	pmemImage := func(containers []corev1.Container) string {
		for _, c := range containers {
			if c.Name == "pmem-driver" || c.Name == "pmem-csi-operator" {
				return c.Image
			}
		}
		return ""
	}

	getOperatorImage := func() string {
		dep, err := f.ClientSet.AppsV1().Deployments(d.Namespace).Get(context.TODO(), "pmem-csi-operator", metav1.GetOptions{})
		Expect(err).ShouldNot(HaveOccurred(), "get operator deployment")

		return pmemImage(dep.Spec.Template.Spec.Containers)
	}

	ensureDeploymentReady := func(uids []types.UID, what string) {
		framework.Logf("Ensuring that the CRs got reconciled")

		Eventually(func() bool {
			evMutex.Lock()
			defer evMutex.Unlock()
			for _, uid := range uids {
				if !evCaptured[uid] {
					return false
				}
			}
			return true
		}, "5m", "2s").Should(BeTrue(), "%s: reconcile driver deployments:", what)
	}

	testVersion := func(toVer string, what string) {
		// Default driver image expected on post version change
		defaultDriverImage := ""
		// Custom image explicitly chosen by the test
		// It must be locked to current operator version
		customImage := getOperatorImage()

		if toVer != "" { // downgrade
			// Downgrading to base
			defaultDriverImage = "intel/pmem-csi-driver:v" + toVer
		} else { // upgrade
			defaultDriverImage = os.Getenv("PMEM_CSI_IMAGE")
		}

		driverCR1 := deployDriver(what+"-default-image", "")
		defer deploy.DeleteDeploymentCR(f, driverCR1.Name)

		driverCR2 := deployDriver(what+"-custom-image", customImage)
		defer deploy.DeleteDeploymentCR(f, driverCR2.Name)

		uids := []types.UID{driverCR1.GetUID(), driverCR2.GetUID()}

		framework.Logf("Checking for events on: %s", uids)
		captureEvents()
		defer stopEventCapture()

		// Ensure the operator served the deployments
		ensureDeploymentReady(uids, fmt.Sprintf("before version %s", what))
		framework.Logf("Deployed both the drivers. Changing operator version to: %q", toVer)

		clearEvents()

		changeVersion(toVer)
		framework.Logf("%s: Operator changed to: %s", what, base)

		// Ensure the deployments reconciled with changed operator
		ensureDeploymentReady(uids, fmt.Sprintf("after version %s", what))

		ss, err := f.ClientSet.AppsV1().StatefulSets(d.Namespace).Get(context.TODO(), driverCR1.ControllerDriverName(), metav1.GetOptions{})
		Expect(err).ShouldNot(HaveOccurred(), "%s: get driver satefuleset", what)
		Expect(pmemImage(ss.Spec.Template.Spec.Containers)).To(HavePrefix(defaultDriverImage), "controller uses %sed driver image", what)

		ds, err := f.ClientSet.AppsV1().DaemonSets(d.Namespace).Get(context.TODO(), driverCR1.NodeDriverName(), metav1.GetOptions{})
		Expect(err).ShouldNot(HaveOccurred(), "%s: get driver daemonset", what)
		Expect(pmemImage(ds.Spec.Template.Spec.Containers)).To(HavePrefix(defaultDriverImage), "node driver uses %sed driver image", what)

		ss, err = f.ClientSet.AppsV1().StatefulSets(d.Namespace).Get(context.TODO(), driverCR2.ControllerDriverName(), metav1.GetOptions{})
		Expect(err).ShouldNot(HaveOccurred(), "%s: get driver satefuleset", what)
		Expect(pmemImage(ss.Spec.Template.Spec.Containers)).To(BeEquivalentTo(customImage), "controller uses custom driver image")

		ds, err = f.ClientSet.AppsV1().DaemonSets(d.Namespace).Get(context.TODO(), driverCR2.NodeDriverName(), metav1.GetOptions{})
		Expect(err).ShouldNot(HaveOccurred(), "%s: get driver daemonset", what)
		Expect(pmemImage(ds.Spec.Template.Spec.Containers)).To(BeEquivalentTo(customImage), "node driver uses custom driver image")
	}

	It("downgrade [Slow]", func() {
		if d.HasOLM {
			Skip("OLM yet not support operator downgrade!")
		}
		testVersion(base, "downgrade")
	})

	It("upgrade [Slow]", func() {
		// First remove existing operator deployment
		// This is mandatory in case of OLM. Otherwise later downgrade
		// step might results in operator upgrade by the OLM.
		deploy.EnsureDeploymentNow(f, &deploy.Deployment{Namespace: d.Namespace})

		// downgrade the operator to base version
		changeVersion(base)

		// And test upgrade
		testVersion("" /*devel*/, "upgrade")
	})
})
