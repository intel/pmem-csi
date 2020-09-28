/*
Copyright 2017 The Kubernetes Authors.
Copyright 2018 Intel Corporation.

SPDX-License-Identifier: Apache-2.0
*/

package pmemcsidriver

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	api "github.com/intel/pmem-csi/pkg/apis/pmemcsi/v1alpha1"
	grpcserver "github.com/intel/pmem-csi/pkg/grpc-server"
	pmdmanager "github.com/intel/pmem-csi/pkg/pmem-device-manager"
	pmemgrpc "github.com/intel/pmem-csi/pkg/pmem-grpc"
	registry "github.com/intel/pmem-csi/pkg/pmem-registry"
	pmemstate "github.com/intel/pmem-csi/pkg/pmem-state"
	"github.com/intel/pmem-csi/pkg/registryserver"
	"github.com/intel/pmem-csi/pkg/scheduler"
	"github.com/kubernetes-csi/csi-lib-utils/metrics"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/connectivity"
	"google.golang.org/grpc/status"
	"k8s.io/client-go/informers"
	"k8s.io/client-go/kubernetes"
	"k8s.io/klog/v2"
)

const (
	connectionTimeout time.Duration = 10 * time.Second
	retryTimeout      time.Duration = 10 * time.Second
	requestTimeout    time.Duration = 10 * time.Second
)

type DriverMode string

func (mode *DriverMode) Set(value string) error {
	switch value {
	case string(Controller), string(Node):
		*mode = DriverMode(value)
	default:
		// The flag package will add the value to the final output, no need to do it here.
		return errors.New("invalid driver mode")
	}
	return nil
}

func (mode *DriverMode) String() string {
	return string(*mode)
}

// The mode strings are part of the metrics API (-> csi_controller,
// csi_node as subsystem), do not change them!
const (
	//Controller definition for controller driver mode
	Controller DriverMode = "controller"
	//Node definition for noder driver mode
	Node DriverMode = "node"
)

var (
	//PmemDriverTopologyKey key to use for topology constraint
	DriverTopologyKey = ""

	// Mirrored after https://github.com/kubernetes/component-base/blob/dae26a37dccb958eac96bc9dedcecf0eb0690f0f/metrics/version.go#L21-L37
	// just with less information.
	buildInfo = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "build_info",
			Help: "A metric with a constant '1' value labeled by version.",
		},
		[]string{"version"},
	)

	pmemMaxDesc = prometheus.NewDesc(
		"pmem_amount_max_volume_size",
		"The size of the largest PMEM volume that can be created.",
		nil, nil,
	)
	pmemAvailableDesc = prometheus.NewDesc(
		"pmem_amount_available",
		"Remaining amount of PMEM on the host that can be used for new volumes.",
		nil, nil,
	)
	pmemManagedDesc = prometheus.NewDesc(
		"pmem_amount_managed",
		"Amount of PMEM on the host that is managed by PMEM-CSI.",
		nil, nil,
	)
	pmemTotalDesc = prometheus.NewDesc(
		"pmem_amount_total",
		"Total amount of PMEM on the host.",
		nil, nil,
	)
)

func init() {
	prometheus.MustRegister(buildInfo)
}

//Config type for driver configuration
type Config struct {
	//DriverName name of the csi driver
	DriverName string
	//NodeID node id on which this csi driver is running
	NodeID string
	//Endpoint exported csi driver endpoint
	Endpoint string
	//TestEndpoint adds the controller service to the server listening on Endpoint.
	//Only needed for testing.
	TestEndpoint bool
	//Mode mode fo the driver
	Mode DriverMode
	//RegistryEndpoint exported registry server endpoint
	RegistryEndpoint string
	//CAFile Root certificate authority certificate file
	CAFile string
	//CertFile certificate for server authentication
	CertFile string
	//KeyFile server private key file
	KeyFile string
	//ClientCertFile certificate for client side authentication
	ClientCertFile string
	//ClientKeyFile client private key
	ClientKeyFile string
	//ControllerEndpoint exported node controller endpoint
	ControllerEndpoint string
	//DeviceManager device manager to use
	DeviceManager api.DeviceMode
	//Directory where to persist the node driver state
	StateBasePath string
	//Version driver release version
	Version string
	// PmemPercentage percentage of space to be used by the driver in each PMEM region
	PmemPercentage uint

	// parameters for Kubernetes scheduler extender
	schedulerListen string
	client          kubernetes.Interface

	// parameters for Prometheus metrics
	metricsListen string
	metricsPath   string
}

type csiDriver struct {
	cfg             Config
	serverTLSConfig *tls.Config
	clientTLSConfig *tls.Config
	gatherers       prometheus.Gatherers
}

// deviceManagerCollector is a wrapper around a PMEM device manager which
// takes GetCapacity values and turns them into metrics data.
type deviceManagerCollector struct {
	pmdmanager.PmemDeviceManager
}

// Describe implements prometheus.Collector.Describe.
func (dm deviceManagerCollector) Describe(ch chan<- *prometheus.Desc) {
	prometheus.DescribeByCollect(dm, ch)
}

// Collect implements prometheus.Collector.Collect.
func (dm deviceManagerCollector) Collect(ch chan<- prometheus.Metric) {
	capacity, err := dm.GetCapacity()
	if err != nil {
		return
	}
	ch <- prometheus.MustNewConstMetric(
		pmemMaxDesc,
		prometheus.GaugeValue,
		float64(capacity.MaxVolumeSize),
	)
	ch <- prometheus.MustNewConstMetric(
		pmemAvailableDesc,
		prometheus.GaugeValue,
		float64(capacity.Available),
	)
	ch <- prometheus.MustNewConstMetric(
		pmemManagedDesc,
		prometheus.GaugeValue,
		float64(capacity.Managed),
	)
	ch <- prometheus.MustNewConstMetric(
		pmemTotalDesc,
		prometheus.GaugeValue,
		float64(capacity.Total),
	)
}

var _ prometheus.Collector = deviceManagerCollector{}

func GetCSIDriver(cfg Config) (*csiDriver, error) {
	validModes := map[DriverMode]struct{}{
		Controller: struct{}{},
		Node:       struct{}{},
	}
	var serverConfig *tls.Config
	var clientConfig *tls.Config
	var err error

	if _, ok := validModes[cfg.Mode]; !ok {
		return nil, fmt.Errorf("Invalid driver mode: %s", string(cfg.Mode))
	}
	if cfg.DriverName == "" {
		return nil, errors.New("driver name configuration option missing")
	}
	if cfg.Endpoint == "" {
		return nil, errors.New("CSI endpoint configuration option missing")
	}
	if cfg.Mode == Node && cfg.NodeID == "" {
		return nil, errors.New("node ID configuration option missing")
	}
	if cfg.Mode == Controller && cfg.RegistryEndpoint == "" {
		return nil, errors.New("registry endpoint configuration option missing")
	}
	if cfg.Mode == Node && cfg.ControllerEndpoint == "" {
		return nil, errors.New("internal controller endpoint configuration option missing")
	}
	if cfg.Mode == Node && cfg.StateBasePath == "" {
		cfg.StateBasePath = "/var/lib/" + cfg.DriverName
	}
	if cfg.Endpoint == cfg.RegistryEndpoint {
		return nil, fmt.Errorf("CSI and registry endpoints must be different, both are: %q", cfg.Endpoint)
	}
	if cfg.Endpoint == cfg.ControllerEndpoint {
		return nil, fmt.Errorf("CSI and internal control endpoints must be different, both are: %q", cfg.Endpoint)
	}

	peerName := "pmem-registry"
	if cfg.Mode == Controller {
		//When driver running in Controller mode, we connect to node controllers
		//so use appropriate peer name
		peerName = "pmem-node-controller"
	}

	if cfg.CertFile != "" && cfg.KeyFile != "" {
		serverConfig, err = pmemgrpc.LoadServerTLS(cfg.CAFile, cfg.CertFile, cfg.KeyFile, peerName)
		if err != nil {
			return nil, err
		}
	}

	/* if no client certificate details provided use same server certificate to connect to peer server */
	if cfg.ClientCertFile == "" {
		cfg.ClientCertFile = cfg.CertFile
		cfg.ClientKeyFile = cfg.KeyFile
	}

	if cfg.ClientCertFile != "" && cfg.ClientKeyFile != "" {
		clientConfig, err = pmemgrpc.LoadClientTLS(cfg.CAFile, cfg.ClientCertFile, cfg.ClientKeyFile, peerName)
		if err != nil {
			return nil, err
		}
	}

	DriverTopologyKey = cfg.DriverName + "/node"

	// Should GetCSIDriver get called more than once per process,
	// all of them will record their version.
	buildInfo.With(prometheus.Labels{"version": cfg.Version}).Set(1)

	return &csiDriver{
		cfg:             cfg,
		serverTLSConfig: serverConfig,
		clientTLSConfig: clientConfig,
		// We use the default Prometheus registry here in addition to
		// any custom CSIMetricsManager.  Therefore we also return all
		// data that is registered globally, including (but not
		// limited to!) our own metrics data. For example, some Go
		// runtime information
		// (https://povilasv.me/prometheus-go-metrics/) are included,
		// which may be useful.
		gatherers: prometheus.Gatherers{prometheus.DefaultGatherer},
	}, nil
}

func (csid *csiDriver) Run() error {
	// Create GRPC servers
	ids, err := NewIdentityServer(csid.cfg.DriverName, csid.cfg.Version)
	if err != nil {
		return err
	}

	s := grpcserver.NewNonBlockingGRPCServer()
	// Ensure that the server is stopped before we return.
	defer func() {
		s.ForceStop()
		s.Wait()
	}()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// On the csi.sock endpoint we gather statistics for incoming
	// CSI method calls like any other CSI driver.
	cmm := metrics.NewCSIMetricsManagerForPlugin(csid.cfg.DriverName)
	csid.gatherers = append(csid.gatherers, cmm.GetRegistry())

	switch csid.cfg.Mode {
	case Controller:
		rs := registryserver.New(csid.clientTLSConfig, csid.cfg.DriverName)
		csid.gatherers = append(csid.gatherers, rs.GetMetricsGatherer())
		cs := NewMasterControllerServer(rs)

		if err := s.Start(csid.cfg.Endpoint, nil, cmm, ids, cs); err != nil {
			return err
		}
		if err := s.Start(csid.cfg.RegistryEndpoint, csid.serverTLSConfig, nil /* no metrics gathering for registry at the moment */, rs); err != nil {
			return err
		}

		// Also run scheduler extender?
		if _, err := csid.startScheduler(ctx, cancel, rs); err != nil {
			return err
		}
	case Node:
		dm, err := newDeviceManager(csid.cfg.DeviceManager, csid.cfg.PmemPercentage)
		if err != nil {
			return err
		}
		sm, err := pmemstate.NewFileState(csid.cfg.StateBasePath)
		if err != nil {
			return err
		}
		cs := NewNodeControllerServer(csid.cfg.NodeID, dm, sm)
		ns := NewNodeServer(cs, filepath.Clean(csid.cfg.StateBasePath)+"/mount")

		// Internal CSI calls are tracked on the server side
		// with a custom "pmem_csi_node" subsystem. The
		// corresponding client calls use "pmem_csi_controller" with
		// a tag that identifies the node that is being called.
		cmmInternal := metrics.NewCSIMetricsManagerWithOptions(csid.cfg.DriverName,
			metrics.WithSubsystem("pmem_csi_node"),
			// Always add the instance label to allow correlating with
			// the controller calls.
			metrics.WithLabels(map[string]string{registryserver.NodeLabel: csid.cfg.NodeID}),
		)
		csid.gatherers = append(csid.gatherers, cmmInternal.GetRegistry())
		if err := s.Start(csid.cfg.ControllerEndpoint, csid.serverTLSConfig, cmmInternal, cs); err != nil {
			return err
		}
		if err := csid.registerNodeController(); err != nil {
			return err
		}
		services := []grpcserver.Service{ids, ns}
		if csid.cfg.TestEndpoint {
			services = append(services, cs)
		}
		if err := s.Start(csid.cfg.Endpoint, nil, cmm, services...); err != nil {
			return err
		}

		// Also collect metrics data via the device manager.
		prometheus.WrapRegistererWith(prometheus.Labels{registryserver.NodeLabel: csid.cfg.NodeID}, prometheus.DefaultRegisterer).MustRegister(
			deviceManagerCollector{dm},
		)
	default:
		return fmt.Errorf("Unsupported device mode '%v", csid.cfg.Mode)
	}

	// And metrics server?
	if csid.cfg.metricsListen != "" {
		addr, err := csid.startMetrics(ctx, cancel)
		if err != nil {
			return err
		}
		klog.V(2).Infof("Prometheus endpoint started at http://%s%s", addr, csid.cfg.metricsPath)
	}

	c := make(chan os.Signal, 1)
	signal.Notify(c, os.Interrupt, syscall.SIGTERM)
	select {
	case sig := <-c:
		// Here we want to shut down cleanly, i.e. let running
		// gRPC calls complete.
		klog.V(3).Infof("Caught signal %s, terminating.", sig)
	case <-ctx.Done():
		// The scheduler HTTP server must have failed (to start).
		// We quit in that case.
	}
	s.Stop()
	s.Wait()

	return nil
}

func (csid *csiDriver) registerNodeController() error {
	var err error
	var conn *grpc.ClientConn

	for {
		klog.V(3).Infof("Connecting to registry server at: %s\n", csid.cfg.RegistryEndpoint)
		conn, err = pmemgrpc.Connect(csid.cfg.RegistryEndpoint, csid.clientTLSConfig)
		if err == nil {
			break
		}
		klog.Warningf("Failed to connect registry server: %s, retrying after %v seconds...", err.Error(), retryTimeout.Seconds())
		time.Sleep(retryTimeout)
	}

	req := &registry.RegisterControllerRequest{
		NodeId:   csid.cfg.NodeID,
		Endpoint: csid.cfg.ControllerEndpoint,
	}

	if err := register(context.Background(), conn, req); err != nil {
		return err
	}
	go waitAndWatchConnection(conn, req)

	return nil
}

// startScheduler starts the scheduler extender if it is enabled. It
// logs errors and cancels the context when it runs into a problem,
// either during the startup phase (blocking) or later at runtime (in
// a go routine).
func (csid *csiDriver) startScheduler(ctx context.Context, cancel func(), rs *registryserver.RegistryServer) (string, error) {
	if csid.cfg.schedulerListen == "" {
		return "", nil
	}

	resyncPeriod := 1 * time.Hour
	factory := informers.NewSharedInformerFactory(csid.cfg.client, resyncPeriod)
	pvcLister := factory.Core().V1().PersistentVolumeClaims().Lister()
	scLister := factory.Storage().V1().StorageClasses().Lister()
	sched, err := scheduler.NewScheduler(
		csid.cfg.DriverName,
		scheduler.CapacityViaRegistry(rs),
		csid.cfg.client,
		pvcLister,
		scLister,
	)
	if err != nil {
		return "", fmt.Errorf("create scheduler: %v", err)
	}
	factory.Start(ctx.Done())
	cacheSyncResult := factory.WaitForCacheSync(ctx.Done())
	klog.V(5).Infof("synchronized caches: %+v", cacheSyncResult)
	for t, v := range cacheSyncResult {
		if !v {
			return "", fmt.Errorf("failed to sync informer for type %v", t)
		}
	}
	return csid.startHTTPSServer(ctx, cancel, csid.cfg.schedulerListen, sched, true /* TLS */)
}

// startMetrics starts the HTTPS server for the Prometheus endpoint, if one is configured.
// Error handling is the same as for startScheduler.
func (csid *csiDriver) startMetrics(ctx context.Context, cancel func()) (string, error) {
	mux := http.NewServeMux()
	mux.Handle(csid.cfg.metricsPath,
		promhttp.InstrumentMetricHandler(
			prometheus.DefaultRegisterer,
			promhttp.HandlerFor(csid.gatherers, promhttp.HandlerOpts{}),
		),
	)
	return csid.startHTTPSServer(ctx, cancel, csid.cfg.metricsListen, mux, false /* no TLS */)
}

// startHTTPSServer contains the common logic for starting and
// stopping an HTTPS server.  Returns an error or the address that can
// be used in Dial("tcp") to reach the server (useful for testing when
// "listen" does not include a port).
func (csid *csiDriver) startHTTPSServer(ctx context.Context, cancel func(), listen string, handler http.Handler, useTLS bool) (string, error) {
	var config *tls.Config
	if useTLS {
		c, err := pmemgrpc.LoadServerTLS(csid.cfg.CAFile, csid.cfg.CertFile, csid.cfg.KeyFile, "")
		if err != nil {
			return "", fmt.Errorf("initialize HTTPS config: %v", err)
		}
		config = c
	}
	server := http.Server{
		Addr: listen,
		Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			klog.V(5).Infof("HTTP request: %s %q from %s %s", r.Method, r.URL.Path, r.RemoteAddr, r.UserAgent())
			handler.ServeHTTP(w, r)
		}),
		TLSConfig: config,
	}
	listener, err := net.Listen("tcp", listen)
	if err != nil {
		return "", fmt.Errorf("listen on TCP address %q: %v", listen, err)
	}
	tcpListener := listener.(*net.TCPListener)
	go func() {
		defer tcpListener.Close()

		var err error
		if useTLS {
			err = server.ServeTLS(listener, csid.cfg.CertFile, csid.cfg.KeyFile)
		} else {
			err = server.Serve(listener)
		}
		if err != http.ErrServerClosed {
			klog.Errorf("%s HTTP(S) server error: %v", listen, err)
		}
		// Also stop main thread.
		cancel()
	}()
	go func() {
		// Block until the context is done, then immediately
		// close the server.
		<-ctx.Done()
		server.Close()
	}()

	return tcpListener.Addr().String(), nil
}

// waitAndWatchConnection Keeps watching for connection changes, and whenever the
// connection state changed from lost to ready, it re-register the node controller with registry server.
func waitAndWatchConnection(conn *grpc.ClientConn, req *registry.RegisterControllerRequest) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	connectionLost := false

	for {
		s := conn.GetState()
		if s == connectivity.Ready {
			if connectionLost {
				klog.V(4).Info("ReConnected.")
				if err := register(ctx, conn, req); err != nil {
					klog.Warning(err)
				}
			}
		} else {
			connectionLost = true
			klog.V(4).Info("Connection state: ", s)
		}
		conn.WaitForStateChange(ctx, s)
	}
}

// register Tries to register with RegistryServer in endless loop till,
// either the registration succeeds or RegisterController() returns only possible InvalidArgument error.
func register(ctx context.Context, conn *grpc.ClientConn, req *registry.RegisterControllerRequest) error {
	client := registry.NewRegistryClient(conn)
	for {
		klog.V(3).Info("Registering controller...")
		if _, err := client.RegisterController(ctx, req); err != nil {
			if s, ok := status.FromError(err); ok && s.Code() == codes.InvalidArgument {
				return fmt.Errorf("Registration failed: %s", s.Message())
			}
			klog.Warningf("Failed to register: %s, retrying after %v seconds...", err.Error(), retryTimeout.Seconds())
			time.Sleep(retryTimeout)
		} else {
			break
		}
	}
	klog.V(4).Info("Registration success")

	return nil
}

func newDeviceManager(dmType api.DeviceMode, pmemPercentage uint) (pmdmanager.PmemDeviceManager, error) {
	switch dmType {
	case api.DeviceModeLVM:
		return pmdmanager.NewPmemDeviceManagerLVM(pmemPercentage)
	case api.DeviceModeDirect:
		return pmdmanager.NewPmemDeviceManagerNdctl(pmemPercentage)
	}
	return nil, fmt.Errorf("Unsupported device manager type '%s'", dmType)
}
