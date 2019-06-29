package registryserver

import (
	"crypto/tls"
	"fmt"
	"sync"

	pmemgrpc "github.com/intel/pmem-csi/pkg/pmem-grpc"
	registry "github.com/intel/pmem-csi/pkg/pmem-registry"
	"golang.org/x/net/context"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"k8s.io/klog/glog"
	"k8s.io/utils/keymutex"
)

// RegistryListener is an interface for registry server change listeners
// All the callbacks are called once after updating the in-memory
// registry data.
type RegistryListener interface {
	// OnNodeAdded is called by RegistryServer whenever a new node controller is registered
	// or node controller updated its endpoint.
	OnNodeAdded(ctx context.Context, node *NodeInfo)
	// OnNodeDeleted is called by RegistryServer whenever a node controller unregistered.
	// Callback implementations has to note that by the time this method is called,
	// the NodeInfo for that node have already removed from in-memory registry.
	OnNodeDeleted(ctx context.Context, node *NodeInfo)
}

type RegistryServer struct {
	mutex           sync.Mutex
	rpcMutex        keymutex.KeyMutex
	clientTLSConfig *tls.Config
	nodeClients     map[string]*NodeInfo
	listeners       map[RegistryListener]struct{}
}

type NodeInfo struct {
	//NodeID controller node id
	NodeID string
	//Endpoint node controller endpoint
	Endpoint string
}

func New(tlsConfig *tls.Config) *RegistryServer {
	return &RegistryServer{
		rpcMutex:        keymutex.NewHashed(-1),
		clientTLSConfig: tlsConfig,
		nodeClients:     map[string]*NodeInfo{},
		listeners:       map[RegistryListener]struct{}{},
	}
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

	glog.V(3).Infof("Connecting to node controller : %s", nodeInfo.Endpoint)

	return pmemgrpc.Connect(nodeInfo.Endpoint, rs.clientTLSConfig)
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

	glog.V(3).Infof("Registering node: %s, endpoint: %s", req.NodeId, req.Endpoint)

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
	rs.mutex.Unlock()

	if !found {
		for l := range rs.listeners {
			l.OnNodeAdded(ctx, node)
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
	rs.mutex.Unlock()

	if ok {
		for l := range rs.listeners {
			l.OnNodeDeleted(ctx, node)
		}
		glog.V(3).Infof("Unregistered node: %s", req.NodeId)
	} else {
		glog.V(3).Infof("No node registered with id '%s'", req.NodeId)
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
