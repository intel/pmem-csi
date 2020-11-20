package registryserver

import (
	"crypto/tls"
	"fmt"
	"sync"
	"time"

	pmemgrpc "github.com/intel/pmem-csi/pkg/pmem-grpc"
	registry "github.com/intel/pmem-csi/pkg/pmem-registry"
	"github.com/kubernetes-csi/csi-lib-utils/metrics"
	"github.com/prometheus/client_golang/prometheus"
	"golang.org/x/net/context"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"k8s.io/klog/v2"
	"k8s.io/utils/keymutex"
)

// NodeLabel is a label used for Prometheus which identifies the
// node that the controller talks to.
const NodeLabel = "node"

// RegistryListener is an interface for registry server change listeners
// All the callbacks are called once after updating the in-memory
// registry data.
type RegistryListener interface {
	// OnNodeAdded is called by RegistryServer whenever a new node controller is registered
	// or node controller updated its endpoint.
	// In case of error, the node registration would fail and removed from registry.
	OnNodeAdded(ctx context.Context, node *NodeInfo) error
	// OnNodeDeleted is called by RegistryServer whenever a node controller unregistered.
	// Callback implementations has to note that by the time this method is called,
	// the NodeInfo for that node have already removed from in-memory registry.
	OnNodeDeleted(ctx context.Context, node *NodeInfo)
}

type RegistryServer struct {
	// mutex is used to protect concurrent access of RegistryServer's
	// data(nodeClients)
	mutex sync.Mutex
	// rpcMutex is used to avoid concurrent RPC(RegisterController, UnregisterController)
	// requests from the same node
	rpcMutex        keymutex.KeyMutex
	clientTLSConfig *tls.Config
	nodeClients     map[string]*NodeInfo
	listeners       map[RegistryListener]struct{}

	// All nodes share the same metrics manager, but log samples
	// with a different "pmem_csi_node" label and thus get their
	// own histogram.
	cmm metrics.CSIMetricsManager
}

type NodeInfo struct {
	//NodeID controller node id
	NodeID string
	//Endpoint node controller endpoint
	Endpoint string
}

var (
	pmemNodes = prometheus.NewGauge(
		prometheus.GaugeOpts{
			Name: "pmem_nodes",
			Help: "The number of PMEM-CSI nodes registered in the controller.",
		},
	)
)

func init() {
	prometheus.MustRegister(pmemNodes)
}

func New(tlsConfig *tls.Config, driverName string) *RegistryServer {
	return &RegistryServer{
		rpcMutex:        keymutex.NewHashed(-1),
		clientTLSConfig: tlsConfig,
		nodeClients:     map[string]*NodeInfo{},
		listeners:       map[RegistryListener]struct{}{},
		cmm: metrics.NewCSIMetricsManagerWithOptions(driverName,
			metrics.WithProcessStartTime(false),
			metrics.WithSubsystem("pmem_csi_controller"),
			metrics.WithLabelNames(NodeLabel),
		),
	}
}

func (rs *RegistryServer) GetMetricsGatherer() prometheus.Gatherer {
	return rs.cmm.GetRegistry()
}

func (rs *RegistryServer) RegisterService(rpcServer *grpc.Server) {
	registry.RegisterRegistryServer(rpcServer, rs)
}

//GetNodeController returns the node controller info for given nodeID, error if not found
func (rs *RegistryServer) GetNodeController(nodeID string) (NodeInfo, error) {
	rs.mutex.Lock()
	defer rs.mutex.Unlock()

	if node, ok := rs.nodeClients[nodeID]; ok {
		return *node, nil
	}

	return NodeInfo{}, fmt.Errorf("No node registered with id: %v", nodeID)
}

// ConnectToNodeController initiates a connection to controller running at nodeId
func (rs *RegistryServer) ConnectToNodeController(nodeId string) (*grpc.ClientConn, error) {
	nodeInfo, err := rs.GetNodeController(nodeId)
	if err != nil {
		return nil, err
	}

	klog.V(3).Infof("Connecting to node controller: %s", nodeInfo.Endpoint)

	return pmemgrpc.Connect(nodeInfo.Endpoint, rs.clientTLSConfig,
		grpc.WithUnaryInterceptor(func(
			ctx context.Context,
			method string,
			req, reply interface{},
			cc *grpc.ClientConn,
			invoker grpc.UnaryInvoker,
			opts ...grpc.CallOption) error {
			start := time.Now()
			err := invoker(ctx, method, req, reply, cc, opts...)
			duration := time.Since(start)
			cmmv, err2 := rs.cmm.WithLabelValues(
				map[string]string{NodeLabel: nodeId},
			)
			if err2 != nil {
				klog.Errorf("CSI call metrics: set label %s value: %v", NodeLabel, err2)
			} else {
				cmmv.RecordMetrics(
					method,   /* operationName */
					err,      /* operationErr */
					duration, /* operationDuration */
				)
			}
			return err
		}),
	)
}

func (rs *RegistryServer) AddListener(l RegistryListener) {
	rs.listeners[l] = struct{}{}
}

func (rs *RegistryServer) RegisterController(ctx context.Context, req *registry.RegisterControllerRequest) (*registry.RegisterControllerReply, error) {
	if req.GetNodeId() == "" {
		return nil, status.Error(codes.InvalidArgument, "Missing NodeId parameter")
	}

	if req.GetEndpoint() == "" {
		return nil, status.Error(codes.InvalidArgument, "Missing endpoint address")
	}

	rs.rpcMutex.LockKey(req.NodeId)
	defer rs.rpcMutex.UnlockKey(req.NodeId)

	klog.V(3).Infof("Registering node: %s, endpoint: %s", req.NodeId, req.Endpoint)

	node := &NodeInfo{
		NodeID:   req.NodeId,
		Endpoint: req.Endpoint,
	}

	rs.mutex.Lock()
	n, found := rs.nodeClients[req.NodeId]
	if found {
		if n.Endpoint != req.Endpoint {
			found = false
		}
	}
	rs.nodeClients[req.NodeId] = node
	pmemNodes.Set(float64(len(rs.nodeClients)))
	rs.mutex.Unlock()

	if !found {
		for l := range rs.listeners {
			if err := l.OnNodeAdded(ctx, node); err != nil {
				rs.mutex.Lock()
				delete(rs.nodeClients, req.NodeId)
				pmemNodes.Set(float64(len(rs.nodeClients)))
				rs.mutex.Unlock()
				return nil, fmt.Errorf("failed to register node: %w", err)
			}
		}
	}

	return &registry.RegisterControllerReply{}, nil
}

func (rs *RegistryServer) UnregisterController(ctx context.Context, req *registry.UnregisterControllerRequest) (*registry.UnregisterControllerReply, error) {
	if req.GetNodeId() == "" {
		return nil, status.Error(codes.InvalidArgument, "Missing NodeId parameter")
	}

	rs.rpcMutex.LockKey(req.NodeId)
	defer rs.rpcMutex.UnlockKey(req.NodeId)

	rs.mutex.Lock()
	node, ok := rs.nodeClients[req.NodeId]
	delete(rs.nodeClients, req.NodeId)
	pmemNodes.Set(float64(len(rs.nodeClients)))
	rs.mutex.Unlock()

	if ok {
		for l := range rs.listeners {
			l.OnNodeDeleted(ctx, node)
		}
		klog.V(3).Infof("Unregistered node: %s", req.NodeId)
	} else {
		klog.V(3).Infof("No node registered with id '%s'", req.NodeId)
	}

	return &registry.UnregisterControllerReply{}, nil
}

// NodeClients returns a new map which contains a copy of all currently known node clients.
// It is safe to use concurrently with the other methods.
func (rs *RegistryServer) NodeClients() map[string]*NodeInfo {
	rs.mutex.Lock()
	defer rs.mutex.Unlock()

	copy := map[string]*NodeInfo{}
	for key, value := range rs.nodeClients {
		copy[key] = value
	}
	return copy
}
