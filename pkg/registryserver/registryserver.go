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
)

type RegistryListener interface {
	// OnNodeAdded is called by RegistryServer whenever a node controller registered.
	OnNodeAdded(ctx context.Context, node *NodeInfo)
	// OnNodeDeleted is called by RegistryServer whenever a node controller unregistered.
	OnNodeDeleted(ctx context.Context, node *NodeInfo)
}

type RegistryServer struct {
	mutex           sync.Mutex
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

	return pmemgrpc.Connect(nodeInfo.Endpoint, rs.clientTLSConfig)
}

// AddListener adds a callback for add/remove events. The list of callbacks
// is not protected against concurrent access, therefore AddListener must be
// called before starting to use the RegistryServer instance. Also, the callbacks
// are called without protection against concurrent calls and thus must
// handle any needed serialization themselves.
func (rs *RegistryServer) AddListener(l RegistryListener) {
	rs.listeners[l] = struct{}{}
}

func (rs *RegistryServer) RegisterController(ctx context.Context, req *registry.RegisterControllerRequest) (*registry.RegisterControllerReply, error) {
	rs.mutex.Lock()
	defer func() {
		rs.mutex.Unlock()
		for l := range rs.listeners {
			l.OnNodeAdded(ctx, node)
		}
	}()

	if req.GetNodeId() == "" {
		return nil, status.Error(codes.InvalidArgument, "Missing NodeId parameter")
	}

	if req.GetEndpoint() == "" {
		return nil, status.Error(codes.InvalidArgument, "Missing endpoint address")
	}
	glog.V(3).Infof("Registering node: %s, endpoint: %s", req.NodeId, req.Endpoint)

	node := &NodeInfo{
		NodeID:   req.NodeId,
		Endpoint: req.Endpoint,
	}

	rs.nodeClients[req.NodeId] = node

	return &registry.RegisterControllerReply{}, nil
}

func (rs *RegistryServer) UnregisterController(ctx context.Context, req *registry.UnregisterControllerRequest) (*registry.UnregisterControllerReply, error) {
	rs.mutex.Lock()
	defer func() {
		rs.mutex.Unlock()
		for l := range rs.listeners {
			l.OnNodeDeleted(ctx, node)
		}
	}()

	if req.GetNodeId() == "" {
		return nil, status.Error(codes.InvalidArgument, "Missing NodeId parameter")
	}

	node, ok := rs.nodeClients[req.NodeId]
	if !ok {
		return nil, status.Errorf(codes.NotFound, "No entry with id '%s' found in registry", req.NodeId)
	}

	glog.V(3).Infof("Unregistering node: %s", req.NodeId)
	delete(rs.nodeClients, req.NodeId)

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
