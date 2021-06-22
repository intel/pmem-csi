/*
Copyright 2017 The Kubernetes Authors.

SPDX-License-Identifier: Apache-2.0
*/

package pmemcsidriver

import (
	"context"
	"crypto/tls"
	"fmt"
	"sync"

	pmemlog "github.com/intel/pmem-csi/pkg/logger"
	pmemgrpc "github.com/intel/pmem-csi/pkg/pmem-grpc"
	"github.com/kubernetes-csi/csi-lib-utils/metrics"
	"google.golang.org/grpc"
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

func (s *NonBlockingGRPCServer) Start(ctx context.Context, endpoint, errorPrefix string, tlsConfig *tls.Config, csiMetricsManager metrics.CSIMetricsManager, services ...Service) error {
	if endpoint == "" {
		return fmt.Errorf("endpoint cannot be empty")
	}
	rpcServer, l, err := pmemgrpc.NewServer(endpoint, errorPrefix, tlsConfig, csiMetricsManager)
	if err != nil {
		return nil
	}
	for _, service := range services {
		service.RegisterService(rpcServer)
	}
	s.servers = append(s.servers, rpcServer)

	logger := pmemlog.Get(ctx).WithName("GRPC-server").WithValues("endpoint", endpoint)
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		logger.V(3).Info("Listening for connections")
		if err := rpcServer.Serve(l); err != nil {
			logger.Error(err, "Listen failure")
		}
		logger.V(3).Info("Stopped")
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
