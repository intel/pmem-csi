/*
Copyright 2017 The Kubernetes Authors.

SPDX-License-Identifier: Apache-2.0
*/

package pmemcsidriver

import (
	"fmt"
	"sync"

	"github.com/intel/pmem-csi/pkg/pmem-grpc"
	"google.golang.org/grpc"
)

// Defines Non blocking GRPC server interfaces
type NonBlockingGRPCServer interface {
	// Start services at the endpoint
	Start(endpoint string, register pmemgrpc.RegisterService) error
	// Waits for the service to stop
	Wait()
	// Stops the service gracefully
	Stop()
	// Stops the service forcefully
	ForceStop()
}

func NewNonBlockingGRPCServer() NonBlockingGRPCServer {
	return &nonBlockingGRPCServer{}
}

// NonBlocking server
type nonBlockingGRPCServer struct {
	wg      sync.WaitGroup
	servers []*grpc.Server
}

func (s *nonBlockingGRPCServer) Start(endpoint string, register pmemgrpc.RegisterService) error {
	if endpoint == "" {
		return fmt.Errorf("endpoint cannot be empty")
	}
	s.wg.Add(1)
	return pmemgrpc.StartNewServer(endpoint, register)
}

func (s *nonBlockingGRPCServer) Wait() {
	s.wg.Wait()
}

func (s *nonBlockingGRPCServer) Stop() {
	for _, s := range s.servers {
		s.GracefulStop()
	}
}

func (s *nonBlockingGRPCServer) ForceStop() {
	for _, s := range s.servers {
		s.Stop()
	}
}
