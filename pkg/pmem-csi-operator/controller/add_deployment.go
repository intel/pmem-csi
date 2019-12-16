package controller

import (
	"github.com/intel/pmem-csi/pkg/pmem-csi-operator/controller/deployment"
)

func init() {
	// AddToManagerFuncs is a list of functions to create controllers and add them to a manager.
	AddToManagerFuncs = append(AddToManagerFuncs, deployment.Add)
}
