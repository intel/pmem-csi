package registryserver_test

import (
	"crypto/tls"
	"fmt"
	"io/ioutil"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/intel/pmem-csi/pkg/pmem-csi-driver"
	pmemgrpc "github.com/intel/pmem-csi/pkg/pmem-grpc"
	registry "github.com/intel/pmem-csi/pkg/pmem-registry"
	"github.com/intel/pmem-csi/pkg/registryserver"
	"golang.org/x/net/context"
	"google.golang.org/grpc"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
)

func TestPmemRegistry(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Registry Suite")
}

var tmpDir string

var _ = BeforeSuite(func() {
	var err error
	tmpDir, err = ioutil.TempDir("", "pmem-test-")
	Expect(err).NotTo(HaveOccurred())
})

var _ = AfterSuite(func() {
	os.RemoveAll(tmpDir)
})

var _ = Describe("pmem registry", func() {

	registryServerSocketFile := filepath.Join(tmpDir, "pmem-registry.sock")
	registryServerEndpoint := "unix://" + registryServerSocketFile

	var (
		tlsConfig          *tls.Config
		nbServer           *pmemcsidriver.NonBlockingGRPCServer
		registryClientConn *grpc.ClientConn
		registryClient     registry.RegistryClient
		registryServer     *registryserver.RegistryServer
	)

	BeforeEach(func() {
		var err error

		registryServer = registryserver.New(nil)

		caFile := os.ExpandEnv("${TEST_WORK}/pmem-ca/ca.pem")
		certFile := os.ExpandEnv("${TEST_WORK}/pmem-ca/pmem-registry.pem")
		keyFile := os.ExpandEnv("${TEST_WORK}/pmem-ca/pmem-registry-key.pem")
		tlsConfig, err = pmemgrpc.LoadServerTLS(caFile, certFile, keyFile, "pmem-node-controller")
		Expect(err).NotTo(HaveOccurred())

		nbServer = pmemcsidriver.NewNonBlockingGRPCServer()
		err = nbServer.Start(registryServerEndpoint, tlsConfig, registryServer)
		Expect(err).NotTo(HaveOccurred())
		_, err = os.Stat(registryServerSocketFile)
		Expect(err).NotTo(HaveOccurred())

		// set up node controller client
		nodeCertFile := os.ExpandEnv("${TEST_WORK}/pmem-ca/pmem-node-controller.pem")
		nodeCertKey := os.ExpandEnv("${TEST_WORK}/pmem-ca/pmem-node-controller-key.pem")
		tlsConfig, err = pmemgrpc.LoadClientTLS(caFile, nodeCertFile, nodeCertKey, "pmem-registry")
		Expect(err).NotTo(HaveOccurred())

		registryClientConn, err = pmemgrpc.Connect(registryServerEndpoint, tlsConfig)
		Expect(err).NotTo(HaveOccurred())
		registryClient = registry.NewRegistryClient(registryClientConn)
	})

	AfterEach(func() {
		if registryServer != nil {
			nbServer.ForceStop()
			nbServer.Wait()
		}
		os.Remove(registryServerSocketFile)
		if registryClientConn != nil {
			registryClientConn.Close()
		}
	})

	Context("Registry API", func() {
		controllerServerSocketFile := filepath.Join(tmpDir, "pmem-controller.sock")
		controllerServerEndpoint := "unix://" + controllerServerSocketFile
		var (
			nodeId      = "pmem-test"
			registerReq = registry.RegisterControllerRequest{
				NodeId:   nodeId,
				Endpoint: controllerServerEndpoint,
			}

			unregisterReq = registry.UnregisterControllerRequest{
				NodeId: nodeId,
			}
		)

		It("Register node controller", func() {
			Expect(registryClient).ShouldNot(BeNil())

			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			_, err := registryClient.RegisterController(ctx, &registerReq)
			Expect(err).NotTo(HaveOccurred())

			_, err = registryServer.GetNodeController(nodeId)
			Expect(err).NotTo(HaveOccurred())
		})

		It("Registration should fail", func() {
			Expect(registryClient).ShouldNot(BeNil())

			l := listener{}

			registryServer.AddListener(l)

			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			_, err := registryClient.RegisterController(ctx, &registerReq)
			Expect(err).To(HaveOccurred())

			_, err = registryServer.GetNodeController(nodeId)
			Expect(err).To(HaveOccurred())
		})

		It("Unregister node controller", func() {
			Expect(registryClient).ShouldNot(BeNil())

			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			_, err := registryClient.RegisterController(ctx, &registerReq)
			Expect(err).NotTo(HaveOccurred())

			ctx, cancel = context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			_, err = registryClient.UnregisterController(ctx, &unregisterReq)
			Expect(err).NotTo(HaveOccurred())

			_, err = registryServer.GetNodeController(nodeId)
			Expect(err).To(HaveOccurred())
		})

		It("Unregister non existing node controller", func() {
			Expect(registryClient).ShouldNot(BeNil())

			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			_, err := registryClient.UnregisterController(ctx, &unregisterReq)
			Expect(err).NotTo(HaveOccurred())
		})
	})

	Context("Registry Security", func() {
		var (
			evilEndpoint = "unix:///tmp/pmem-evil.sock"
			ca           = os.ExpandEnv("${TEST_WORK}/pmem-ca/ca.pem")
			cert         = os.ExpandEnv("${TEST_WORK}/pmem-ca/pmem-node-controller.pem")
			key          = os.ExpandEnv("${TEST_WORK}/pmem-ca/pmem-node-controller-key.pem")
			wrongCert    = os.ExpandEnv("${TEST_WORK}/pmem-ca/wrong-node-controller.pem")
			wrongKey     = os.ExpandEnv("${TEST_WORK}/pmem-ca/wrong-node-controller-key.pem")

			evilCA   = os.ExpandEnv("${TEST_WORK}/evil-ca/ca.pem")
			evilCert = os.ExpandEnv("${TEST_WORK}/evil-ca/pmem-node-controller.pem")
			evilKey  = os.ExpandEnv("${TEST_WORK}/evil-ca/pmem-node-controller-key.pem")
		)

		// This covers different scenarios for connections to the registry.
		cases := []struct {
			name, ca, cert, key, peerName, errorText string
		}{
			{"registry should detect man-in-the-middle", ca, evilCert, evilKey, "pmem-registry", "authentication handshake failed: remote error: tls: bad certificate"},
			{"client should detect man-in-the-middle", evilCA, evilCert, evilKey, "pmem-registry", "transport: authentication handshake failed: x509: certificate signed by unknown authority"},
			{"client should detect wrong peer", ca, cert, key, "unknown-registry", "transport: authentication handshake failed: x509: certificate is valid for pmem-registry, not unknown-registry"},
			{"server should detect wrong peer", ca, wrongCert, wrongKey, "pmem-registry", "transport: authentication handshake failed: remote error: tls: bad certificate"},
		}

		for _, c := range cases {
			c := c
			It(c.name, func() {
				tlsConfig, err := pmemgrpc.LoadClientTLS(c.ca, c.cert, c.key, c.peerName)
				Expect(err).NotTo(HaveOccurred())
				clientConn, err := pmemgrpc.Connect(registryServerEndpoint, tlsConfig)
				Expect(err).NotTo(HaveOccurred())
				client := registry.NewRegistryClient(clientConn)

				req := registry.RegisterControllerRequest{
					NodeId:   "pmem-evil",
					Endpoint: evilEndpoint,
				}

				_, err = client.RegisterController(context.Background(), &req)
				Expect(err).To(HaveOccurred())
				Expect(err.Error()).To(ContainSubstring(c.errorText))
			})
		}
	})

})

type listener struct{}

func (l listener) OnNodeAdded(ctx context.Context, node *registryserver.NodeInfo) error {
	return fmt.Errorf("failed")
}

func (l listener) OnNodeDeleted(ctx context.Context, node *registryserver.NodeInfo) {
}
