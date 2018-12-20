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
	"k8s.io/klog/glog"
)

type PmemService interface {
	// RegisterService will be called by NonBlockingGRPCServer whenever
	// its about to start a grpc server on an endpoint.
	RegisterService(s *grpc.Server)
}

// NonBlocking server
type NonBlockingGRPCServer struct {
	wg      sync.WaitGroup
	servers []*grpc.Server
}

func NewNonBlockingGRPCServer() *NonBlockingGRPCServer {
	return &NonBlockingGRPCServer{}
}

func (s *NonBlockingGRPCServer) Start(endpoint string, services ...PmemService) error {
	if endpoint == "" {
		return fmt.Errorf("endpoint cannot be empty")
	}
	rpcServer, l, err := pmemgrpc.NewServer(endpoint)
	if err != nil {
		return nil
	}
	for _, service := range services {
		service.RegisterService(rpcServer)
	}
	s.servers = append(s.servers, rpcServer)

	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		glog.Infof("Listening for connections on address: %v", l.Addr())
		if err := rpcServer.Serve(l); err != nil {
			glog.Errorf("Server Listen failure: %s", err.Error())
		}
		glog.Infof("Server on '%s' stopped !!!", endpoint)
	}()

	return nil
}

func (s *NonBlockingGRPCServer) Wait() {
	s.wg.Wait()
}

func (s *NonBlockingGRPCServer) Stop() {
	for _, s := range s.servers {
		s.GracefulStop()
	}
}

func (s *NonBlockingGRPCServer) ForceStop() {
	for _, s := range s.servers {
		s.Stop()
	}
}
