package pmemgrpc

import (
	"context"
	"fmt"
	"net"
	"os"
	"strings"
	"time"

	"github.com/golang/glog"
	"github.com/grpc-ecosystem/go-grpc-middleware"
	"github.com/grpc-ecosystem/grpc-opentracing/go/otgrpc"
	"github.com/opentracing/opentracing-go"
	"google.golang.org/grpc"
	"google.golang.org/grpc/connectivity"

	"github.com/intel/pmem-csi/pkg/pmem-common"
)

func Connect(endpoint string, timeout time.Duration) (*grpc.ClientConn, error) {
	_, address, err := parseEndpoint(endpoint)
	if err != nil {
		return nil, err
	}
	glog.V(2).Infof("Connecting to %s", address)
	dialOptions := []grpc.DialOption{
		grpc.WithInsecure(),
		grpc.WithBackoffMaxDelay(time.Second),
	}

	conn, err := grpc.Dial(address, dialOptions...)
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	for {
		if !conn.WaitForStateChange(ctx, conn.GetState()) {
			glog.V(4).Infof("Connection timed out")
			return conn, nil // return nil, subsequent GetPluginInfo will show the real connection error
		}
		if conn.GetState() == connectivity.Ready {
			glog.V(3).Infof("Connected")
			return conn, nil
		}
		glog.V(4).Infof("Still trying, connection is %s", conn.GetState())
	}
}

type RegisterService func(*grpc.Server)

func StartNewServer(endpoint string, serviceRegister RegisterService) error {
	proto, addr, err := parseEndpoint(endpoint)
	if err != nil {
		return err
	}

	if proto == "unix" {
		if err = os.Remove(addr); err != nil && !os.IsNotExist(err) {
			return err
		}
	}

	listener, err := net.Listen(proto, addr)
	if err != nil {
		return err
	}

	interceptor := grpc_middleware.ChainUnaryServer(
		otgrpc.OpenTracingServerInterceptor(
			opentracing.GlobalTracer(),
			otgrpc.SpanDecorator(pmemcommon.TraceGRPCPayload),
		),
		pmemcommon.LogGRPCServer,
	)
	opts := []grpc.ServerOption{
		grpc.UnaryInterceptor(interceptor),
	}

	server := grpc.NewServer(opts...)

	serviceRegister(server)

	go func(server *grpc.Server, listener net.Listener) {
		glog.Infof("Listening for connections on address: %#v", listener.Addr())

		if err := server.Serve(listener); err != nil {
			glog.Errorf("Server Listen failure: %s", err.Error())
		}
		glog.Infof("Server stopped !!!")
	}(server, listener)

	return nil
}

func parseEndpoint(ep string) (string, string, error) {
	if strings.HasPrefix(strings.ToLower(ep), "unix://") || strings.HasPrefix(strings.ToLower(ep), "tcp://") {
		s := strings.SplitN(ep, "://", 2)
		if s[1] != "" {
			return s[0], s[1], nil
		}
	}
	return "", "", fmt.Errorf("Invalid endpoint: %v", ep)
}
