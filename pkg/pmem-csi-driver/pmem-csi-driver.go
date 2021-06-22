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

	api "github.com/intel/pmem-csi/pkg/apis/pmemcsi/v1beta1"
	grpcserver "github.com/intel/pmem-csi/pkg/grpc-server"
	"github.com/intel/pmem-csi/pkg/k8sutil"
	pmemlog "github.com/intel/pmem-csi/pkg/logger"
	pmdmanager "github.com/intel/pmem-csi/pkg/pmem-device-manager"
	pmemgrpc "github.com/intel/pmem-csi/pkg/pmem-grpc"
	pmemstate "github.com/intel/pmem-csi/pkg/pmem-state"
	"github.com/intel/pmem-csi/pkg/scheduler"
	"github.com/intel/pmem-csi/pkg/types"
	"github.com/kubernetes-csi/csi-lib-utils/metrics"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"k8s.io/client-go/informers"
)

const (
	connectionTimeout time.Duration = 10 * time.Second
	retryTimeout      time.Duration = 10 * time.Second
	requestTimeout    time.Duration = 10 * time.Second

	// Resyncing should never be needed for correct operation,
	// so this is so high that it shouldn't matter in practice.
	resyncPeriod = 10000 * time.Hour
)

type DriverMode string

func (mode *DriverMode) Set(value string) error {
	switch value {
	case string(Node), string(Webhooks), string(ForceConvertRawNamespaces):
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
	// Node driver with support for provisioning.
	Node DriverMode = "node"
	// Just the webhooks, using metrics instead of gRPC over TCP.
	Webhooks DriverMode = "webhooks"
	// Convert each raw namespace into fsdax.
	ForceConvertRawNamespaces = "force-convert-raw-namespaces"
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
	//Mode mode fo the driver
	Mode DriverMode
	//CAFile Root certificate authority certificate file
	CAFile string
	//CertFile certificate for server authentication
	CertFile string
	//KeyFile server private key file
	KeyFile string
	//DeviceManager device manager to use
	DeviceManager api.DeviceMode
	//Directory where to persist the node driver state
	StateBasePath string
	//Version driver release version
	Version string
	// PmemPercentage percentage of space to be used by the driver in each PMEM region
	PmemPercentage uint

	// KubeAPIQPS is the average rate of requests to the Kubernetes API server,
	// enforced locally in client-go.
	KubeAPIQPS float64

	// KubeAPIQPS is the number of requests that a client is
	// allowed to send above the average rate of request.
	KubeAPIBurst int

	// parameters for Kubernetes scheduler extender
	schedulerListen string

	// parameters for rescheduler and raw namespace conversion
	nodeSelector types.NodeSelector

	// parameters for Prometheus metrics
	metricsListen string
	metricsPath   string
}

type csiDriver struct {
	cfg       Config
	gatherers prometheus.Gatherers
}

func GetCSIDriver(cfg Config) (*csiDriver, error) {
	if cfg.DriverName == "" {
		return nil, errors.New("driver name configuration option missing")
	}
	if cfg.Endpoint == "" {
		return nil, errors.New("CSI endpoint configuration option missing")
	}
	if cfg.Mode == Node && cfg.NodeID == "" {
		return nil, errors.New("node ID configuration option missing")
	}
	if cfg.Mode == Node && cfg.StateBasePath == "" {
		cfg.StateBasePath = "/var/lib/" + cfg.DriverName
	}

	DriverTopologyKey = cfg.DriverName + "/node"

	// Should GetCSIDriver get called more than once per process,
	// all of them will record their version.
	buildInfo.With(prometheus.Labels{"version": cfg.Version}).Set(1)

	return &csiDriver{
		cfg: cfg,
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

func (csid *csiDriver) Run(ctx context.Context) error {
	s := grpcserver.NewNonBlockingGRPCServer()
	// Ensure that the server is stopped before we return.
	defer func() {
		s.ForceStop()
		s.Wait()
	}()
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	logger := pmemlog.Get(ctx)

	switch csid.cfg.Mode {
	case Webhooks:
		client, err := k8sutil.NewClient(config.KubeAPIQPS, config.KubeAPIBurst)
		if err != nil {
			return fmt.Errorf("connect to apiserver: %v", err)
		}

		// A factory for all namespaces. Some of these are only needed by
		// scheduler webhooks or deprovisioner, but because the normal
		// setup is to have both enabled, the logic here is simplified so that
		// everything gets initialized.
		//
		// The PV informer is not really needed, but there is no good way to
		// tell the lib that it should watch PVs. An informer for a fake client
		// did not work:
		// Failed to watch *v1.PersistentVolume: unhandled watch: testing.WatchActionImpl
		globalFactory := informers.NewSharedInformerFactory(client, resyncPeriod)
		pvcInformer := globalFactory.Core().V1().PersistentVolumeClaims().Informer()
		pvcLister := globalFactory.Core().V1().PersistentVolumeClaims().Lister()
		scLister := globalFactory.Storage().V1().StorageClasses().Lister()
		scInformer := globalFactory.Storage().V1().StorageClasses().Informer()
		pvInformer := globalFactory.Core().V1().PersistentVolumes().Informer()
		csiNodeLister := globalFactory.Storage().V1().CSINodes().Lister()

		var pcp *pmemCSIProvisioner
		if csid.cfg.nodeSelector != nil {
			serverVersion, err := client.Discovery().ServerVersion()
			if err != nil {
				return fmt.Errorf("discover server version: %v", err)
			}

			// Create rescheduler. This has to be done before starting the factory
			// because it will indirectly add a new index.
			pcp = newRescheduler(ctx,
				csid.cfg.DriverName,
				client, pvcInformer, scInformer, pvInformer, csiNodeLister,
				csid.cfg.nodeSelector,
				serverVersion.GitVersion)
		}

		// Now that all informers and indices are created we can run the factory.
		globalFactory.Start(ctx.Done())
		cacheSyncResult := globalFactory.WaitForCacheSync(ctx.Done())
		logger.V(5).Info("Synchronized caches", "cache-sync-result", cacheSyncResult)
		for t, v := range cacheSyncResult {
			if !v {
				return fmt.Errorf("failed to sync informer for type %v", t)
			}
		}

		if csid.cfg.schedulerListen != "" {
			// Factory for the driver's namespace.
			namespace := os.Getenv("POD_NAMESPACE")
			if namespace == "" {
				return errors.New("POD_NAMESPACE env variable is not set")
			}
			localFactory := informers.NewSharedInformerFactoryWithOptions(client, resyncPeriod,
				informers.WithNamespace(namespace),
			)
			podLister := localFactory.Core().V1().Pods().Lister()
			c := scheduler.CapacityViaMetrics(namespace, csid.cfg.DriverName, podLister)
			localFactory.Start(ctx.Done())

			sched, err := scheduler.NewScheduler(
				csid.cfg.DriverName,
				c,
				client,
				pvcLister,
				scLister,
			)
			if err != nil {
				return fmt.Errorf("create scheduler: %v", err)
			}
			if _, err := csid.startHTTPSServer(ctx, cancel, csid.cfg.schedulerListen, sched, true /* TLS */); err != nil {
				return err
			}
		}

		if pcp != nil {
			pcp.startRescheduler(ctx, cancel)
		}
	case Node:
		dm, err := pmdmanager.New(ctx, csid.cfg.DeviceManager, csid.cfg.PmemPercentage)
		if err != nil {
			return err
		}
		sm, err := pmemstate.NewFileState(csid.cfg.StateBasePath)
		if err != nil {
			return err
		}

		// On the csi.sock endpoint we gather statistics for incoming
		// CSI method calls like any other CSI driver.
		cmm := metrics.NewCSIMetricsManagerWithOptions(csid.cfg.DriverName,
			metrics.WithProcessStartTime(false),
			metrics.WithSubsystem(metrics.SubsystemPlugin),
		)
		csid.gatherers = append(csid.gatherers, cmm.GetRegistry())

		// Create GRPC servers
		ids := NewIdentityServer(csid.cfg.DriverName, csid.cfg.Version)
		cs := NewNodeControllerServer(ctx, csid.cfg.NodeID, dm, sm)
		ns := NewNodeServer(cs, filepath.Clean(csid.cfg.StateBasePath)+"/mount")

		services := []grpcserver.Service{ids, ns, cs}
		if err := s.Start(ctx, csid.cfg.Endpoint, csid.cfg.NodeID, nil, cmm, services...); err != nil {
			return err
		}

		// Also collect metrics data via the device manager.
		pmdmanager.CapacityCollector{PmemDeviceCapacity: dm}.MustRegister(prometheus.DefaultRegisterer, csid.cfg.NodeID, csid.cfg.DriverName)

		capacity, err := dm.GetCapacity(ctx)
		if err != nil {
			return fmt.Errorf("get initial capacity: %v", err)
		}
		logger.Info("PMEM-CSI ready.", "capacity", capacity)
	case ForceConvertRawNamespaces:
		client, err := k8sutil.NewClient(config.KubeAPIQPS, config.KubeAPIBurst)
		if err != nil {
			return fmt.Errorf("connect to apiserver: %v", err)
		}

		if err := pmdmanager.ForceConvertRawNamespaces(ctx, client, csid.cfg.DriverName, csid.cfg.nodeSelector, csid.cfg.NodeID); err != nil {
			return err
		}

		// By proceeding to waiting for the termination signal below
		// we keep the pod around after it has its work done until
		// Kubernetes notices that the pod is no longer needed.
		// Terminating the pod (even with a zero exit code) would
		// cause a race between detecting the label change and
		// restarting the container.
		//
		// "RestartPolicy: OnFailure" would solve that, but
		// isn't supported for DaemonSets
		// (https://github.com/kubernetes/kubernetes/issues/24725).
		logger.Info("Raw namespace conversion is done, waiting for termination signal.")
	default:
		return fmt.Errorf("Unsupported device mode '%v", csid.cfg.Mode)
	}

	// And metrics server?
	if csid.cfg.metricsListen != "" {
		addr, err := csid.startMetrics(ctx, cancel)
		if err != nil {
			return err
		}
		logger.Info("Prometheus endpoint started.", "endpoint", fmt.Sprintf("http://%s%s", addr, csid.cfg.metricsPath))
	}

	c := make(chan os.Signal, 1)
	signal.Notify(c, os.Interrupt, syscall.SIGTERM)
	select {
	case sig := <-c:
		logger.Info("Caught signal, terminating.", "signal", sig)
		// We sleep briefly to give sidecars a chance to shut down cleanly
		// before we close the CSI socket and force them to shut down
		// abnormally, because the latter causes lots of debug output
		// due to usage of klog.Fatal (https://github.com/intel/pmem-csi/issues/856).
		time.Sleep(time.Second)
	case <-ctx.Done():
		// The scheduler HTTP server must have failed (to start).
		// We quit directly in that case.
	}

	// Here (in contrast to the s.ForceStop() above) we let the gRPC server finish
	// its work on any pending call.
	s.Stop()
	s.Wait()

	return nil
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
	name := "HTTP server"
	if useTLS {
		name = "HTTPS server"
	}
	logger := pmemlog.Get(ctx).WithName(name).WithValues("listen", listen)
	var config *tls.Config
	if useTLS {
		c, err := pmemgrpc.LoadServerTLS(ctx, csid.cfg.CAFile, csid.cfg.CertFile, csid.cfg.KeyFile, "")
		if err != nil {
			return "", fmt.Errorf("initialize HTTPS config: %v", err)
		}
		config = c
	}
	server := http.Server{
		Addr: listen,
		Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			logger.V(5).Info("Handling request", "method", r.Method, "path", r.URL.Path, "peer", r.RemoteAddr, "agent", r.UserAgent())
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
			logger.Error(err, "Failed")
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

	logger.V(3).Info("Started", "addr", tcpListener.Addr())
	return tcpListener.Addr().String(), nil
}
