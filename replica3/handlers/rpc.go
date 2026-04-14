package handlers

import (
	"miniraft/replica/raft"

	"github.com/gofiber/fiber/v2"
)

// =============================================================================
// RPC HANDLERS
// =============================================================================
// This file binds the RAFT logic from the raft package to HTTP endpoints.
// Each endpoint receives a JSON request, calls the appropriate Node method,
// and returns a JSON response.
//
// The Fiber framework provides:
//   - Automatic JSON parsing via c.BodyParser()
//   - Automatic JSON serialization via c.JSON()
//   - Fast HTTP routing and middleware support

// RPCHandler holds a reference to the RAFT node and provides HTTP handlers.
type RPCHandler struct {
	Node *raft.Node
}

// NewRPCHandler creates a new handler instance with the given RAFT node.
func NewRPCHandler(node *raft.Node) *RPCHandler {
	return &RPCHandler{Node: node}
}

// =============================================================================
// POST /request-vote
// =============================================================================
// Called by a Candidate during election to request a vote from this node.
//
// Request Body:
//
//	{
//	  "term": 3,
//	  "candidateId": "replica2",
//	  "lastLogIndex": 12,
//	  "lastLogTerm": 2
//	}
//
// Response Body:
//
//	{
//	  "term": 3,
//	  "voteGranted": true
//	}
func (h *RPCHandler) HandleRequestVote(c *fiber.Ctx) error {
	// Parse the incoming JSON request
	var req raft.RequestVoteRequest
	if err := c.BodyParser(&req); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": "invalid request body: " + err.Error(),
		})
	}

	// Delegate to the RAFT node's vote handling logic
	// This will check terms, voting status, and log up-to-dateness
	resp := h.Node.HandleRequestVote(req)

	// Return the response as JSON
	return c.JSON(resp)
}

// =============================================================================
// POST /append-entries
// =============================================================================
// Called by the Leader to replicate log entries to this node.
// Also serves as a heartbeat when entries array is empty.
//
// Request Body:
//
//	{
//	  "term": 3,
//	  "leaderId": "replica1",
//	  "prevLogIndex": 11,
//	  "prevLogTerm": 2,
//	  "entries": [...],
//	  "leaderCommit": 11
//	}
//
// Response Body:
//
//	{
//	  "term": 3,
//	  "success": true,
//	  "logLength": 12
//	}
func (h *RPCHandler) HandleAppendEntries(c *fiber.Ctx) error {
	// Parse the incoming JSON request
	var req raft.AppendEntriesRequest
	if err := c.BodyParser(&req); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": "invalid request body: " + err.Error(),
		})
	}

	// Delegate to the RAFT node's append entries logic
	// This will check terms, log consistency, and append entries
	resp := h.Node.HandleAppendEntries(req)

	// Return the response as JSON
	return c.JSON(resp)
}

// =============================================================================
// POST /heartbeat
// =============================================================================
// Lightweight keep-alive from the Leader.
// This is essentially an AppendEntries with no log entries, but separated
// for clarity and potential future optimizations.
//
// Request Body:
//
//	{
//	  "term": 3,
//	  "leaderId": "replica1"
//	}
//
// Response Body:
//
//	{
//	  "term": 3,
//	  "success": true
//	}
func (h *RPCHandler) HandleHeartbeat(c *fiber.Ctx) error {
	// Parse the incoming JSON request
	var req raft.HeartbeatRequest
	if err := c.BodyParser(&req); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": "invalid request body: " + err.Error(),
		})
	}

	// Delegate to the RAFT node's heartbeat logic
	// This will check terms and reset the election timer
	resp := h.Node.HandleHeartbeat(req)

	// Return the response as JSON
	return c.JSON(resp)
}

// =============================================================================
// POST /sync-log
// =============================================================================
// Called by the Leader to send all committed entries to a follower that
// has fallen behind (e.g., after a crash/restart).
//
// Request Body:
//
//	{
//	  "fromIndex": 0,
//	  "entries": [...]
//	}
//
// Response Body:
//
//	{
//	  "success": true,
//	  "syncedUpTo": 47
//	}
func (h *RPCHandler) HandleSyncLog(c *fiber.Ctx) error {
	// Parse the incoming JSON request
	var req raft.SyncLogRequest
	if err := c.BodyParser(&req); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": "invalid request body: " + err.Error(),
		})
	}

	// Delegate to the RAFT node's sync log logic
	// This will replace the local log with the leader's entries
	resp := h.Node.HandleSyncLog(req)

	// Return the response as JSON
	return c.JSON(resp)
}

// =============================================================================
// GET /status
// =============================================================================
// Returns the current node state. Used by the Gateway to discover the leader.
//
// Response Body:
//
//	{
//	  "replicaId": "replica1",
//	  "state": "leader",
//	  "term": 3,
//	  "commitIndex": 47,
//	  "logLength": 48
//	}
func (h *RPCHandler) HandleStatus(c *fiber.Ctx) error {
	// Get the current state from the RAFT node
	// We need to lock to safely read the node's state
	h.Node.Mu.Lock()
	defer h.Node.Mu.Unlock()

	// Build the status response
	status := fiber.Map{
		"replicaId":   h.Node.ID,
		"state":       h.Node.State.String(),
		"term":        h.Node.CurrentTerm,
		"commitIndex": h.Node.CommitIndex,
		"logLength":   len(h.Node.Log.Entries),
	}

	return c.JSON(status)
}

// =============================================================================
// POST /stroke
// =============================================================================
// Called by the Gateway when a client draws a stroke.
// This endpoint is ONLY valid on the Leader node.
//
// Request Body:
//
//	{
//	  "x0": 100, "y0": 200,
//	  "x1": 150, "y1": 250,
//	  "color": "#e63946",
//	  "width": 3
//	}
//
// Response Body (success):
//
//	{
//	  "success": true,
//	  "committed": true,
//	  "index": 48
//	}
//
// Response Body (not leader):
//
//	{
//	  "success": false,
//	  "error": "not the leader",
//	  "leaderId": "replica2"  // hint for gateway to redirect
//	}
func (h *RPCHandler) HandleStroke(c *fiber.Ctx) error {
	// Parse the incoming stroke from the request body
	var stroke raft.Stroke
	if err := c.BodyParser(&stroke); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": "invalid request body: " + err.Error(),
		})
	}

	// Attempt to replicate the stroke (only works if we're the Leader)
	committed, err := h.Node.ReplicateEntry(stroke)
	if err != nil {
		// Not the leader - return an error with a hint
		return c.Status(fiber.StatusServiceUnavailable).JSON(fiber.Map{
			"success": false,
			"error":   err.Error(),
		})
	}

	// Return success
	h.Node.Mu.Lock()
	logLength := len(h.Node.Log.Entries)
	h.Node.Mu.Unlock()

	return c.JSON(fiber.Map{
		"success":   true,
		"committed": committed,
		"index":     logLength,
	})
}

// =============================================================================
// GET /log
// =============================================================================
// Debug endpoint to view the current log entries.
// Useful for verifying replication during development.
//
// Response Body:
//
//	{
//	  "entries": [...],
//	  "commitIndex": 47,
//	  "length": 48
//	}
func (h *RPCHandler) HandleGetLog(c *fiber.Ctx) error {
	h.Node.Mu.Lock()
	defer h.Node.Mu.Unlock()

	// Make a copy of the entries to avoid holding the lock during serialization
	entries := make([]raft.LogEntry, len(h.Node.Log.Entries))
	copy(entries, h.Node.Log.Entries)

	return c.JSON(fiber.Map{
		"entries":     entries,
		"commitIndex": h.Node.CommitIndex,
		"length":      len(entries),
	})
}

// =============================================================================
// REGISTER ROUTES
// =============================================================================
// This function registers all RPC endpoints with the Fiber app.

// RegisterRoutes attaches all RPC handlers to the Fiber app.
// Call this from main.go after creating the Fiber app and RPCHandler.
func (h *RPCHandler) RegisterRoutes(app *fiber.App) {
	// RAFT RPC endpoints (called by other replicas)
	app.Post("/request-vote", h.HandleRequestVote)
	app.Post("/append-entries", h.HandleAppendEntries)
	app.Post("/heartbeat", h.HandleHeartbeat)
	app.Post("/sync-log", h.HandleSyncLog)

	// Status endpoint (called by Gateway for leader discovery)
	app.Get("/status", h.HandleStatus)

	// Stroke endpoint (called by Gateway to submit new strokes)
	app.Post("/stroke", h.HandleStroke)

	// Debug endpoints
	app.Get("/log", h.HandleGetLog)
}
