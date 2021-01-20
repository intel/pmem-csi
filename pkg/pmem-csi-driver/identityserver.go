/*
Copyright 2017 The Kubernetes Authors.

SPDX-License-Identifier: Apache-2.0
*/

package pmemcsidriver

import (
	csi "github.com/container-storage-interface/spec/lib/go/csi"
	grpcserver "github.com/intel/pmem-csi/pkg/grpc-server"
	"golang.org/x/net/context"
	"google.golang.org/grpc"
)

type identityServer struct {
	name       string
	version    string
	pluginCaps []*csi.PluginCapability
}

var _ grpcserver.Service = &identityServer{}

func NewIdentityServer(name, version string) *identityServer {
	return &identityServer{
		name:    name,
		version: version,
		pluginCaps: []*csi.PluginCapability{
			{
				Type: &csi.PluginCapability_Service_{
					Service: &csi.PluginCapability_Service{
						Type: csi.PluginCapability_Service_CONTROLLER_SERVICE,
					},
				},
			},
			{
				Type: &csi.PluginCapability_Service_{
					Service: &csi.PluginCapability_Service{
						Type: csi.PluginCapability_Service_VOLUME_ACCESSIBILITY_CONSTRAINTS,
					},
				},
			},
		},
	}
}

func (ids *identityServer) RegisterService(rpcServer *grpc.Server) {
	csi.RegisterIdentityServer(rpcServer, ids)
}

func (ids *identityServer) GetPluginInfo(ctx context.Context, req *csi.GetPluginInfoRequest) (*csi.GetPluginInfoResponse, error) {
	return &csi.GetPluginInfoResponse{
		Name:          ids.name,
		VendorVersion: ids.version,
	}, nil
}

func (ids *identityServer) Probe(ctx context.Context, req *csi.ProbeRequest) (*csi.ProbeResponse, error) {
	return &csi.ProbeResponse{}, nil
}

func (ids *identityServer) GetPluginCapabilities(ctx context.Context, req *csi.GetPluginCapabilitiesRequest) (*csi.GetPluginCapabilitiesResponse, error) {

	return &csi.GetPluginCapabilitiesResponse{
		Capabilities: ids.pluginCaps,
	}, nil
}
