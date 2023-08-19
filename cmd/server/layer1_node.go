package main

import (
	"context"
	"errors"
	"fmt"
	"sync"

	"github.com/lioia/distributed-pagerank/lib"
)

func (n *Layer1Node) Init(info *lib.Info) error {
	for _, v := range info.GetLayer1S() {
		// Save information on the other layer 1 nodes
		n.Layer1s = append(n.Layer1s, v)
		// Contact other layer 1 nodes
		layer1Url := fmt.Sprintf("%s:%d", v.Address, v.Port)
		clientInfo, err := lib.Layer1ClientCall(layer1Url)
		// FIXME: error handling
		if err != nil {
			return err
		}
		announceMsg := lib.AnnounceMessage{
			LayerNumber: 1,
			Connection: &lib.ConnectionInfo{
				Address: n.Address,
				Port:    n.Port,
			},
		}
		_, err = clientInfo.Client.Announce(clientInfo.Ctx, &announceMsg)
		// FIXME: error handling
		if err != nil {
			return err
		}
	}
	return nil
}

func (n *Layer1Node) Update() error {
	// TODO: implement what the layer 1 node has to do
	switch n.Phase {
	// Send data to layer 2 nodes and wait for results (in goroutines)
	case Map:
		n.Map()
		// TODO: case Collect: Send data to layer 1 nodes and wait for their data
	}
	return nil
}

func (n *Layer1Node) Map() {
	var wg sync.WaitGroup
	errored := make(chan int) // -1: no errors; >= 0 i-th layer 2 error
	// For each layer 2 node
	for i, layer2 := range n.Layer2s {
		wg.Add(1)
		// Create goroutine, send subgraph and wait for results
		go func(i int, layer2 *lib.ConnectionInfo) {
			defer wg.Done()
			subGraph := n.SubGraphs[i]
			clientUrl := fmt.Sprintf("%s:%d", layer2.Address, layer2.Port)
			clientInfo, err := lib.Layer2ClientCall(clientUrl)
			// FIXME: error handling
			if err != nil {
				errored <- i
				return
			}
			message := lib.SubGraph{Graph: subGraph}
			maps, err := clientInfo.Client.ComputeMap(clientInfo.Ctx, &message)
			// FIXME: error handling
			if err != nil {
				errored <- i
				return
			}
			for id, v := range maps.GetContribution() {
				n.MapData[id] += v
			}
			n.Counter += 1
			errored <- -1
		}(i, layer2)
	}
	for i := range errored {
		// i-th layer 2 node errored
		if i != -1 {
			// Remove from network (assuming crash)
			n.Layer2s = append(n.Layer2s[:i], n.Layer2s[i+1:]...)
			// Calculating Map in this node
			for _, node := range n.SubGraphs[i] {
				contributions := node.Map()
				for id, v := range contributions {
					n.MapData[id] += v
				}
			}
		}
	}
	wg.Wait()
	// Map phase completed, go to Collect phase
	n.Counter = 0
	n.Phase = Collect
}

type Layer1NodeServerImpl struct {
	Node *Layer1Node
	lib.UnimplementedLayer1NodeServer
}

func (s *Layer1NodeServerImpl) HealthCheck(context.Context, *lib.Empty) (*lib.Empty, error) {
	return &lib.Empty{}, nil
}

func (s *Layer1NodeServerImpl) Announce(_ context.Context, in *lib.AnnounceMessage) (*lib.Empty, error) {
	if in.LayerNumber == 1 {
		s.Node.Layer1s = append(s.Node.Layer1s, in.Connection)
	} else if in.LayerNumber == 2 {
		s.Node.Layer2s = append(s.Node.Layer2s, in.Connection)
	} else {
		return &lib.Empty{}, errors.New("invalid layer number")
	}
	return &lib.Empty{}, nil
}

func (s *Layer1NodeServerImpl) ReceiveGraph(_ context.Context, in *lib.SubGraph) (*lib.Empty, error) {
	empty := &lib.Empty{}
	s.Node.MapData = make(map[int32]float64)
	// No layer 2 nodes, computing map by itself and switch to Collect phase
	if len(s.Node.Layer2s) == 0 {
		for _, node := range in.Graph {
			contributions := node.Map()
			for id, v := range contributions {
				s.Node.MapData[id] += v
			}
		}
		s.Node.Phase = Collect
		return empty, nil
	}
	// Save information and set to Map phase
	s.Node.Graph = in.Graph
	s.Node.Phase = Map
	s.Node.SubGraphs = make([]lib.Graph, len(s.Node.Layer2s))
	// # nodes to send to layer 2 network node
	graphNodesPerNetworkNodes := len(in.Graph) / len(s.Node.Layer2s)
	// Divide graph into multiple subgraphs
	index := 0
	for id, node := range in.Graph {
		s.Node.SubGraphs[index/graphNodesPerNetworkNodes][id] = node
		index += 1
	}

	return empty, nil
}