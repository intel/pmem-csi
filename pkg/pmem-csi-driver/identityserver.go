/*
Copyright 2017 The Kubernetes Authors.

SPDX-License-Identifier: Apache-2.0
*/

package pmemcsidriver

import (
	csi "github.com/container-storage-interface/spec/lib/go/csi/v0"
	"google.golang.org/grpc"
)

type identityServer struct {
	*DefaultIdentityServer
}

var _ PmemService = &identityServer{}

func NewIdentityServer(pmemd *pmemDriver) *identityServer {
	return &identityServer{
		DefaultIdentityServer: NewDefaultIdentityServer(pmemd.driver),
	}
}

func (ids *identityServer) RegisterService(rpcServer *grpc.Server) {
	csi.RegisterIdentityServer(rpcServer, ids)
}
