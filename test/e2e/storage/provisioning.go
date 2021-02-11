/*
Copyright 2018 The Kubernetes Authors.
Copyright 2019 Intel Corporation.

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
	"time"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"

	"k8s.io/api/core/v1"
	storagev1 "k8s.io/api/storage/v1"
	apierrs "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	clientset "k8s.io/client-go/kubernetes"
	"k8s.io/kubernetes/test/e2e/framework"
	e2epod "k8s.io/kubernetes/test/e2e/framework/pod"
	e2epv "k8s.io/kubernetes/test/e2e/framework/pv"
	"k8s.io/kubernetes/test/e2e/storage/testsuites"
)

// TestDynamicLateBindingProvisioning is a variant of k8s.io/kubernetes/test/e2e/storage/testsuites/provisioning.go
// which works with late and immediate binding and can be invoked in parallel with different IDs.
func TestDynamicProvisioning(client clientset.Interface, claim *v1.PersistentVolumeClaim, mode storagev1.VolumeBindingMode, id string) {
	var err error

	By(fmt.Sprintf("%s: creating a claim", id))
	claim, err = client.CoreV1().PersistentVolumeClaims(claim.Namespace).Create(context.Background(), claim, metav1.CreateOptions{})
	Expect(err).NotTo(HaveOccurred())
	defer func() {
		framework.Logf("deleting claim %q/%q", claim.Namespace, claim.Name)
		// typically this claim has already been deleted
		err = client.CoreV1().PersistentVolumeClaims(claim.Namespace).Delete(context.Background(), claim.Name, metav1.DeleteOptions{})
		if err != nil && !apierrs.IsNotFound(err) {
			framework.Failf("Error deleting claim %q. Error: %v", claim.Name, err)
		}
	}()

	if mode == storagev1.VolumeBindingWaitForFirstConsumer {
		// Schedule a pod, otherwise there's not going to be a PV.
		PVWriteReadSingleNodeCheck(client, claim, id)
	}

	err = e2epv.WaitForPersistentVolumeClaimPhase(v1.ClaimBound, client, claim.Namespace, claim.Name, framework.Poll, framework.ClaimProvisionTimeout)
	Expect(err).NotTo(HaveOccurred())

	By(fmt.Sprintf("%s: checking the claim", id))
	// Get new copy of the claim
	claim, err = client.CoreV1().PersistentVolumeClaims(claim.Namespace).Get(context.Background(), claim.Name, metav1.GetOptions{})
	Expect(err).NotTo(HaveOccurred())

	// Get the bound PV
	pv, err := client.CoreV1().PersistentVolumes().Get(context.Background(), claim.Spec.VolumeName, metav1.GetOptions{})
	Expect(err).NotTo(HaveOccurred())

	// Check PV properties
	By(fmt.Sprintf("%s: checking the PV", id))
	expectedAccessModes := []v1.PersistentVolumeAccessMode{v1.ReadWriteOnce}
	Expect(pv.Spec.AccessModes).To(Equal(expectedAccessModes))
	Expect(pv.Spec.ClaimRef.Name).To(Equal(claim.ObjectMeta.Name))
	Expect(pv.Spec.ClaimRef.Namespace).To(Equal(claim.ObjectMeta.Namespace))
	Expect(pv.Spec.PersistentVolumeReclaimPolicy).To(Equal(v1.PersistentVolumeReclaimDelete))

	By(fmt.Sprintf("%s: deleting claim %q/%q", id, claim.Namespace, claim.Name))
	framework.ExpectNoError(client.CoreV1().PersistentVolumeClaims(claim.Namespace).Delete(context.Background(), claim.Name, metav1.DeleteOptions{}))

	// Wait for the PV to get deleted if reclaim policy is Delete. (If it's
	// Retain, there's no use waiting because the PV won't be auto-deleted and
	// it's expected for the caller to do it.) Technically, the first few delete
	// attempts may fail, as the volume is still attached to a node because
	// kubelet is slowly cleaning up the previous pod, however it should succeed
	// in a couple of minutes. Wait 20 minutes to recover from random cloud
	// hiccups.
	if pv.Spec.PersistentVolumeReclaimPolicy == v1.PersistentVolumeReclaimDelete {
		By(fmt.Sprintf("%s: deleting the claim's PV %q", id, pv.Name))
		framework.ExpectNoError(e2epv.WaitForPersistentVolumeDeleted(client, pv.Name, 5*time.Second, 20*time.Minute))
	}
}

// PVWriteReadSingleNodeCheck checks that a PV retains data on a single node.
//
// It starts two pods:
// - The first pod writes 'hello word' to the /mnt/test (= the volume) on one node.
// - The second pod runs grep 'hello world' on /mnt/test on the same node.
//
// The node is selected by Kubernetes when scheduling the first
// pod. It's then selected via its name for the second pod.
//
// If both succeed, Kubernetes actually allocated something that is
// persistent across pods.
//
// This is a common test that can be called from a StorageClassTest.PvCheck.
func PVWriteReadSingleNodeCheck(client clientset.Interface, claim *v1.PersistentVolumeClaim, id string) {
	By(fmt.Sprintf("%s: checking the created volume is writable", id))
	command := "echo 'hello world' > /mnt/test/data || (mount | grep 'on /mnt/test'; false)"
	pod := testsuites.StartInPodWithVolume(client, claim.Namespace, claim.Name, "pvc-volume-tester-writer-"+id, command, e2epod.NodeSelection{})
	defer func() {
		// pod might be nil now.
		testsuites.StopPod(client, pod)
	}()
	framework.ExpectNoError(e2epod.WaitForPodSuccessInNamespaceSlow(client, pod.Name, pod.Namespace))
	runningPod, err := client.CoreV1().Pods(pod.Namespace).Get(context.Background(), pod.Name, metav1.GetOptions{})
	Expect(err).NotTo(HaveOccurred(), "get pod %s", id)
	actualNodeName := runningPod.Spec.NodeName
	testsuites.StopPod(client, pod)
	pod = nil // Don't stop twice.

	By(fmt.Sprintf("%s: checking the created volume is readable and retains data on the same node %q", id, actualNodeName))
	command = "grep 'hello world' /mnt/test/data"
	testsuites.RunInPodWithVolume(client, claim.Namespace, claim.Name, "pvc-volume-tester-reader-"+id, command, e2epod.NodeSelection{Name: actualNodeName})
}
