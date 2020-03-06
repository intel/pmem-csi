/*
Copyright 2020 The Kubernetes Authors.

SPDX-License-Identifier: Apache-2.0
*/

package utils

import (
	"io/ioutil"
	"os"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/klog"
)

const (
	namespaceEnvVar          = "WATCH_NAMESPACE"
	namespaceFile            = "/var/run/secrets/kubernetes.io/serviceaccount/namespace"
	defaultOperatorNamespace = metav1.NamespaceSystem
)

// GetNamespace returns the namespace of the operator pod
// defaults to "kube-system"
func GetNamespace() string {
	ns := os.Getenv(namespaceEnvVar)
	if ns == "" {
		// If environment variable not set, give it a try to fetch it from
		// mounted filesystem by Kubernetes
		data, err := ioutil.ReadFile(namespaceFile)
		if err != nil {
			klog.Infof("Could not read namespace from %q: %v", namespaceFile, err)
		} else {
			ns = string(data)
			klog.Infof("Operator Namespace: %q", ns)
		}
	}

	if ns == "" {
		ns = defaultOperatorNamespace
	}

	return ns
}
