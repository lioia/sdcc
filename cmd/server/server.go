package main

import (
	"context"
	"errors"

	"github.com/lioia/distributed-pagerank/lib"
)

type NodeServerImpl struct {
	Node *Node
	lib.UnimplementedNodeServer
}

func (n *NodeServerImpl) HealthCheck(_ context.Context, _ *lib.Empty) (*lib.Empty, error) {
	return &lib.Empty{}, nil
}

func (n *NodeServerImpl) GetInfo(_ context.Context, in *lib.ConnectionInfo) (*lib.Info, error) {
	var info lib.Info
	first, ok := (*n.Node).(*Layer1Node)
	if !ok || first.Layer != 0 {
		return &info, errors.New("cannot ask info on a node that is not the first")
	}
	// TODO: 4 should be a configuration variable
	if len(first.Layer1s) < 4 {
		// There are not enough layer 1s node -> this node is a layer 1
		info.LayerNumber = 1
		// Sending already present layer 1 nodes in the network
		info.Layer1S = first.Layer1s
		// Add node requesting info to list of layer1s
		first.Layer1s = append(first.Layer1s, in)
	} else {
		// Layer 2 node
		info.LayerNumber = 2
		// Find the layer 1 node with the least number of nodes
		var assigned int
		var minNumOfNodes int32 = 1<<31 - 1 // max int32 value
		for i, v := range first.NumberOfNodes {
			if minNumOfNodes > v {
				minNumOfNodes = v
				assigned = i
			}
		}
		info.Assigned = first.Layer1s[assigned]
		first.NumberOfNodes[assigned] += 1
	}
	return &info, nil
}

func (n *NodeServerImpl) Announce(_ context.Context, in *lib.AnnounceMessage) (*lib.Empty, error) {
	empty := &lib.Empty{}
	node, ok := (*n.Node).(*Layer1Node)
	if !ok {
		return empty, errors.New("request cannot be fulfilled by this node")
	}
	if in.LayerNumber == 1 {
		node.Layer1s = append(node.Layer1s, in.Connection)
	} else if in.LayerNumber == 2 {
		node.Layer2s = append(node.Layer2s, in.Connection)
	} else {
		return empty, errors.New("request cannot be fulfilled by this node")
	}
	return empty, nil
}
