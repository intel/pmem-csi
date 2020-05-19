/*
Copyright 2020 Intel Corp.

SPDX-License-Identifier: Apache-2.0
*/

package scheduler

import (
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"sync"

	"github.com/go-logr/logr"
	"k8s.io/api/core/v1"
	storagev1 "k8s.io/api/storage/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	corelisters "k8s.io/client-go/listers/core/v1"
	storagelisters "k8s.io/client-go/listers/storage/v1"
	"k8s.io/klog/klogr"
	schedulerapi "k8s.io/kube-scheduler/extender/v1"
	"sigs.k8s.io/controller-runtime/pkg/webhook"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"

	"github.com/intel/pmem-csi/pkg/pmem-csi-driver/parameters"
)

// Capacity provides information of remaining free PMEM per node.
type Capacity interface {
	// NodeCapacity returns the available PMEM for the node.
	NodeCapacity(nodeName string) (int64, error)
}

type scheduler struct {
	driverName string
	capacity   Capacity
	clientSet  kubernetes.Interface
	pvcLister  corelisters.PersistentVolumeClaimLister
	scLister   storagelisters.StorageClassLister
	podMutator http.Handler
	decoder    *admission.Decoder
	log        logr.Logger
}

func NewScheduler(
	driverName string,
	capacity Capacity,
	clientSet kubernetes.Interface,
	pvcLister corelisters.PersistentVolumeClaimLister,
	scLister storagelisters.StorageClassLister,
) (http.Handler, error) {
	s := &scheduler{
		driverName: driverName,
		capacity:   capacity,
		clientSet:  clientSet,
		pvcLister:  pvcLister,
		scLister:   scLister,
		log:        klogr.New().WithName("scheduler"),
	}
	scheme := runtime.NewScheme()
	if err := clientgoscheme.AddToScheme(scheme); err != nil {
		return nil, fmt.Errorf("initialize client-go scheme: %v", err)
	}
	decoder, err := admission.NewDecoder(scheme)
	if err != nil {
		return nil, fmt.Errorf("initialize admission decoder: %v", err)
	}
	s.decoder = decoder
	webhook := webhook.Admission{Handler: s}
	webhook.InjectLogger(s.log.WithName("webhook"))
	s.podMutator = &webhook
	return s, nil
}

func (s *scheduler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet, http.MethodPost:
		// Both are treated the same below.
	default:
		http.Error(w, r.Method+" not supported", http.StatusMethodNotAllowed)
		return
	}

	switch r.URL.Path {
	case "/filter":
		s.filter(w, r)
	// TODO (?): prioritize nodes similar to https://github.com/cybozu-go/topolvm/blob/master/scheduler/prioritize.go
	// case "/prioritize":
	// 	s.prioritize(w, r)
	case "/status":
		s.status(w, r)
	case "/pod/mutate":
		s.podMutator.ServeHTTP(w, r)
	default:
		http.Error(w, "not found", http.StatusNotFound)
	}
}

func (s *scheduler) status(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusOK)
	w.Write([]byte("ok"))
}

// filter handles the JSON decoding+encoding.
func (s *scheduler) filter(w http.ResponseWriter, r *http.Request) {
	// From https://github.com/Huang-Wei/sample-scheduler-extender/blob/047fdd5ae8b1a6d7fdc0e6d20ce4d70a1d6e7178/routers.go#L19-L39
	var args schedulerapi.ExtenderArgs
	var result *schedulerapi.ExtenderFilterResult
	err := json.NewDecoder(r.Body).Decode(&args)
	if err == nil {
		result, err = s.doFilter(args)
	}

	// Always try to write a resonable response.
	if result == nil && err != nil {
		result = &schedulerapi.ExtenderFilterResult{
			Error: err.Error(),
		}
	}
	if response, err := json.Marshal(result); err != nil {
		s.log.Error(err, "JSON encoding")
		w.WriteHeader(http.StatusInternalServerError)
	} else {
		s.log.V(5).Info("node filter", "result", string(response))
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write(response)
	}
}

// doFilter determines how much PMEM storage the pod wants and filters out nodes with
// insufficient storage. A better solution would be a reservation system, but that's
// complicated to implement and should better be handled generically for volumes
// in Kubernetes.
func (s *scheduler) doFilter(args schedulerapi.ExtenderArgs) (*schedulerapi.ExtenderFilterResult, error) {
	var filteredNodes []v1.Node
	failedNodes := make(schedulerapi.FailedNodesMap)

	log := s.log.WithValues("pod", args.Pod.Name)
	log.V(5).Info("node filter request", "potential nodes", nodeNames(args.Nodes.Items))
	required, err := s.requiredStorage(args.Pod)
	if err != nil {
		return nil, fmt.Errorf("checking for unbound volumes: %v", err)
	}
	log.V(5).Info("needs PMEM", "bytes", required)

	var mutex sync.Mutex
	var waitgroup sync.WaitGroup
	for _, node := range args.Nodes.Items {
		if required == 0 {
			// Nothing to check.
			filteredNodes = append(filteredNodes, node)
			continue
		}

		// Check in parallel.
		node := node
		waitgroup.Add(1)
		go func() {
			fits, failReasons, err := s.nodeHasEnoughCapacity(required, node)
			mutex.Lock()
			defer mutex.Unlock()
			defer waitgroup.Done()
			switch {
			case fits:
				filteredNodes = append(filteredNodes, node)
			case failReasons != nil:
				failedNodes[node.Name] = strings.Join(failReasons, ",")
			case err != nil:
				failedNodes[node.Name] = fmt.Sprintf("checking for capacity: %v", err)
			}
		}()
	}
	waitgroup.Wait()

	log.V(5).Info("node filter result",
		"suitable nodes", nodeNames(filteredNodes),
		"failed nodes", failedNodes)
	return &schedulerapi.ExtenderFilterResult{
		Nodes: &v1.NodeList{
			Items: filteredNodes,
		},
		FailedNodes: failedNodes,
		Error:       "",
	}, nil
}

// requiredStorage sums up total size of all currently unbound
// persistent volumes and all inline ephemeral volumes. This is a
// rough estimate whether the pod may still fit onto a node.
func (s *scheduler) requiredStorage(pod *v1.Pod) (int64, error) {
	var total int64

	for _, volume := range pod.Spec.Volumes {
		if volume.PersistentVolumeClaim != nil {
			claimName := volume.PersistentVolumeClaim.ClaimName
			pvc, err := s.pvcLister.PersistentVolumeClaims(pod.Namespace).Get(claimName)
			if err != nil {
				return 0, fmt.Errorf("look up claim: %v", err)
			}

			if pvc.Status.Phase == v1.ClaimBound ||
				pvc.Spec.VolumeName != "" {
				// No need to check, the volume already exists.
				continue
			}

			scName := pvc.Spec.StorageClassName
			if scName == nil {
				// Shouldn't happen.
				continue
			}
			sc, err := s.scLister.Get(*scName)
			if err != nil {
				return 0, fmt.Errorf("look up storage class: %v", err)
			}
			if sc.Provisioner != s.driverName {
				// Not us.
				continue
			}
			if sc.VolumeBindingMode != nil &&
				*sc.VolumeBindingMode == storagev1.VolumeBindingImmediate {
				// Picking nodes for normal volumes will be handled by the master controller.
				continue
			}

			storage := pvc.Spec.Resources.Requests[v1.ResourceStorage]
			size := storage.Value()
			if size == 0 {
				// We don't know exactly how the driver is going to round up.
				// Let's use a conservative guess here - 1GiB.
				size = 1024 * 1024 * 1024
			}
			total += size
		}
		if volume.CSI != nil {
			if volume.CSI.Driver != s.driverName {
				// Not us.
				continue
			}
			p, err := parameters.Parse(parameters.EphemeralVolumeOrigin, volume.CSI.VolumeAttributes)
			if err != nil {
				return 0, fmt.Errorf("ephemeral inline volume %s: %v", volume.Name, err)
			}
			total += p.GetSize()
		}
	}
	return total, nil
}

// nodeHasEnoughCapacity determines whether a node has enough storage available. It either returns
// true if yes, a list of explanations why not, or an error if checking failed.
func (s *scheduler) nodeHasEnoughCapacity(required int64, node v1.Node) (bool, []string, error) {
	available, err := s.capacity.NodeCapacity(node.Name)
	if err != nil {
		return false, nil, fmt.Errorf("retrieve capacity: %v", err)
	}

	if available < required {
		return false, []string{fmt.Sprintf("only %vB of PMEM available, need %vB",
			resource.NewQuantity(available, resource.BinarySI),
			resource.NewQuantity(required, resource.BinarySI)),
		}, nil
	}

	// Success!
	return true, nil, nil
}

func nodeNames(nodes []v1.Node) []string {
	var names []string
	for _, node := range nodes {
		names = append(names, node.Name)
	}
	sort.Strings(names)
	return names
}
