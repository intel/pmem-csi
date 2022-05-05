package pmemgrpc

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"io/ioutil"
	"net"
	"os"
	"strings"
	"sync/atomic"
	"time"

	"github.com/kubernetes-csi/csi-lib-utils/connection"
	"github.com/kubernetes-csi/csi-lib-utils/metrics"
	"google.golang.org/grpc"
	"google.golang.org/grpc/backoff"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/keepalive"
	"google.golang.org/grpc/resolver"
	"google.golang.org/grpc/status"
	"k8s.io/klog/v2"

	pmemcommon "github.com/intel/pmem-csi/pkg/pmem-common"
)

// grpcRequestCounter is used to assign a unique ID to all incoming gRPC requests.
var grpcRequestCounter uint64

func unixDialer(ctx context.Context, addr string) (net.Conn, error) {
	dialer := net.Dialer{}
	return dialer.DialContext(ctx, "unix", addr)
}

//Connect is a helper function to initiate a grpc client connection to server running at endpoint using tlsConfig
func Connect(endpoint string, tlsConfig *tls.Config, dialOptions ...grpc.DialOption) (*grpc.ClientConn, error) {
	proto, address, err := parseEndpoint(endpoint)
	if err != nil {
		return nil, err
	}

	connectParams := grpc.ConnectParams{
		Backoff: backoff.DefaultConfig,
	}
	connectParams.Backoff.MaxDelay = time.Second
	dialOptions = append(dialOptions, grpc.WithConnectParams(connectParams))
	if tlsConfig != nil {
		dialOptions = append(dialOptions, grpc.WithTransportCredentials(credentials.NewTLS(tlsConfig)))
	} else {
		dialOptions = append(dialOptions, grpc.WithInsecure())
	}

	if proto == "tcp" {
		resolver.SetDefaultScheme("dns")
	} else if proto == "unix" {
		dialOptions = append(dialOptions, grpc.WithContextDialer(unixDialer))
	}
	// This is necessary when connecting via TCP and does not hurt
	// when using Unix domain sockets. It ensures that gRPC detects a dead connection
	// in a timely manner.
	// Code lifted from https://github.com/kubernetes-csi/csi-test/commit/6b8830bf5959a1c51c6e98fe514b22818b51eeeb
	dialOptions = append(dialOptions, grpc.WithKeepaliveParams(keepalive.ClientParameters{PermitWithoutStream: true}))

	return grpc.Dial(address, dialOptions...)
}

// NewServer is a helper function to start a grpc server at the given endpoint.
// The error prefix is added to all error messages if not empty.
func NewServer(endpoint, errorPrefix string, tlsConfig *tls.Config, csiMetricsManager metrics.CSIMetricsManager, opts ...grpc.ServerOption) (*grpc.Server, net.Listener, error) {
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

	interceptors := []grpc.UnaryServerInterceptor{
		func(ctx context.Context, req interface{}, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (interface{}, error) {
			// Prepare a logger instance which always adds GRPC as prefix and a unique
			// counter. This makes it possible to determine which log messages belong
			// to which request and which are unrelated to gRPC.
			logger := klog.FromContext(ctx)
			methodName := info.FullMethod[strings.LastIndex(info.FullMethod, "/")+1:]
			logger = logger.WithName(methodName).WithValues("request-counter", atomic.AddUint64(&grpcRequestCounter, 1))
			ctx = klog.NewContext(ctx, logger)

			resp, err := handler(ctx, req)
			if errorPrefix != "" && err != nil {
				// We loose any additional details here that might be attached
				// to the status, but that's okay because we shouldn't have any.
				originalStatus := status.Convert(err)
				extendedStatus := status.New(originalStatus.Code(),
					errorPrefix+": "+originalStatus.Message())
				// Return the extended error.
				return resp, extendedStatus.Err()
			}
			return resp, err
		},
		pmemcommon.LogGRPCServer,
	}
	if csiMetricsManager != nil {
		interceptors = append(interceptors,
			connection.ExtendedCSIMetricsManager{CSIMetricsManager: csiMetricsManager}.RecordMetricsServerInterceptor)
	}
	opts = append(opts, grpc.ChainUnaryInterceptor(interceptors...))
	if tlsConfig != nil {
		opts = append(opts, grpc.Creds(credentials.NewTLS(tlsConfig)))
	}

	return grpc.NewServer(opts...), listener, nil
}

// ServerTLS prepares the TLS configuration needed for a server with given
// encoded certficate and private key.
func ServerTLS(ctx context.Context, caCert, cert, key []byte, peerName string) (*tls.Config, error) {
	certPool := x509.NewCertPool()
	if ok := certPool.AppendCertsFromPEM(caCert); !ok {
		return nil, fmt.Errorf("failed to  append CA certificate to pool")
	}

	certificate, err := tls.X509KeyPair(cert, key)
	if err != nil {
		return nil, err
	}

	return serverConfig(ctx, certPool, &certificate, peerName), nil
}

// LoadServerTLS prepares the TLS configuration needed for a server with the given certificate files.
// peerName is either the name that the client is expected to have a certificate for or empty,
// in which case any client is allowed to connect.
func LoadServerTLS(ctx context.Context, caFile, certFile, keyFile, peerName string) (*tls.Config, error) {
	certPool, peerCert, err := loadCertificate(caFile, certFile, keyFile)
	if err != nil {
		return nil, err
	}
	return serverConfig(ctx, certPool, peerCert, peerName), nil
}

func serverConfig(ctx context.Context, certPool *x509.CertPool, peerCert *tls.Certificate, peerName string) *tls.Config {
	logger := klog.FromContext(ctx).WithName("serverConfig").WithValues("peername", peerName)
	return &tls.Config{
		GetConfigForClient: func(info *tls.ClientHelloInfo) (*tls.Config, error) {
			if info == nil {
				return nil, errors.New("nil client info passed")
			}
			ciphers := []uint16{}
			for _, c := range info.CipherSuites {
				// filter out all insecure ciphers from client offered list
				switch c {
				case tls.TLS_ECDHE_RSA_WITH_3DES_EDE_CBC_SHA,
					tls.TLS_RSA_WITH_3DES_EDE_CBC_SHA,
					tls.TLS_ECDHE_RSA_WITH_RC4_128_SHA,
					tls.TLS_ECDHE_RSA_WITH_AES_128_CBC_SHA,
					tls.TLS_ECDHE_RSA_WITH_AES_256_CBC_SHA,
					tls.TLS_RSA_WITH_RC4_128_SHA,
					tls.TLS_ECDHE_ECDSA_WITH_RC4_128_SHA,
					tls.TLS_ECDHE_ECDSA_WITH_AES_128_CBC_SHA,
					tls.TLS_ECDHE_ECDSA_WITH_AES_256_CBC_SHA,
					tls.TLS_RSA_WITH_AES_128_CBC_SHA,
					tls.TLS_RSA_WITH_AES_256_CBC_SHA:

					continue
				default:
					ciphers = append(ciphers, c)
				}
			}

			config := &tls.Config{
				MinVersion:    tls.VersionTLS12,
				Renegotiation: tls.RenegotiateNever,
				Certificates:  []tls.Certificate{*peerCert},
				ClientCAs:     certPool,
				CipherSuites:  ciphers,
				VerifyPeerCertificate: func(rawCerts [][]byte, verifiedChains [][]*x509.Certificate) error {
					// Common name check when accepting a connection from a client.
					if peerName == "" {
						// All names allowed.
						return nil
					}

					if len(verifiedChains) == 0 ||
						len(verifiedChains[0]) == 0 {
						return errors.New("no valid certificate")
					}

					for _, name := range verifiedChains[0][0].DNSNames {
						logger.V(5).Info("verify peer", "dnsname", name)
						if name == peerName {
							return nil
						}
					}
					// For backword compatibility - using CN as hostName
					commonName := verifiedChains[0][0].Subject.CommonName
					if commonName == peerName {
						logger.Info("Use of CommonName certificates is deprecated. Use a SAN certificate instead.")
						return nil
					}
					return fmt.Errorf("certificate is not signed for %q hostname", peerName)
				},
			}
			if peerName != "" {
				config.ClientAuth = tls.RequireAndVerifyClientCert
			}
			return config, nil
		},
	}
}

// ClientTLS prepares the TLS configuration that can be used by a client while connecting to a server
// with given encoded certificate and private key.
// peerName must be provided when expecting the server to offer a certificate with that CommonName.
func ClientTLS(caCert, cert, key []byte, peerName string) (*tls.Config, error) {
	certPool := x509.NewCertPool()
	if ok := certPool.AppendCertsFromPEM(caCert); !ok {
		return nil, fmt.Errorf("failed to append CA certificate to pool")
	}

	certificate, err := tls.X509KeyPair(cert, key)
	if err != nil {
		return nil, err
	}

	return clientConfig(certPool, &certificate, peerName), nil
}

// LoadClientTLS prepares the TLS configuration that can be used by a client while connecting to a server.
// peerName must be provided when expecting the server to offer a certificate with that CommonName. caFile, certFile, and keyFile are all optional.
func LoadClientTLS(caFile, certFile, keyFile, peerName string) (*tls.Config, error) {
	certPool, peerCert, err := loadCertificate(caFile, certFile, keyFile)
	if err != nil {
		return nil, err
	}

	return clientConfig(certPool, peerCert, peerName), nil
}

func clientConfig(certPool *x509.CertPool, peerCert *tls.Certificate, peerName string) *tls.Config {
	tlsConfig := &tls.Config{
		MinVersion:    tls.VersionTLS12,
		Renegotiation: tls.RenegotiateNever,
		ServerName:    peerName,
		RootCAs:       certPool,
	}
	if peerCert != nil {
		tlsConfig.Certificates = append(tlsConfig.Certificates, *peerCert)
	}
	return tlsConfig
}

func loadCertificate(caFile, certFile, keyFile string) (certPool *x509.CertPool, peerCert *tls.Certificate, err error) {
	if certFile != "" || keyFile != "" {
		cert, err := tls.LoadX509KeyPair(certFile, keyFile)
		if err != nil {
			return nil, nil, err
		}
		peerCert = &cert
	}

	if caFile != "" {
		caCert, err := ioutil.ReadFile(caFile)
		if err != nil {
			return nil, nil, err
		}

		certPool = x509.NewCertPool()
		if ok := certPool.AppendCertsFromPEM(caCert); !ok {
			return nil, nil, fmt.Errorf("failed to append certs from %s", caFile)
		}
	}

	return
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
