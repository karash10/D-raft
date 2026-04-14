package raft

import (
	"log"
	"math/rand"
	"sync"
	"time"
)

// State defines the current role of the node in the RAFT cluster.
type State int

const (
	Follower State = iota
	Candidate
	Leader
)

func (s State) String() string {
	switch s {
	case Follower:
		return "FOLLOWER"
	case Candidate:
		return "CANDIDATE"
	case Leader:
		return "LEADER"
	default:
		return "UNKNOWN"
	}
}

// Node represents a single server in our 3-replica cluster.
// It manages state transitions, election timeouts, and HTTP peers.
//
// Thread Safety: Because RAFT receives concurrent HTTP requests (RPCs)
// and runs background timers, ALL state variables must be protected by a mutex.
type Node struct {
	// ------------------------------------------------------------------------
	// Server Configuration
	// ------------------------------------------------------------------------
	Mu       sync.RWMutex // Protects concurrent access to node state
	ID       string       // e.g., "replica1", "replica2", "replica3"
	PeerURLs []string     // e.g., []string{"http://replica2:9002", "http://replica3:9003"}
	Logger   *log.Logger

	// ------------------------------------------------------------------------
	// Persistent State on all servers
	// (Updated on stable storage before responding to RPCs in standard RAFT)
	// ------------------------------------------------------------------------
	CurrentTerm int         // Latest term server has seen (initialized to 0)
	VotedFor    string      // candidateId that received vote in current term (or "")
	Log         *LogManager // The append-only log of drawing strokes

	// ------------------------------------------------------------------------
	// Volatile State on all servers
	// ------------------------------------------------------------------------
	CommitIndex int   // Index of highest log entry known to be committed
	LastApplied int   // Index of highest log entry applied to state machine
	State       State // Current role: Follower, Candidate, or Leader

	// ------------------------------------------------------------------------
	// Volatile State on leaders (reinitialized after election)
	// ------------------------------------------------------------------------
	// LeaderState holds nextIndex[] and matchIndex[] for each follower.
	// This is only populated when the node is the Leader.
	// See replication.go for the LeaderState struct definition.
	LeaderState *LeaderState

	// Timers and channels for background tasks
	lastHeartbeat time.Time
	electionTimer *time.Timer
}

// NewNode initializes a fresh RAFT node.
// It starts in the Follower state with term 0 and an empty log.
func NewNode(id string, peers []string, logger *log.Logger) *Node {
	n := &Node{
		ID:          id,
		PeerURLs:    peers,
		Logger:      logger,
		State:       Follower,
		CurrentTerm: 0,
		VotedFor:    "",
		Log:         &LogManager{Entries: make([]LogEntry, 0)},
		CommitIndex: 0,
		LastApplied: 0,
	}

	// According to RAFT, nodes start as followers waiting for heartbeats.
	n.lastHeartbeat = time.Now()
	// Initialize an inactive timer. It will be started properly by the election loop.
	r := n.RandomElectionTimeout()
	n.electionTimer = time.NewTimer(r)
	n.Logger.Print("Election timeout to start the election: ", r)
	return n
}

// RandomElectionTimeout generates a randomized duration between 500ms and 800ms.
// Randomization is critical to prevent split votes where multiple nodes
// become candidates at the exact same moment.
func (n *Node) RandomElectionTimeout() time.Duration {
	// Random float between 0 and 1
	r := rand.Float64()
	// Map to 500ms - 800ms range
	timeoutMs := 500.0 + (r * 300.0)
	return time.Duration(timeoutMs) * time.Millisecond
}

// BecomeFollower forces the node to step down to Follower.
// This is triggered if a Candidate discovers a higher term, or if a Leader
// discovers another Leader with a higher term.
func (n *Node) BecomeFollower(newTerm int) {
	n.State = Follower
	n.CurrentTerm = newTerm
	n.VotedFor = ""
	n.lastHeartbeat = time.Now()
	n.Logger.Printf("term=%d state=FOLLOWER event=stepped_down", n.CurrentTerm)
}
