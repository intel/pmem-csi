/*
Copyright 2020 Intel Corp.

SPDX-License-Identifier: Apache-2.0
*/

package scheduler

import (
	"context"
	"fmt"

	"github.com/container-storage-interface/spec/lib/go/csi"

	"github.com/intel/pmem-csi/pkg/registryserver"
)

type capacityFromRegistry struct {
	rs *registryserver.RegistryServer
}

func CapacityViaRegistry(rs *registryserver.RegistryServer) Capacity {
	return capacityFromRegistry{rs}
}

// NodeCapacity implements the necessary method for the NodeCapacity interface based
// on a registry server.
func (c capacityFromRegistry) NodeCapacity(nodeName string) (int64, error) {
	conn, err := c.rs.ConnectToNodeController(nodeName)
	if err != nil {
		return 0, fmt.Errorf("connect to PMEM-CSI on node %q: %v", nodeName, err)
	}
	defer conn.Close()

	csiClient := csi.NewControllerClient(conn)
	// We assume here that storage class parameters do not matter.
	resp, err := csiClient.GetCapacity(context.Background(), &csi.GetCapacityRequest{})
	if err != nil {
		// We cause an abort of scheduling by treating this as error.
		// A less drastic reaction would be to filter out the node.
		return 0, fmt.Errorf("get capacity from node %q: %v", nodeName, err)
	}
	return resp.AvailableCapacity, nil
}
