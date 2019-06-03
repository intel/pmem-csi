package pmemgrpc

import (
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"io/ioutil"
	"net"
	"os"
	"strings"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/resolver"
	"k8s.io/klog/glog"

	pmemcommon "github.com/intel/pmem-csi/pkg/pmem-common"
)

func unixDialer(addr string, timeout time.Duration) (net.Conn, error) {
	return net.DialTimeout("unix", addr, timeout)
}

//Connect is a helper function to initiate a grpc client connection to server running at endpoint using tlsConfig
func Connect(endpoint string, tlsConfig *tls.Config) (*grpc.ClientConn, error) {
	proto, address, err := parseEndpoint(endpoint)
	if err != nil {
		return nil, err
	}
	glog.V(3).Infof("Connecting to %s", address)
	dialOptions := []grpc.DialOption{grpc.WithBackoffMaxDelay(time.Second)}
	if tlsConfig != nil {
		dialOptions = append(dialOptions, grpc.WithTransportCredentials(credentials.NewTLS(tlsConfig)))
	} else {
		dialOptions = append(dialOptions, grpc.WithInsecure())
	}

	if proto == "tcp" {
		resolver.SetDefaultScheme("dns")
	} else if proto == "unix" {
		dialOptions = append(dialOptions, grpc.WithDialer(unixDialer))
	}

	return grpc.Dial(address, dialOptions...)

}

//NewServer is a helper function to start a grpc server at given endpoint and uses provided tlsConfig
func NewServer(endpoint string, tlsConfig *tls.Config) (*grpc.Server, net.Listener, error) {
	proto, addr, err := parseEndpoint(endpoint)
	if err != nil {
		return nil, nil, err
	}

	if proto == "unix" {
		if err = os.Remove(addr); err != nil && !os.IsNotExist(err) {
			return nil, nil, err
		}
	}

	listener, err := net.Listen(proto, addr)
	if err != nil {
		return nil, nil, err
	}

	opts := []grpc.ServerOption{grpc.UnaryInterceptor(pmemcommon.LogGRPCServer)}
	if tlsConfig != nil {
		opts = append(opts, grpc.Creds(credentials.NewTLS(tlsConfig)))
	}

	return grpc.NewServer(opts...), listener, nil
}

//LoadServerTLS prepares the TLS configuration needed for a server with the given certificate files
func LoadServerTLS(caFile, certFile, keyFile, peerName string) (*tls.Config, error) {
	certPool, peerCert, err := loadCertificate(caFile, certFile, keyFile)
	if err != nil {
		return nil, err
	}

	return &tls.Config{
		Certificates: []tls.Certificate{*peerCert},
		ClientCAs:    certPool,
		ClientAuth:   tls.RequireAndVerifyClientCert,
		VerifyPeerCertificate: func(rawCerts [][]byte, verifiedChains [][]*x509.Certificate) error {
			// Common name check when accepting a connection from a client.
			if peerName == "" {
				// All names allowed.
				return nil
			}
			if len(verifiedChains) == 0 ||
				len(verifiedChains[0]) == 0 {
				return fmt.Errorf("no valid certificate")
			}
			commonName := verifiedChains[0][0].Subject.CommonName
			if commonName != peerName {
				return fmt.Errorf("expected CN %q, got %q", peerName, commonName)
			}
			return nil
		},
	}, nil
}

//LoadClientTLS prepares the TLS configuration that can be used by a client while connecting to a server.
// peerName must be provided when expecting the server to offer a certificate with that CommonName
func LoadClientTLS(caFile, certFile, keyFile, peerName string) (*tls.Config, error) {
	certPool, peerCert, err := loadCertificate(caFile, certFile, keyFile)
	if err != nil {
		return nil, err
	}
	glog.V(3).Infof("Using Servername: %s", peerName)
	return &tls.Config{
		ServerName:   peerName,
		Certificates: []tls.Certificate{*peerCert},
		RootCAs:      certPool,
	}, nil
}

func loadCertificate(caFile, certFile, keyFile string) (*x509.CertPool, *tls.Certificate, error) {
	peerCert, err := tls.LoadX509KeyPair(certFile, keyFile)
	if err != nil {
		return nil, nil, err
	}

	caCert, err := ioutil.ReadFile(caFile)
	if err != nil {
		return nil, nil, err
	}

	certPool := x509.NewCertPool()
	if ok := certPool.AppendCertsFromPEM(caCert); !ok {
		return nil, nil, fmt.Errorf("failed to append certs from %s", caFile)
	}

	return certPool, &peerCert, nil
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
