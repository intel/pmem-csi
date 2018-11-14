package pmemgrpc

import (
	"context"
	"fmt"
	"net"
	"os"
	"strings"
	"time"

	"github.com/golang/glog"
	"google.golang.org/grpc"
	"google.golang.org/grpc/connectivity"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/resolver"

	"github.com/intel/csi-pmem/pkg/pmem-common"
)

func Connect(endpoint, certFile string, timeout time.Duration) (*grpc.ClientConn, error) {
	_, address, err := parseEndpoint(endpoint)
	if err != nil {
		return nil, err
	}
	glog.V(2).Infof("Connecting to %s", address)
	dialOptions := []grpc.DialOption{grpc.WithBackoffMaxDelay(time.Second)}
	if certFile != "" {
		cred, err := credentials.NewClientTLSFromFile(certFile, "")
		if err != nil {
			return nil, fmt.Errorf("failed load certificates : %s", err.Error())
		}
		dialOptions = append(dialOptions, grpc.WithTransportCredentials(cred))
	} else {
		dialOptions = append(dialOptions, grpc.WithInsecure())
	}

	resolver.SetDefaultScheme("dns")

	conn, err := grpc.Dial(address, dialOptions...)
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	for {
		if !conn.WaitForStateChange(ctx, conn.GetState()) {
			glog.V(4).Infof("Connection timed out")
			conn.Close()
			return nil, fmt.Errorf("Connection time out")
		}
		if conn.GetState() == connectivity.Ready {
			glog.V(3).Infof("Connected")
			return conn, nil
		}
		glog.V(4).Infof("Still trying, connection is %s", conn.GetState())
	}
}

type RegisterService func(*grpc.Server)

func StartNewServer(endpoint string, certFile string, keyFile string, serviceRegister RegisterService) (*grpc.Server, error) {
	proto, addr, err := parseEndpoint(endpoint)
	if err != nil {
		return nil, err
	}

	if proto == "unix" {
		if err = os.Remove(addr); err != nil && !os.IsNotExist(err) {
			return nil, err
		}
	}

	listener, err := net.Listen(proto, addr)
	if err != nil {
		return nil, err
	}

	opts := []grpc.ServerOption{grpc.UnaryInterceptor(pmemcommon.LogGRPCServer)}
	if certFile != "" && keyFile != "" {
		cred, err := credentials.NewServerTLSFromFile(certFile, keyFile)
		if err != nil {
			return nil, fmt.Errorf("Failed to load credentials: %s", err.Error())
		}
		opts = append(opts, grpc.Creds(cred))
	}

	server := grpc.NewServer(opts...)

	serviceRegister(server)

	go func(server *grpc.Server, listener net.Listener) {
		glog.Infof("Listening for connections on address: %v", listener.Addr())

		if err := server.Serve(listener); err != nil {
			glog.Errorf("Server Listen failure: %s", err.Error())
		}
		glog.Infof("Server stopped !!!")
	}(server, listener)

	return server, nil
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
