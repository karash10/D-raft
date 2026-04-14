package raft

import "sync"

// Stroke represents a single drawing action on the frontend canvas.
// This is the actual data we want to replicate across all nodes.
type Stroke struct {
	X0    float64 `json:"x0"`
	Y0    float64 `json:"y0"`
	X1    float64 `json:"x1"`
	Y1    float64 `json:"y1"`
	Color string  `json:"color"`
	Width float64 `json:"width"`
}

// LogEntry wraps a state machine command (a Stroke) with RAFT metadata.
// In RAFT, every entry Must have an Index and the Term in which it was created.
// This allows replicas to compare their logs and detect inconsistencies.
type LogEntry struct {
	Index  int    `json:"index"`
	Term   int    `json:"term"`
	Stroke Stroke `json:"stroke"`
}

// LogManager encapsulates the append-only log array and its related operations.
// We embed this in the main Node struct to keep log management organized.
type LogManager struct {
	Mu      sync.RWMutex
	Entries []LogEntry
}

// GetLastLogIndexAndTerm returns the metadata of the latest entry in the log.
// This is crucial for the RequestVote RPC, as candidates Must prove their log
// is at least as up-to-date as the voter's log.
func (lm *LogManager) GetLastLogIndexAndTerm() (int, int) {
	lm.Mu.RLock()
	defer lm.Mu.RUnlock()

	if len(lm.Entries) == 0 {
		return 0, 0
	}
	lastEntry := lm.Entries[len(lm.Entries)-1]
	return lastEntry.Index, lastEntry.Term
}

// Append safely adds a new entry to the end of the log.
// RAFT rule: Log entries are strictly append-only.
func (lm *LogManager) Append(entry LogEntry) {
	lm.Mu.Lock()
	defer lm.Mu.Unlock()
	lm.Entries = append(lm.Entries, entry)
}
