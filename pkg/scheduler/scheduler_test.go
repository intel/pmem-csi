/*
Copyright 2017 The Kubernetes Authors.
Copyright 2020 Intel Corp.

SPDX-License-Identifier: Apache-2.0

Based on https://github.com/kubernetes/kubernetes/blob/5d5b444c4da95eb49618d380c49608e20a08c31c/pkg/controller/volume/scheduling/scheduler_binder_test.go
*/

package scheduler

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"net/http"
	"net/url"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	admissionv1beta1 "k8s.io/api/admission/v1beta1"
	v1 "k8s.io/api/core/v1"
	storagev1 "k8s.io/api/storage/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/informers"
	coreinformers "k8s.io/client-go/informers/core/v1"
	clientset "k8s.io/client-go/kubernetes"
	"k8s.io/client-go/kubernetes/fake"
	"k8s.io/klog"
	"k8s.io/kubernetes/pkg/controller"
	schedulerapi "k8s.io/kubernetes/pkg/scheduler/api"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"
)

var (
	// PVCs for manual binding
	unboundPVC             = makeTestPVC("unbound-pvc", "1Gi", "", pvcUnbound, "", "1", &waitClass)
	unboundPVC2            = makeTestPVC("unbound-pvc2", "5Gi", "", pvcUnbound, "", "1", &waitClass)
	unboundPVCUnknownClass = makeTestPVC("unbound-pvc", "1Gi", "", pvcUnbound, "", "1", &unknownClass)
	unboundNoPVC           = makeTestPVC("unbound-pvc", "1Gi", "", pvcUnbound, "", "1", &waitClassNoProvisioner)
	unboundOtherPVC        = makeTestPVC("unbound-pvc", "1Gi", "", pvcUnbound, "", "1", &waitClassOtherProvisioner)
	immediateUnboundPVC    = makeTestPVC("immediate-unbound-pvc", "1Gi", "", pvcUnbound, "", "1", &immediateClass)
	boundPVC               = makeTestPVC("bound-pvc", "1Gi", "", pvcBound, "pv-bound", "1", &waitClass)

	preboundPVC       = makeTestPVC("prebound-pvc", "1Gi", "", pvcPrebound, "pv-node1a", "1", &waitClass)
	preboundPVCNode1a = makeTestPVC("unbound-pvc", "1Gi", "", pvcPrebound, "pv-node1a", "1", &waitClass)
	boundPVCNode1a    = makeTestPVC("unbound-pvc", "1Gi", "", pvcBound, "pv-node1a", "1", &waitClass)
	immediateBoundPVC = makeTestPVC("immediate-bound-pvc", "1Gi", "", pvcBound, "pv-bound-immediate", "1", &immediateClass)

	// storage class names
	waitClass                 = "waitClass"
	immediateClass            = "immediateClass"
	waitClassNoProvisioner    = "waitClassNoProvisioner"
	waitClassOtherProvisioner = "waitClassOtherProvisioner"
	unknownClass              = "unknownClass"
)

const (
	GiG = 1024 * 1024 * 1024

	// Provisioner aka driver names.
	driverName = "test.pmem-csi.intel.com"

	// Our nodes.
	nodeA = "node-A"
	nodeB = "node-B"
	nodeC = "node-C"
)

func init() {
	klog.InitFlags(nil)
}

type inlineVolume struct {
	driverName string
	size       string
}

// clusterCapacity is a stub implementation of the Capacity interface.
type clusterCapacity map[string]int64

func (cc clusterCapacity) NodeCapacity(nodeName string) (int64, error) {
	available, ok := cc[nodeName]
	if !ok {
		return 0, fmt.Errorf("node %s unknown", nodeName)
	}
	return available, nil
}

type testEnv struct {
	client      clientset.Interface
	scheduler   *scheduler
	pvcInformer coreinformers.PersistentVolumeClaimInformer
}

func newTestEnv(t *testing.T, capacity Capacity, stopCh <-chan struct{}) *testEnv {
	client := &fake.Clientset{}
	informerFactory := informers.NewSharedInformerFactory(client, controller.NoResyncPeriodFunc())

	pvcInformer := informerFactory.Core().V1().PersistentVolumeClaims()
	classInformer := informerFactory.Storage().V1().StorageClasses()

	// Wait for informers cache sync
	informerFactory.Start(stopCh)
	for v, synced := range informerFactory.WaitForCacheSync(stopCh) {
		if !synced {
			t.Fatalf("Error syncing informer for %v", v)
		}
	}

	// Add storageclasses
	waitMode := storagev1.VolumeBindingWaitForFirstConsumer
	immediateMode := storagev1.VolumeBindingImmediate
	classes := []*storagev1.StorageClass{
		{
			ObjectMeta: metav1.ObjectMeta{
				Name: waitClass,
			},
			VolumeBindingMode: &waitMode,
			Provisioner:       driverName,
		},
		{
			ObjectMeta: metav1.ObjectMeta{
				Name: immediateClass,
			},
			VolumeBindingMode: &immediateMode,
			Provisioner:       driverName,
		},
		{
			ObjectMeta: metav1.ObjectMeta{
				Name: waitClassNoProvisioner,
			},
			VolumeBindingMode: &waitMode,
		},
		{
			ObjectMeta: metav1.ObjectMeta{
				Name: waitClassOtherProvisioner,
			},
			VolumeBindingMode: &waitMode,
			Provisioner:       driverName + ".example",
		},
	}
	for _, class := range classes {
		if err := classInformer.Informer().GetIndexer().Add(class); err != nil {
			t.Fatalf("Failed to add storage class to internal cache: %v", err)
		}
	}

	handler, err := NewScheduler(driverName,
		capacity,
		client,
		pvcInformer.Lister(),
		classInformer.Lister(),
	)
	if err != nil {
		t.Fatalf("Failed to create scheduler: %v", err)
	}

	internalScheduler, ok := handler.(*scheduler)
	if !ok {
		t.Fatalf("Failed to convert %T to *scheduler", handler)
	}

	return &testEnv{
		client:      client,
		scheduler:   internalScheduler,
		pvcInformer: pvcInformer,
	}
}

func (env *testEnv) initClaims(pvcs []*v1.PersistentVolumeClaim) {
	for _, pvc := range pvcs {
		env.pvcInformer.Informer().GetIndexer().Add(pvc)
	}
}

const (
	pvcUnbound = iota
	pvcPrebound
	pvcBound
	pvcSelectedNode
)

func makeTestPVC(name, size, node string, pvcBoundState int, pvName, resourceVersion string, className *string) *v1.PersistentVolumeClaim {
	fs := v1.PersistentVolumeFilesystem
	pvc := &v1.PersistentVolumeClaim{
		TypeMeta: metav1.TypeMeta{
			Kind:       "PersistentVolumeClaim",
			APIVersion: "v1",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:            name,
			Namespace:       "testns",
			UID:             types.UID("pvc-uid"),
			ResourceVersion: resourceVersion,
			SelfLink:        "/api/v1/namespaces/testns/persistentvolumeclaims/" + name,
		},
		Spec: v1.PersistentVolumeClaimSpec{
			Resources: v1.ResourceRequirements{
				Requests: v1.ResourceList{
					v1.ResourceName(v1.ResourceStorage): resource.MustParse(size),
				},
			},
			StorageClassName: className,
			VolumeMode:       &fs,
		},
	}

	switch pvcBoundState {
	case pvcBound:
		pvc.Status.Phase = v1.ClaimBound
	case pvcPrebound:
		pvc.Spec.VolumeName = pvName
	}
	return pvc
}

func makePod(pvcs []*v1.PersistentVolumeClaim, inline []inlineVolume) *v1.Pod {
	pod := &v1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-pod",
			Namespace: "testns",
		},
		Spec: v1.PodSpec{
			Containers: []v1.Container{
				v1.Container{},
			},
		},
	}

	volumes := []v1.Volume{}
	i := 0
	for _, pvc := range pvcs {
		pvcVol := v1.Volume{
			Name: fmt.Sprintf("vol%v", i),
			VolumeSource: v1.VolumeSource{
				PersistentVolumeClaim: &v1.PersistentVolumeClaimVolumeSource{
					ClaimName: pvc.Name,
				},
			},
		}
		volumes = append(volumes, pvcVol)
		i++
	}
	for _, vol := range inline {
		pvcVol := v1.Volume{
			Name: fmt.Sprintf("vol%v", i),
			VolumeSource: v1.VolumeSource{
				CSI: &v1.CSIVolumeSource{
					Driver: vol.driverName,
					VolumeAttributes: map[string]string{
						"size": vol.size,
					},
				},
			},
		}
		volumes = append(volumes, pvcVol)
		i++
	}
	pod.Spec.Volumes = volumes
	pod.Spec.NodeName = "node1"
	return pod
}

func makePodWithoutPVC() *v1.Pod {
	pod := &v1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-pod",
			Namespace: "testns",
		},
		Spec: v1.PodSpec{
			Volumes: []v1.Volume{
				{
					VolumeSource: v1.VolumeSource{
						EmptyDir: &v1.EmptyDirVolumeSource{},
					},
				},
			},
		},
	}
	return pod
}

func makeNodeList(nodeNames []string) *v1.NodeList {
	nodes := v1.NodeList{}
	for _, nodeName := range nodeNames {
		nodes.Items = append(nodes.Items, v1.Node{ObjectMeta: metav1.ObjectMeta{Name: nodeName}})
	}
	return &nodes
}

type response struct {
	statusCode int
	body       []byte
}

func (r *response) Header() http.Header {
	return http.Header{}
}

func (r *response) WriteHeader(statusCode int) {
	r.statusCode = statusCode
}

func (r *response) Write(buffer []byte) (int, error) {
	r.body = append(r.body, buffer...)
	return len(buffer), nil
}

func TestScheduler(t *testing.T) {
	t.Parallel()
	type scenarioType struct {
		// Inputs
		pvcs   []*v1.PersistentVolumeClaim
		inline []inlineVolume
		// If nil, makePod with podPVCs
		pod      *v1.Pod
		capacity clusterCapacity
		// Nodes to check.
		nodes []string

		// Results
		expectedError    string
		expectedNodes    []string
		expectedFailures schedulerapi.FailedNodesMap
	}
	scenarios := map[string]scenarioType{
		"no volumes, no nodes": {},
		"no volumes, one node, no capacity": {
			nodes:         []string{nodeA},
			expectedNodes: []string{nodeA},
		},
		"one volume, one node, no capacity": {
			pvcs: []*v1.PersistentVolumeClaim{
				unboundPVC,
			},
			nodes: []string{nodeA},
			expectedFailures: map[string]string{
				nodeA: "checking for capacity: retrieve capacity: node node-A unknown",
			},
		},
		"one volume, one node, enough capacity": {
			pvcs: []*v1.PersistentVolumeClaim{
				unboundPVC,
			},
			nodes: []string{nodeA},
			capacity: clusterCapacity{
				nodeA: GiG,
			},
			expectedNodes: []string{nodeA},
		},
		"one inline volume, one node, insufficient capacity": {
			inline: []inlineVolume{
				{
					driverName: driverName,
					size:       resource.NewQuantity(GiG, resource.BinarySI).String(),
				},
			},
			nodes: []string{nodeA},
			capacity: clusterCapacity{
				nodeA: 1,
			},
			expectedFailures: map[string]string{
				nodeA: "only 1B of PMEM available, need 1GiB",
			},
		},
		"one inline volume, one node, invalid size": {
			inline: []inlineVolume{
				{
					driverName: driverName,
					size:       "foobar",
				},
			},
			nodes: []string{nodeA},
			capacity: clusterCapacity{
				nodeA: 1,
			},
			expectedError: "checking for unbound volumes: ephemeral inline volume vol0: parameter \"size\": failed to parse \"foobar\" as int64: quantities must match the regular expression '^([+-]?[0-9.]+)([eEinumkKMGTP]*[-+]?[0-9]*)$'",
		},
		"one inline volume, one node, enough capacity": {
			inline: []inlineVolume{
				{
					driverName: driverName,
					size:       resource.NewQuantity(GiG, resource.DecimalSI).String(),
				},
			},
			nodes: []string{nodeA},
			capacity: clusterCapacity{
				nodeA: GiG,
			},
			expectedNodes: []string{nodeA},
		},
		"one other inline volume, one node, no capacity": {
			inline: []inlineVolume{
				{
					driverName: driverName + ".example",
					size:       resource.NewQuantity(GiG, resource.BinarySI).String(),
				},
			},
			nodes:         []string{nodeA},
			expectedNodes: []string{nodeA},
		},
		"one bound volume, one node, no capacity": {
			pvcs: []*v1.PersistentVolumeClaim{
				boundPVC,
			},
			nodes:         []string{nodeA},
			expectedNodes: []string{nodeA},
		},
		"one pending volume, one node, no capacity": {
			// This might never happen because Kubernetes probably first waits
			// for the volume to be bound.
			pvcs: []*v1.PersistentVolumeClaim{
				immediateUnboundPVC,
			},
			nodes:         []string{nodeA},
			expectedNodes: []string{nodeA},
		},
		"one volume with no provisioner, one node, no capacity": {
			pvcs: []*v1.PersistentVolumeClaim{
				unboundNoPVC,
			},
			nodes:         []string{nodeA},
			expectedNodes: []string{nodeA},
		},
		"one volume with other provisioner, one node, no capacity": {
			pvcs: []*v1.PersistentVolumeClaim{
				unboundOtherPVC,
			},
			nodes:         []string{nodeA},
			expectedNodes: []string{nodeA},
		},
		"two volumes, one node, enough capacity for one": {
			pvcs: []*v1.PersistentVolumeClaim{
				unboundPVC,
				unboundPVC2,
			},
			nodes: []string{nodeA},
			capacity: clusterCapacity{
				nodeA: GiG,
			},
			expectedFailures: map[string]string{
				nodeA: "only 1GiB of PMEM available, need 6GiB",
			},
		},
		"one volume, two nodes, enough capacity": {
			pvcs: []*v1.PersistentVolumeClaim{
				unboundPVC,
			},
			nodes: []string{nodeA, nodeB},
			capacity: clusterCapacity{
				nodeA: GiG,
				nodeB: GiG,
			},
			expectedNodes: []string{nodeA, nodeB},
		},
		"one volume, two nodes, enough capacity on A": {
			pvcs: []*v1.PersistentVolumeClaim{
				unboundPVC,
			},
			nodes: []string{nodeA, nodeB},
			capacity: clusterCapacity{
				nodeA: GiG,
				nodeB: 1,
			},
			expectedNodes: []string{nodeA},
			expectedFailures: map[string]string{
				nodeB: "only 1B of PMEM available, need 1GiB",
			},
		},
		"one volume, two nodes, insufficient capacity": {
			pvcs: []*v1.PersistentVolumeClaim{
				unboundPVC,
			},
			nodes: []string{nodeA, nodeB},
			capacity: clusterCapacity{
				nodeA: 100,
				nodeB: 10,
			},
			expectedFailures: map[string]string{
				nodeA: "only 100B of PMEM available, need 1GiB",
				nodeB: "only 10B of PMEM available, need 1GiB",
			},
		},
		"unknown pvc": {
			pod:           makePod([]*v1.PersistentVolumeClaim{unboundPVC}, nil),
			expectedError: "checking for unbound volumes: look up claim: persistentvolumeclaim \"" + unboundPVC.Name + "\" not found",
		},
		"unknown storage class": {
			pvcs: []*v1.PersistentVolumeClaim{
				unboundPVCUnknownClass,
			},
			expectedError: "checking for unbound volumes: look up storage class: storageclass.storage.k8s.io \"" + unknownClass + "\" not found",
		},
	}

	run := func(t *testing.T, scenario scenarioType) {
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()

		// Setup
		testEnv := newTestEnv(t, scenario.capacity, ctx.Done())
		testEnv.initClaims(scenario.pvcs)

		// Generate input. We keep it very simple. Kubernetes actually sends
		// the entire pod and node objects.
		pod := scenario.pod
		if pod == nil {
			pod = makePod(scenario.pvcs, scenario.inline)
		}
		nodes := makeNodeList(scenario.nodes)
		args := schedulerapi.ExtenderArgs{
			Pod:   pod,
			Nodes: nodes,
		}
		requestBody, err := json.Marshal(args)
		require.NoError(t, err, "marshal request")
		request := &http.Request{
			URL:  &url.URL{Path: "/filter"},
			Body: ioutil.NopCloser(bytes.NewReader(requestBody)),
		}
		r := &response{}
		testEnv.scheduler.ServeHTTP(r, request)

		// Check response.
		var result schedulerapi.ExtenderFilterResult
		err = json.Unmarshal(r.body, &result)
		require.NoError(t, err, "unmarshal response")
		assert.Equal(t, scenario.expectedError, result.Error)
		var names []string
		if result.Nodes != nil {
			names = nodeNames(result.Nodes.Items)
		}
		assert.Equal(t, scenario.expectedNodes, names)
		failures := scenario.expectedFailures
		if failures == nil && scenario.expectedError == "" {
			failures = schedulerapi.FailedNodesMap{}
		}
		assert.Equal(t, failures, result.FailedNodes)
	}

	for name, scenario := range scenarios {
		scenario := scenario
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			run(t, scenario)
		})
	}
}

func TestMutatePod(t *testing.T) {
	t.Parallel()
	// denied := admission.Denied("pod has no containers")
	noPMEM := admission.Allowed("no request for PMEM-CSI")
	noVolumes := admission.Allowed("no volumes")

	type scenarioType struct {
		// Inputs
		pvcs   []*v1.PersistentVolumeClaim
		inline []inlineVolume
		// If nil, makePod with podPVCs
		pod *v1.Pod

		// If a result is specified, that is what should be returned.
		// If not, then the pod is expected to get filtered.
		expectedResult *admission.Response
	}
	scenarios := map[string]scenarioType{
		"no volumes": {
			expectedResult: &noVolumes,
		},
		"one PMEM volume": {
			pvcs: []*v1.PersistentVolumeClaim{
				unboundPVC,
			},
		},
		"one other volume": {
			pvcs: []*v1.PersistentVolumeClaim{
				unboundOtherPVC,
			},
			expectedResult: &noPMEM,
		},
		"two volumes": {
			pvcs: []*v1.PersistentVolumeClaim{
				unboundOtherPVC,
				unboundPVC,
			},
		},
		"unknown pvc": {
			pod:            makePod([]*v1.PersistentVolumeClaim{unboundPVC}, nil),
			expectedResult: &noPMEM,
		},
		"unknown storage class": {
			pvcs: []*v1.PersistentVolumeClaim{
				unboundPVCUnknownClass,
			},
			expectedResult: &noPMEM,
		},
		"one inline volume": {
			inline: []inlineVolume{
				{
					driverName: driverName,
					size:       resource.NewQuantity(GiG, resource.BinarySI).String(),
				},
			},
		},
		"one other inline volume": {
			inline: []inlineVolume{
				{
					driverName: driverName + ".example",
					size:       resource.NewQuantity(GiG, resource.BinarySI).String(),
				},
			},
			expectedResult: &noPMEM,
		},
	}

	run := func(t *testing.T, scenario scenarioType) {
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()

		// Setup
		testEnv := newTestEnv(t, nil, ctx.Done())
		testEnv.initClaims(scenario.pvcs)

		// Generate input. We keep it very simple. Kubernetes actually sends
		// the entire pod and node objects.
		pod := scenario.pod
		if pod == nil {
			pod = makePod(scenario.pvcs, scenario.inline)
		}
		obj, err := json.Marshal(pod)
		require.NoError(t, err, "encode pod")
		req := admission.Request{
			AdmissionRequest: admissionv1beta1.AdmissionRequest{
				Namespace: "default",
				Object: runtime.RawExtension{
					Raw: obj,
				},
			},
		}

		// Now mutate.
		response := testEnv.scheduler.Handle(context.Background(), req)

		// Check response.
		if scenario.expectedResult != nil {
			assert.Equal(t, *scenario.expectedResult, response, "webhook result")
		} else {
			assert.True(t, response.Allowed, "allowed")
			// That the patches do indeed add the extended
			// resource is covered by the E2E test.
			assert.NotEmpty(t, response.Patches, "JSON patch")
		}
	}

	for name, scenario := range scenarios {
		scenario := scenario
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			run(t, scenario)
		})
	}
}
