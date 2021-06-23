/*
Copyright 2020 Intel Coporation.

SPDX-License-Identifier: Apache-2.0
*/

package k8sutil

import (
	"context"
	"io/ioutil"
	"os"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	pmemlog "github.com/intel/pmem-csi/pkg/logger"
)

const (
	namespaceEnvVar          = "WATCH_NAMESPACE"
	namespaceFile            = "/var/run/secrets/kubernetes.io/serviceaccount/namespace"
	defaultOperatorNamespace = metav1.NamespaceSystem
)

// GetNamespace returns the namespace of the operator pod
// defaults to "kube-system"
func GetNamespace(ctx context.Context) string {
	logger := pmemlog.Get(ctx).WithValues("namespace-file", namespaceFile)
	ns := os.Getenv(namespaceEnvVar)
	if ns == "" {
		// If environment variable not set, give it a try to fetch it from
		// mounted filesystem by Kubernetes
		data, err := ioutil.ReadFile(namespaceFile)
		if err != nil {
			logger.Info("Could not read namespace from secret, using fallback "+defaultOperatorNamespace,
				"error", err,
			)
		} else {
			ns = string(data)
			logger.V(3).Info("Retrieved namespace from secret", "namespace", ns)
		}
	}

	if ns == "" {
		ns = defaultOperatorNamespace
	}

	return ns
}
