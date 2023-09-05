package main

import (
	"encoding/json"
	"fmt"
	"log"
	"net"
	"os"

	"github.com/joho/godotenv"
	"github.com/lioia/distributed-pagerank/graph"
	"github.com/lioia/distributed-pagerank/node"
	"github.com/lioia/distributed-pagerank/proto"
	"github.com/lioia/distributed-pagerank/utils"

	amqp "github.com/rabbitmq/amqp091-go"
	"google.golang.org/grpc"
	"google.golang.org/protobuf/types/known/wrapperspb"
)

func main() {
	// Read environment variables
	errored := false
	master, err := utils.ReadStringEnvVar("MASTER")
	if err != nil {
		errored = true
	}
	rabbitHost, err := utils.ReadStringEnvVar("RABBIT_HOST")
	if err != nil {
		errored = true
	}
	rabbitUser := utils.ReadStringEnvVarOr("RABBIT_USER", "guest")
	rabbitPass := utils.ReadStringEnvVarOr("RABBIT_PASSWORD", "guest")
	host, err := utils.ReadStringEnvVar("HOST")
	if err != nil {
		errored = true
	}
	port, err := utils.ReadIntEnvVar("PORT")
	if err != nil {
		errored = true
	}
	if err := godotenv.Load(); err != nil && errored {
		log.Fatal("Environment variables are not set")
	}

	// Create connection
	lis, err := net.Listen("tcp", fmt.Sprintf(":%d", port))
	utils.FailOnError("Failed to listen for node server", err)
	// lis.Close in goroutine

	// Connect to RabbitMQ
	queue := fmt.Sprintf("amqp://%s:%s@%s:5672/", rabbitUser, rabbitPass, rabbitHost)
	queueConn, err := amqp.Dial(queue)
	utils.FailOnError("Could not connect to RabbitMQ", err)
	defer queueConn.Close()
	ch, err := queueConn.Channel()
	utils.FailOnError("Failed to open a channel to RabbitMQ", err)
	defer ch.Close()

	// Base node values
	n := node.Node{
		State: &proto.State{Phase: int32(node.Wait)},
		Data:  utils.NewSafeMap[int32, float64](),
		Role:  node.Master,
		Queue: node.Queue{
			Conn:    queueConn,
			Channel: ch,
		},
	}
	// Default value for master
	workQueueName := "work"
	resultQueueName := "result"

	// Contact master node to join the network
	client, err := utils.NodeCall(master)
	utils.FailOnError("Failed to create connection to the master node", err)
	defer client.Close()
	join, err := client.Client.NodeJoin(
		client.Ctx,
		&wrapperspb.StringValue{Value: fmt.Sprintf("%s:%d", host, port)},
	)
	if err != nil {
		// There is no node at the address -> creating a new network
		// This node will be the master
		log.Printf("No master node found at %s\n", master)
		c, threshold, graph, err := loadConfiguration()
		if err != nil {
			// Configuration could not be loaded
			log.Println("Configuration will asked later")
		} else {
			// Configuration loaded correctly
			n.State.C = c
			n.State.Threshold = threshold
			n.State.Graph = graph
		}
	} else {
		// Ther is a master node -> this node will be a worker
		n.Role = node.Worker
		n.Master = master
		n.State = join.State
		workQueueName = join.WorkQueue
		resultQueueName = join.ResultQueue
		n.QueueReader = make(chan bool)
	}
	// Queue declaration
	work, err := utils.DeclareQueue(workQueueName, ch)
	utils.FailOnError("Failed to declare 'work' queue", err)
	n.Queue.Work = &work
	result, err := utils.DeclareQueue(resultQueueName, ch)
	utils.FailOnError("Failed to declare 'result' queue", err)
	n.Queue.Result = &result

	// Running gRPC server for internal network communication in a goroutine
	status := make(chan bool)
	go func() {
		// Creating gRPC server
		defer lis.Close()
		server := grpc.NewServer()
		proto.RegisterNodeServer(server, &node.NodeServerImpl{Node: &n})
		log.Printf("Starting %s node at %s:%d\n", node.RoleToString(n.Role), host, port)
		status <- true
		err = server.Serve(lis)
		utils.FailOnError("Failed to serve", err)
	}()
	// Waiting for gRPC server to start
	<-status
	// Node Update
	n.Update()
}

// Load config.json (C, Threshold and graph file)
func loadConfiguration() (c float64, threshold float64, g map[int32]*proto.GraphNode, err error) {
	// Try to open the config.json file
	_, err = os.Open("config.json")
	if err != nil {
		log.Printf("Configuration file does not exists: %v", err)
		return
	}
	// File exists -> load configuration
	bytes, err := os.ReadFile("config.json")
	if err != nil {
		log.Printf("Failed to read configuration file: %v", err)
		return
	}
	// Parse config.json into a Golang struct
	var config node.Config
	err = json.Unmarshal(bytes, &config)
	if err != nil {
		log.Printf("Failed to parse configuration file: %v", err)
		return
	}
	// Parse graph file into graph representation
	g, err = graph.LoadGraphResource(config.Graph)
	if err != nil {
		return
	}
	c = config.C
	threshold = config.Threshold

	return
}
