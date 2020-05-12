/*
Copyright 2020 The Kubernetes Authors.

SPDX-License-Identifier: Apache-2.0
*/

package controller

import (
	"github.com/intel/pmem-csi/pkg/version"
	"k8s.io/client-go/rest"
	"sigs.k8s.io/controller-runtime/pkg/manager"
)

// ControllerOptions type defintions for options to be passed to reconcile controller
type ControllerOptions struct {
	// K8sVersion represents version of the running Kubernetes cluster/API server
	K8sVersion version.Version
	// Namespace to use for namespace-scoped sub-resources created by the controller
	Namespace string
	// DriverImage to use as default image for driver deployment
	DriverImage string
	// Config kubernetes config used
	Config *rest.Config
}

// AddToManagerFuncs is a list of functions to add all Controllers to the Manager
var AddToManagerFuncs []func(manager.Manager, ControllerOptions) error

// AddToManager adds all Controllers to the Manager
func AddToManager(m manager.Manager, opts ControllerOptions) error {
	for _, f := range AddToManagerFuncs {
		if err := f(m, opts); err != nil {
			return err
		}
	}
	return nil
}
