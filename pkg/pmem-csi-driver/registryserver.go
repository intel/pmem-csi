package pmemcsidriver

import (
	"crypto/tls"
	"fmt"
	"time"

	pmemgrpc "github.com/intel/pmem-csi/pkg/pmem-grpc"
	"github.com/intel/pmem-csi/pkg/pmem-registry"
	"golang.org/x/net/context"
	"google.golang.org/grpc"
	"k8s.io/klog/glog"
)

type registryServer struct {
	clientTLSConfig *tls.Config
	nodeClients     map[string]NodeInfo
}

var _ PmemService = &registryServer{}

type NodeInfo struct {
	//NodeID controller node id
	NodeID string
	//Endpoint node controller endpoint
	Endpoint string
}

func NewRegistryServer(tlsConfig *tls.Config) *registryServer {
	return &registryServer{
		clientTLSConfig: tlsConfig,
		nodeClients:     map[string]NodeInfo{},
	}
}

func (rs *registryServer) RegisterService(rpcServer *grpc.Server) {
	registry.RegisterRegistryServer(rpcServer, rs)
}

//GetNodeController returns the node controller info for given nodeID, error if not found
func (rs *registryServer) GetNodeController(nodeID string) (NodeInfo, error) {
	if node, ok := rs.nodeClients[nodeID]; ok {
		return node, nil
	}

	return NodeInfo{}, fmt.Errorf("No node registered with id: %v", nodeID)
}

// ConnectToNodeController initiates a connection to controller running at nodeId
func (rs *registryServer) ConnectToNodeController(nodeId string, timeout time.Duration) (*grpc.ClientConn, error) {
	nodeInfo, err := rs.GetNodeController(nodeId)
	if err != nil {
		return nil, err
	}

	return pmemgrpc.Connect(nodeInfo.Endpoint, rs.clientTLSConfig, timeout)
}

func (rs *registryServer) RegisterController(ctx context.Context, req *registry.RegisterControllerRequest) (*registry.RegisterControllerReply, error) {
	if req.GetNodeId() == "" {
		return nil, fmt.Errorf("Missing NodeId parameter")
	}

	if req.GetEndpoint() == "" {
		return nil, fmt.Errorf("Missing endpoint address")
	}
	glog.Infof("Registering node: %s, endpoint: %s", req.NodeId, req.Endpoint)

	rs.nodeClients[req.NodeId] = NodeInfo{
		NodeID:   req.NodeId,
		Endpoint: req.Endpoint,
	}

	return &registry.RegisterControllerReply{}, nil
}

func (rs *registryServer) UnregisterController(ctx context.Context, req *registry.UnregisterControllerRequest) (*registry.UnregisterControllerReply, error) {
	if req.GetNodeId() == "" {
		return nil, fmt.Errorf("Missing NodeId parameter")
	}

	if _, ok := rs.nodeClients[req.NodeId]; !ok {
		return nil, fmt.Errorf("No entry with id '%s' found in registry", req.NodeId)
	}

	glog.Infof("Unregistering node: %s", req.NodeId)
	delete(rs.nodeClients, req.NodeId)

	return &registry.UnregisterControllerReply{}, nil
}
