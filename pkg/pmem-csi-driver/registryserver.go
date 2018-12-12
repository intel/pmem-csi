package pmemcsidriver

import (
	"fmt"

	"github.com/intel/pmem-csi/pkg/pmem-registry"
	"golang.org/x/net/context"
	"k8s.io/klog/glog"
)

type registryServer struct {
	nodeClients map[string]NodeInfo
}

type NodeInfo struct {
	//NodeID controller node id
	NodeID string
	//Endpoint node controller endpoint
	Endpoint string
	//Capacity node capacity(unused)
	Capacity uint64
}

func NewRegistryServer() *registryServer {
	return &registryServer{
		nodeClients: map[string]NodeInfo{},
	}
}

//GetNodeController returns the node controller info for given nodeID, error if not found
func (rs *registryServer) GetNodeController(nodeID string) (NodeInfo, error) {
	if node, ok := rs.nodeClients[nodeID]; ok {
		return node, nil
	}

	return NodeInfo{}, fmt.Errorf("No node registered with id: %v", nodeID)
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
		Capacity: req.GetCapacity(),
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

func (rs *registryServer) UpdateNodeCapacity(nodeID string, capacity uint64) error {
	info, ok := rs.nodeClients[nodeID]
	if !ok {
		return fmt.Errorf("No entry with id '%s' found in registry", nodeID)
	}

	info.Capacity = capacity
	rs.nodeClients[nodeID] = info

	return nil
}
