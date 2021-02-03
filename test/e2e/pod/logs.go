/*
Copyright 2021 Intel Corporation.

SPDX-License-Identifier: Apache-2.0
*/

package pod

import (
	"context"
	"fmt"
	"time"

	"k8s.io/client-go/kubernetes"
	e2epod "k8s.io/kubernetes/test/e2e/framework/pod"
)

// Retrieves a container log, with retry in case of errors.
// It is okay if the pod does not exist yet.
func Logs(ctx context.Context, client kubernetes.Interface, namespace, pod, container string) (output string, err error) {
	for {
		output, err = e2epod.GetPodLogs(client, namespace, pod, container)
		if err == nil {
			return
		}
		if ctx.Err() == nil {
			return "", fmt.Errorf("waiting for pod log of container %s in pod %s/%s: %v: %v", container, namespace, pod, ctx.Err(), err)
		}
		time.Sleep(5 * time.Second)
	}
}
