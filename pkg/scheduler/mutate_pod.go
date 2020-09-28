/*
Copyright 2019 Cybozu
Copyright 2020 Intel Corp.

SPDX-License-Identifier: Apache-2.0

Based on https://github.com/cybozu-go/topolvm/blob/7b79ee30e997a165b220d4519c784e50eaec36c8/hook/mutate_pod.go
and information from https://banzaicloud.com/blog/k8s-admission-webhooks/
*/

package scheduler

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"

	corev1 "k8s.io/api/core/v1"
	storagev1 "k8s.io/api/storage/v1"
	apierrs "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/klog/v2"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"
)

const (
	// Resource is the resource that will trigger the scheduler extender.
	Resource = "pmem-csi.intel.com/scheduler"
)

// Handle implements admission.Handler interface.
func (s scheduler) Handle(ctx context.Context, req admission.Request) admission.Response {
	pod := &corev1.Pod{}
	err := s.decoder.Decode(req, pod)
	if err != nil {
		return admission.Errored(http.StatusBadRequest, err)
	}
	if len(pod.Spec.Containers) == 0 {
		return admission.Denied("pod has no containers")
	}

	// short cut
	if len(pod.Spec.Volumes) == 0 {
		return admission.Allowed("no volumes")
	}

	// Pods instantiated from templates may have empty name/namespace.
	// To lookup PVC in the same namespace, we set namespace obtained from req.
	if pod.Namespace == "" {
		pod.Namespace = req.Namespace
	}

	targets, err := s.targetStorageClasses(ctx)
	if err != nil {
		return admission.Errored(http.StatusInternalServerError, err)
	}
	klog.V(5).Infof("mutate pod %s: PMEM-CSI storage classes %v", pod.Name, targets)

	filter, err := s.mustFilterPod(ctx, pod, targets)
	if err != nil {
		return admission.Errored(http.StatusInternalServerError, err)
	}
	if !filter {
		klog.V(5).Infof("mutate pod %s: does not use inline or delayed PMEM volumes", pod.Name)
		return admission.Allowed("no relevant PMEM volumes")
	}

	ctnr := &pod.Spec.Containers[0]
	quantity := resource.NewQuantity(1, resource.DecimalSI)
	if ctnr.Resources.Requests == nil {
		ctnr.Resources.Requests = corev1.ResourceList{}
	}
	ctnr.Resources.Requests[Resource] = *quantity
	if ctnr.Resources.Limits == nil {
		ctnr.Resources.Limits = corev1.ResourceList{}
	}
	ctnr.Resources.Limits[Resource] = *quantity

	marshaledPod, err := json.Marshal(pod)
	if err != nil {
		return admission.Errored(http.StatusInternalServerError, err)
	}

	klog.V(5).Infof("mutate pod %s: uses PMEM", pod.Name)
	return admission.PatchResponseFromRaw(req.Object.Raw, marshaledPod)
}

func (s scheduler) targetStorageClasses(ctx context.Context) (map[string]bool, error) {
	scs, err := s.scLister.List(labels.Everything())
	if err != nil {
		return nil, err
	}

	targets := make(map[string]bool)
	for _, sc := range scs {
		targets[sc.Name] = s.mustFilterSC(sc)
	}
	return targets, nil
}

// mustFilter returns true iff we must mark a pod using a PVC with this storage class
// as one which needs the scheduler extender, i.e. when the storage class uses PMEM-CSI
// and isn't using immediate binding.
func (s scheduler) mustFilterSC(sc *storagev1.StorageClass) bool {
	return sc.Provisioner == s.driverName &&
		(sc.VolumeBindingMode == nil ||
			*sc.VolumeBindingMode != storagev1.VolumeBindingImmediate)
}

func (s scheduler) mustFilterPod(ctx context.Context, pod *corev1.Pod, targets map[string]bool) (bool, error) {
	for _, vol := range pod.Spec.Volumes {
		if vol.PersistentVolumeClaim != nil {
			pvcName := vol.PersistentVolumeClaim.ClaimName
			pvc, err := s.pvcLister.PersistentVolumeClaims(pod.Namespace).Get(pvcName)
			if err != nil && apierrs.IsNotFound(err) {
				// Bypass the lister, it might not have a recently created PVC yet.
				pvc, err = s.clientSet.CoreV1().PersistentVolumeClaims(pod.Namespace).Get(ctx, pvcName, metav1.GetOptions{})
			}

			if err != nil {
				if apierrs.IsNotFound(err) {
					// A pod is getting created before its PVC. TopoLVM returns an error in this case (https://github.com/cybozu-go/topolvm/blob/7b79ee30e997a165b220d4519c784e50eaec36c8/hook/mutate_pod.go#L129-L136),
					// but then pod creation fails while normally it would go through.
					// We ignore the PVC instead.
					klog.Warningf("pod mutator: pod %q with unknown pvc %q in namespace %q, ignoring the pvc", pod.Name, pvcName, pod.Namespace)
					continue
				}
				return false, fmt.Errorf("check PVC %s/%s: %v", pod.Namespace, pvcName, err)
			}

			if pvc.Spec.StorageClassName == nil {
				// empty class name may appear when DefaultStorageClass admission plugin
				// is turned off, or there are no default StorageClass.
				// https://kubernetes.io/docs/concepts/storage/persistent-volumes/#class-1
				continue
			}

			scName := *pvc.Spec.StorageClassName
			filter, known := targets[scName]
			if !known {
				// Query API server directly.
				sc, err := s.clientSet.StorageV1().StorageClasses().Get(ctx, scName, metav1.GetOptions{})
				if err != nil {
					if apierrs.IsNotFound(err) {
						// As with non-existent PVC, assume that it isn't using PMEM.
						continue
					}
					return false, fmt.Errorf("check storage class %s: %v", scName, err)
				}
				filter = s.mustFilterSC(sc)

				// Also cache result.
				targets[scName] = filter
			}
			if !filter {
				continue
			}

			// We don't care here whether the volume is already bound.
			// That can be checked more reliably by the scheduler extender.
			return true, nil
		} else if vol.CSI != nil {
			if vol.CSI.Driver == s.driverName {
				return true, nil
			}
		}
	}
	return false, nil
}
