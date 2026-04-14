package main

import (
	"fmt"
	"log"
	"math/rand"
	"os"
	"strings"
	"time"

	"miniraft/replica/handlers"
	"miniraft/replica/raft"

	"github.com/gofiber/fiber/v2"
	"github.com/gofiber/fiber/v2/middleware/cors"
	"github.com/gofiber/fiber/v2/middleware/logger"
)

// MAIN ENTRY POINT
//
// This is the entry point for a RAFT replica node.
//
// Configuration is done via environment variables:
//   - REPLICA_ID: Unique identifier for this node (e.g., "replica1")
//   - PORT: HTTP port to listen on (e.g., "9001")
//   - PEERS: Comma-separated URLs of peer replicas (e.g., "http://replica2:9002,http://replica3:9003")
//
// The node:
//   1. Reads configuration from environment
//   2. Initializes the RAFT node (starts as Follower)
//   3. Starts the election timeout loop (background goroutine)
//   4. Starts the Fiber HTTP server to handle RPC requests
//
// Flow:
//   startup → Follower → [election timeout] → Candidate → [majority votes] → Leader
//                ↑                                             │
//                └─────────────[higher term discovered]────────┘

func main() {
	// -------------------------------------------------------------------------
	// Seed the random number generator
	// -------------------------------------------------------------------------
	// CRITICAL: Without this, all nodes would have the same "random" election
	// timeouts, defeating the purpose of randomization to prevent split votes.
	// We use the current time plus some node-specific data to ensure uniqueness.
	rand.Seed(time.Now().UnixNano())

	// -------------------------------------------------------------------------
	// Read configuration from environment variables
	// -------------------------------------------------------------------------
	replicaID := os.Getenv("REPLICA_ID")
	if replicaID == "" {
		log.Fatal("REPLICA_ID environment variable is required")
	}

	port := os.Getenv("PORT")
	if port == "" {
		log.Fatal("PORT environment variable is required")
	}

	peersEnv := os.Getenv("PEERS")
	if peersEnv == "" {
		log.Fatal("PEERS environment variable is required")
	}

	// Parse the comma-separated list of peer URLs
	// Example: "http://replica2:9002,http://replica3:9003"
	peers := strings.Split(peersEnv, ",")
	for i, peer := range peers {
		peers[i] = strings.TrimSpace(peer)
	}

	// -------------------------------------------------------------------------
	// Create a custom logger with the replica ID prefix
	// -------------------------------------------------------------------------
	// All log messages will be prefixed with [replicaX] for easy filtering
	// in docker-compose logs or when tailing multiple replica logs.
	customLogger := log.New(os.Stdout, fmt.Sprintf("[%s] ", replicaID), log.LstdFlags)

	customLogger.Printf("Starting RAFT replica...")
	customLogger.Printf("  ID:    %s", replicaID)
	customLogger.Printf("  Port:  %s", port)
	customLogger.Printf("  Peers: %v", peers)

	// -------------------------------------------------------------------------
	// Initialize the RAFT node
	// -------------------------------------------------------------------------
	// The node starts as a Follower with term 0 and an empty log.
	// The election timer is initialized but the election loop hasn't started yet.
	node := raft.NewNode(replicaID, peers, customLogger)

	customLogger.Printf("term=%d state=%s event=node_initialized", node.CurrentTerm, node.State)

	// -------------------------------------------------------------------------
	// Start the election timeout loop (background goroutine)
	// -------------------------------------------------------------------------
	// This goroutine continuously monitors the election timer.
	// If the timer expires (no heartbeat received from Leader), it triggers an election.
	// This is the core of RAFT's leader election mechanism.
	go node.RunElectionTimer()

	customLogger.Printf("term=%d state=%s event=election_timer_started", node.CurrentTerm, node.State)

	// -------------------------------------------------------------------------
	// Create the Fiber HTTP server
	// -------------------------------------------------------------------------
	// Fiber is a fast, Express-inspired web framework for Go.
	// It handles JSON parsing, routing, and middleware out of the box.
	app := fiber.New(fiber.Config{
		// Disable the startup banner for cleaner logs
		DisableStartupMessage: true,
		// Set a reasonable read timeout for RPC requests
		ReadTimeout: 10 * time.Second,
		// Set a reasonable write timeout for RPC responses
		WriteTimeout: 10 * time.Second,
	})

	// -------------------------------------------------------------------------
	// Add middleware
	// -------------------------------------------------------------------------

	// CORS middleware: Allow requests from any origin
	// This is necessary for the frontend to make requests to the replicas
	// (though in production, the Gateway should be the only client)
	app.Use(cors.New(cors.Config{
		AllowOrigins: "*",
		AllowMethods: "GET,POST,PUT,DELETE,OPTIONS",
		AllowHeaders: "Content-Type,Authorization",
	}))

	// Logger middleware: Log all incoming requests
	// Format: [replica1] 2024/01/15 10:30:45 | 200 | 1.234ms | POST /heartbeat
	app.Use(logger.New(logger.Config{
		Format:     fmt.Sprintf("[%s] ${time} | ${status} | ${latency} | ${method} ${path}\n", replicaID),
		TimeFormat: "2006/01/02 15:04:05",
	}))

	// -------------------------------------------------------------------------
	// Register RPC routes
	// -------------------------------------------------------------------------
	// The RPCHandler binds our RAFT node to HTTP endpoints.
	// See handlers/rpc.go for the full list of endpoints.
	rpcHandler := handlers.NewRPCHandler(node)
	rpcHandler.RegisterRoutes(app)

	// -------------------------------------------------------------------------
	// Health check endpoint
	// -------------------------------------------------------------------------
	// Simple endpoint to verify the server is running.
	// Useful for Docker health checks and load balancer probes.
	app.Get("/health", func(c *fiber.Ctx) error {
		return c.JSON(fiber.Map{
			"status":    "ok",
			"replicaId": replicaID,
		})
	})

	// -------------------------------------------------------------------------
	// Start the HTTP server
	// -------------------------------------------------------------------------
	// This blocks forever (or until the process is killed).
	// The server listens on 0.0.0.0:PORT to accept connections from any interface.
	listenAddr := fmt.Sprintf(":%s", port)
	customLogger.Printf("term=%d state=%s event=server_starting addr=%s", node.CurrentTerm, node.State, listenAddr)

	if err := app.Listen(listenAddr); err != nil {
		customLogger.Fatalf("Failed to start server: %v", err)
	}
}
