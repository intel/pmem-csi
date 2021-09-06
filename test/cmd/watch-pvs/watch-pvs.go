/*
Copyright 2017 The Kubernetes Authors.
Copyright 2020 Intel Coporation.

SPDX-License-Identifier: Apache-2.0
*/

package main

import (
	"context"
	"os"
	"time"

	"github.com/google/go-cmp/cmp"
	"k8s.io/client-go/informers"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/cache"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/klog/v2"
	"sigs.k8s.io/yaml"
)

func toYAML(obj interface{}) string {
	out, err := yaml.Marshal(obj)
	if err != nil {
		klog.Fatalf("marshal %+q: %v", obj, err)
	}
	return string(out)
}

func main() {
	ctx := context.Background()

	// get the KUBECONFIG from env if specified (useful for local/debug cluster)
	kubeconfigEnv := os.Getenv("KUBECONFIG")
	config, err := clientcmd.BuildConfigFromFlags("", kubeconfigEnv)
	if err != nil {
		klog.Fatalf("Failed to create config from KUBECONFIG=%s: %v", kubeconfigEnv, err)
	}

	clientset, err := kubernetes.NewForConfig(config)
	if err != nil {
		klog.Fatalf("Failed to create client: %v", err)
	}

	factory := informers.NewSharedInformerFactory(clientset, time.Hour)
	claimInformer := factory.Core().V1().PersistentVolumeClaims().Informer()
	volumeInformer := factory.Core().V1().PersistentVolumes().Informer()

	claimHandler := cache.ResourceEventHandlerFuncs{
		AddFunc: func(obj interface{}) {
			klog.Infof("PVC added:\n%s\n", toYAML(obj))
		},
		UpdateFunc: func(oldObj, newObj interface{}) {
			klog.Infof("PVC updated:\n%s\n%s\n",
				toYAML(newObj),
				cmp.Diff(oldObj, newObj),
			)
		},
		DeleteFunc: func(obj interface{}) {
			klog.Infof("PVC deleted:\n%s\n", toYAML(obj))
		},
	}
	claimInformer.AddEventHandlerWithResyncPeriod(claimHandler, time.Hour)

	volumeHandler := cache.ResourceEventHandlerFuncs{
		AddFunc: func(obj interface{}) {
			klog.Infof("PV added:\n%s\n", toYAML(obj))
		},
		UpdateFunc: func(oldObj, newObj interface{}) {
			klog.Infof("PV updated:\n%s\n%s\n",
				toYAML(newObj),
				cmp.Diff(oldObj, newObj),
			)
		},
		DeleteFunc: func(obj interface{}) {
			klog.Infof("PV deleted:\n%s\n", toYAML(obj))
		},
	}
	volumeInformer.AddEventHandlerWithResyncPeriod(volumeHandler, time.Hour)

	factory.Start(ctx.Done())
	<-ctx.Done()
}
