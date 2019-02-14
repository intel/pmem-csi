/*
Copyright 2017 The Kubernetes Authors.
Copyright 2018 Intel Corporation.

SPDX-License-Identifier: Apache-2.0
*/

package pmemcommon

import (
	// "fmt"
	"io"

	"github.com/kubernetes-csi/csi-lib-utils/protosanitizer"
	"golang.org/x/net/context"
	"google.golang.org/grpc"
	"k8s.io/klog/glog"
	// "github.com/opentracing/opentracing-go"
	// otlog "github.com/opentracing/opentracing-go/log"
	// jaegercfg "github.com/uber/jaeger-client-go/config"
)

// LogGRPCServer logs the server-side call information via glog.
//
// Warning: at log levels >= 5 the recorded information includes all
// parameters, which potentially contains sensitive information like
// the secrets.
func LogGRPCServer(ctx context.Context, req interface{}, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (interface{}, error) {
	glog.V(3).Infof("GRPC call: %s", info.FullMethod)
	glog.V(5).Infof("GRPC request: %+v", protosanitizer.StripSecrets(req))
	resp, err := handler(ctx, req)
	if err != nil {
		glog.Errorf("GRPC error: %v", err)
	} else {
		glog.V(5).Infof("GRPC response: %+v", protosanitizer.StripSecrets(resp))
	}
	return resp, err
}

// LogGRPCClient does the same as LogGRPCServer, only on the client side.
func LogGRPCClient(ctx context.Context, method string, req, reply interface{}, cc *grpc.ClientConn, invoker grpc.UnaryInvoker, opts ...grpc.CallOption) error {
	glog.V(3).Infof("GRPC call: %s", method)
	glog.V(5).Infof("GRPC request: %+v", protosanitizer.StripSecrets(req))
	err := invoker(ctx, method, req, reply, cc, opts...)
	if err != nil {
		glog.Errorf("GRPC error: %v", err)
	} else {
		glog.V(5).Infof("GRPC response: %+v", protosanitizer.StripSecrets(reply))
	}
	return err
}

// TraceGRPCPayload adds the request and response as tags
// to the call's span, if the log level is five or higher.
// Warning: this may include sensitive information like the
// secrets.
// func TraceGRPCPayload(sp opentracing.Span, method string, req, reply interface{}, err error) {
// 	if glog.V(5) {
// 		sp.SetTag("request", req)
// 		if err == nil {
// 			sp.SetTag("response", reply)
// 		}
// 	}
// }

// Infof logs with glog.V(level).Infof() and in addition, always adds
// a log message to the current tracing span if the context has
// one. This ensures that spans which get recorded (not all do) have
// the full information.
func Infof(level glog.Level, ctx context.Context, format string, args ...interface{}) {
	glog.V(level).Infof(format, args...)
	// sp := opentracing.SpanFromContext(ctx)
	// if sp != nil {
	// 	sp.LogFields(otlog.Lazy(func(fv otlog.Encoder) {
	// 		fv.EmitString("message", fmt.Sprintf(format, args...))
	// 	}))
	// }
}

// Errorf does the same as Infof for error messages, except that
// it ignores the current log level.
func Errorf(ctx context.Context, format string, args ...interface{}) {
	glog.Errorf(format, args...)
	// sp := opentracing.SpanFromContext(ctx)
	// if sp != nil {
	// 	sp.LogFields(otlog.Lazy(func(fv otlog.Encoder) {
	// 		fv.EmitString("event", "error")
	// 		fv.EmitString("message", fmt.Sprintf(format, args...))
	// 	}))
	// }
}

// InitTracer initializes the global OpenTracing tracer, using Jaeger
// and the provided name for the current process. Must be called at
// the start of main(). The result is a function which should be
// called at the end of main() to clean up.
func InitTracer(component string) (closer io.Closer, err error) {
	// // Add support for the usual env variables, in particular
	// // JAEGER_AGENT_HOST, which is needed when running only one
	// // Jaeger agent per cluster.
	// cfg, err := jaegercfg.FromEnv()
	// if err != nil {
	// 	// parsing errors might happen here, such as when we get a string where we expect a number
	// 	return
	// }

	// // Initialize tracer singleton.
	// closer, err = cfg.InitGlobalTracer(component)
	return
}
