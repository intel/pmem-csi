/*
Copyright 2017 The Kubernetes Authors.

SPDX-License-Identifier: Apache-2.0
*/

package pmemcsidriver

import (
	"crypto/tls"
	"fmt"
	"sync"

	"github.com/intel/pmem-csi/pkg/pmem-grpc"
	"github.com/kubernetes-csi/csi-lib-utils/metrics"
	"google.golang.org/grpc"
	"k8s.io/klog/v2"
)

type Service interface {
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

func (s *NonBlockingGRPCServer) Start(endpoint string, tlsConfig *tls.Config, csiMetricsManager metrics.CSIMetricsManager, services ...Service) error {
	if endpoint == "" {
		return fmt.Errorf("endpoint cannot be empty")
	}
	rpcServer, l, err := pmemgrpc.NewServer(endpoint, tlsConfig, csiMetricsManager)
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
		klog.V(3).Infof("Listening for connections on address: %v", l.Addr())
		if err := rpcServer.Serve(l); err != nil {
			klog.Errorf("Server Listen failure: %s", err.Error())
		}
		klog.V(3).Infof("Server on '%s' stopped", endpoint)
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
