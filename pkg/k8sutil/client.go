/*
Copyright 2020 Intel Coporation.

SPDX-License-Identifier: Apache-2.0
*/

package k8sutil

import (
	"context"
	"fmt"
	"os"
	"regexp"
	"strconv"

	"github.com/intel/pmem-csi/pkg/version"
	apiclient "k8s.io/apiextensions-apiserver/pkg/client/clientset/clientset"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/discovery"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
)

// NewClient connects to an API server either through KUBECONFIG (if set) or
// through the in-cluster env variables.
func NewClient(qps float64, burst int) (kubernetes.Interface, error) {
	var config *rest.Config
	var err error

	if kubeconfig := os.Getenv("KUBECONFIG"); kubeconfig != "" {
		config, err = clientcmd.BuildConfigFromFlags("" /* master */, kubeconfig)
	} else {
		config, err = rest.InClusterConfig()
	}
	if err != nil {
		return nil, fmt.Errorf("create Kubernetes REST config: %v", err)
	}
	config.QPS = float32(qps)
	config.Burst = burst
	client, err := kubernetes.NewForConfig(config)
	if err != nil {
		return nil, fmt.Errorf("create Kubernetes client: %v", err)
	}
	return client, nil
}

// GetKubernetesVersion returns kubernetes server version
func GetKubernetesVersion(cfg *rest.Config) (*version.Version, error) {
	client, err := discovery.NewDiscoveryClientForConfig(cfg)
	if err != nil {
		return nil, err
	}
	ver, err := client.ServerVersion()
	if err != nil {
		return nil, err
	}

	// Suppress all non digits, version might contain special charcters like, <number>+
	reg, _ := regexp.Compile("[^0-9]+")
	major, err := strconv.Atoi(reg.ReplaceAllString(ver.Major, ""))
	if err != nil {
		return nil, fmt.Errorf("failed to parse Kubernetes major version %q: %v", ver.Major, err)
	}
	minor, err := strconv.Atoi(reg.ReplaceAllString(ver.Minor, ""))
	if err != nil {
		return nil, fmt.Errorf("failed to parse Kubernetes minor version %q: %v", ver.Minor, err)
	}

	v := version.NewVersion(uint(major), uint(minor))
	return &v, nil
}

// IsOpenShift determines whether the cluster is based on OpenShift.
func IsOpenShift(cfg *rest.Config) (bool, error) {
	client, err := apiclient.NewForConfig(cfg)
	if err != nil {
		return false, err
	}
	// For our purposed we run on OpenShift if the scheduler operator is installed.
	if _, err := client.ApiextensionsV1().CustomResourceDefinitions().Get(context.Background(), "schedulers.config.openshift.io", metav1.GetOptions{}); err != nil {
		if apierrors.IsNotFound(err) {
			return false, nil
		}
		return false, fmt.Errorf("check for OpenShift CRD: %v", err)
	}
	return true, nil
}
