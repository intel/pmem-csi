/*
Copyright 2017 The Kubernetes Authors.

SPDX-License-Identifier: Apache-2.0
*/

package pmemcsidriver

type identityServer struct {
	*DefaultIdentityServer
}

func NewIdentityServer(pmemd *pmemDriver) *identityServer {
	return &identityServer{
		DefaultIdentityServer: NewDefaultIdentityServer(pmemd.driver),
	}
}
