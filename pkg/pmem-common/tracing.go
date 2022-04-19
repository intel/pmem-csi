/*
Copyright 2017 The Kubernetes Authors.
Copyright 2018 Intel Corporation.

SPDX-License-Identifier: Apache-2.0
*/

package pmemcommon

import (
	"github.com/kubernetes-csi/csi-lib-utils/protosanitizer"
	"golang.org/x/net/context"
	"google.golang.org/grpc"
	"k8s.io/klog/v2"
)

// LogGRPCServer logs the server-side call information via klog.
func LogGRPCServer(ctx context.Context, req interface{}, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (interface{}, error) {
	logger := klog.FromContext(ctx)
	values := []interface{}{"full-method", info.FullMethod}
	if logger.V(5).Enabled() {
		values = append(values, "request", protosanitizer.StripSecrets(req))
	}
	logger.V(3).Info("Processing gRPC call", values...)
	resp, err := handler(ctx, req)
	if err != nil {
		logger.Error(err, "gRPC call failed")
	} else {
		logger.V(5).Info("Completed gRPC call", "response", protosanitizer.StripSecrets(resp))
	}
	return resp, err
}

// LogGRPCClient does the same as LogGRPCServer, only on the client side.
func LogGRPCClient(ctx context.Context, method string, req, reply interface{}, cc *grpc.ClientConn, invoker grpc.UnaryInvoker, opts ...grpc.CallOption) error {
	logger := klog.FromContext(ctx)
	values := []interface{}{"full-method", method}
	if logger.V(5).Enabled() {
		values = append(values, "request", protosanitizer.StripSecrets(req))
	}
	logger.V(3).Info("Invoking gRPC call", values...)
	err := invoker(ctx, method, req, reply, cc, opts...)
	if err != nil {
		logger.Error(err, "Received gRPC error")
	} else {
		logger.V(5).Info("Received gRPC response", "response", protosanitizer.StripSecrets(reply))
	}
	return err
}
