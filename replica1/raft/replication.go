package raft

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"sync"
	"time"
)

// =============================================================================
// APPEND ENTRIES RPC STRUCTURES
// =============================================================================
// These structs define the JSON payloads for the AppendEntries RPC.
// AppendEntries serves dual purposes:
//   1. Heartbeat (when entries is empty) - Leader sends to maintain authority
//   2. Log Replication (when entries is non-empty) - Leader replicates new strokes

// AppendEntriesRequest is sent by the Leader to replicate log entries.
// According to RAFT:
//   - Term: Leader's current term
//   - LeaderId: So followers can redirect clients (or for debugging)
//   - PrevLogIndex: Index of log entry immediately preceding new ones
//   - PrevLogTerm: Term of PrevLogIndex entry
//   - Entries: Log entries to store (empty for heartbeat)
//   - LeaderCommit: Leader's commitIndex (tells followers what's safe to apply)
type AppendEntriesRequest struct {
	Term         int        `json:"term"`
	LeaderId     string     `json:"leaderId"`
	PrevLogIndex int        `json:"prevLogIndex"`
	PrevLogTerm  int        `json:"prevLogTerm"`
	Entries      []LogEntry `json:"entries"`
	LeaderCommit int        `json:"leaderCommit"`
}

// AppendEntriesResponse is returned by a Follower in response to AppendEntries.
//   - Term: The responder's current term (leader Must step down if this is higher)
//   - Success: True if follower contained entry matching prevLogIndex and prevLogTerm
//   - LogLength: Current length of the follower's log (helps leader track progress)
type AppendEntriesResponse struct {
	Term      int  `json:"term"`
	Success   bool `json:"success"`
	LogLength int  `json:"logLength"`
}

// =============================================================================
// HEARTBEAT RPC STRUCTURES
// =============================================================================
// Heartbeat is a lightweight keep-alive message from Leader to Followers.
// It's essentially an AppendEntries with no log entries, but we define
// separate structs for clarity and to match the API spec.

// HeartbeatRequest is sent by the Leader every 150ms to maintain authority.
type HeartbeatRequest struct {
	Term     int    `json:"term"`
	LeaderId string `json:"leaderId"`
}

// HeartbeatResponse is returned by a Follower acknowledging the heartbeat.
type HeartbeatResponse struct {
	Term    int  `json:"term"`
	Success bool `json:"success"`
}

// =============================================================================
// SYNC LOG RPC STRUCTURES
// =============================================================================
// SyncLog is used when a Follower's log is too far behind (e.g., after restart).
// Instead of replaying AppendEntries one at a time, the Leader sends all
// committed entries in a single request.

// SyncLogRequest is sent by the Leader to a follower that needs to catch up.
//   - FromIndex: The starting index (usually 0 for a fresh node)
//   - Entries: All committed log entries from FromIndex onward
type SyncLogRequest struct {
	FromIndex int        `json:"fromIndex"`
	Entries   []LogEntry `json:"entries"`
}

// SyncLogResponse confirms the follower has received and applied the entries.
//   - Success: True if the sync was successful
//   - SyncedUpTo: The index of the last entry that was synced
type SyncLogResponse struct {
	Success    bool `json:"success"`
	SyncedUpTo int  `json:"syncedUpTo"`
}

// =============================================================================
// LEADER STATE FOR REPLICATION
// =============================================================================
// When a node becomes Leader, it needs to track the replication progress
// for each follower. These are volatile state that's reset on each election.

// LeaderState holds volatile state used only when this node is the Leader.
// This is re-initialized every time the node wins an election.
type LeaderState struct {
	// nextIndex[peerURL] = Index of the next log entry to send to that peer
	// Initialized to leader's last log index + 1
	NextIndex map[string]int

	// matchIndex[peerURL] = Index of highest log entry known to be replicated on peer
	// Initialized to 0, increases monotonically
	MatchIndex map[string]int
}

// InitLeaderState initializes the leader-specific state when becoming Leader.
// This MuST be called when transitioning from Candidate to Leader.
//
// According to RAFT:
//   - nextIndex[] initialized to leader's last log index + 1
//   - matchIndex[] initialized to 0
func (n *Node) InitLeaderState() {
	lastLogIndex, _ := n.Log.GetLastLogIndexAndTerm()

	n.LeaderState = &LeaderState{
		NextIndex:  make(map[string]int),
		MatchIndex: make(map[string]int),
	}

	// Initialize nextIndex for each peer to point just past our last entry
	// This optimistically assumes followers are up-to-date; we'll decrement
	// if AppendEntries fails due to log inconsistency
	for _, peer := range n.PeerURLs {
		n.LeaderState.NextIndex[peer] = lastLogIndex + 1
		n.LeaderState.MatchIndex[peer] = 0
	}
}

// =============================================================================
// HEARTBEAT LOOP (Leader Only)
// =============================================================================
// The Leader Must send heartbeats to all peers every 150ms to prevent
// them from starting elections.

// RunHeartbeatLoop is started as a goroutine when a node becomes Leader.
// It sends heartbeats to all peers every 150ms (HEARTBEAT_INTERVAL).
//
// The loop terminates when the node is no longer the Leader.
func (n *Node) RunHeartbeatLoop() {
	// Initialize leader state for tracking follower progress
	n.Mu.Lock()
	n.InitLeaderState()
	n.Mu.Unlock()

	// Create a ticker that fires every 150ms
	ticker := time.NewTicker(150 * time.Millisecond)
	defer ticker.Stop()

	for {
		<-ticker.C

		n.Mu.Lock()
		// Stop the heartbeat loop if we're no longer the Leader
		if n.State != Leader {
			n.Mu.Unlock()
			n.Logger.Printf("term=%d state=%s event=heartbeat_loop_stopped", n.CurrentTerm, n.State)
			return
		}

		// Capture current state for the heartbeat
		currentTerm := n.CurrentTerm
		leaderId := n.ID
		leaderCommit := n.CommitIndex
		peers := make([]string, len(n.PeerURLs))
		copy(peers, n.PeerURLs)

		n.Mu.Unlock()

		// Send heartbeats to all peers in parallel
		var wg sync.WaitGroup
		for _, peerURL := range peers {
			wg.Add(1)
			go func(url string) {
				defer wg.Done()
				n.sendHeartbeatToPeer(url, currentTerm, leaderId, leaderCommit)
			}(peerURL)
		}
		wg.Wait()

		n.Logger.Printf("term=%d state=LEADER event=heartbeat_sent peers=%d", currentTerm, len(peers))
	}
}

// sendHeartbeatToPeer sends a heartbeat to a single peer.
// If the peer responds with a higher term, we step down to Follower.
func (n *Node) sendHeartbeatToPeer(peerURL string, term int, leaderId string, leaderCommit int) {
	req := HeartbeatRequest{
		Term:     term,
		LeaderId: leaderId,
	}

	resp, err := n.sendHeartbeat(peerURL, req)
	if err != nil {
		// Peer is unreachable - this is expected during network partitions or crashes
		return
	}

	// Check if the peer has a higher term (we Must step down)
	n.Mu.Lock()
	if resp.Term > n.CurrentTerm {
		n.Logger.Printf("term=%d state=LEADER event=higher_term_discovered newTerm=%d from=%s",
			n.CurrentTerm, resp.Term, peerURL)
		n.BecomeFollower(resp.Term)
	}
	n.Mu.Unlock()
}

// =============================================================================
// HANDLE HEARTBEAT (Receiver Side)
// =============================================================================
// Called when a Follower receives a heartbeat from the Leader.

// HandleHeartbeat processes an incoming heartbeat from the Leader.
// This resets the election timer, preventing the Follower from starting
// an unnecessary election.
//
// According to RAFT:
//  1. If term < currentTerm, reject
//  2. If term >= currentTerm, reset election timer, return success
func (n *Node) HandleHeartbeat(req HeartbeatRequest) HeartbeatResponse {
	n.Mu.Lock()
	defer n.Mu.Unlock()

	response := HeartbeatResponse{
		Term:    n.CurrentTerm,
		Success: false,
	}

	// Reject heartbeats from old terms
	if req.Term < n.CurrentTerm {
		n.Logger.Printf("term=%d state=%s event=heartbeat_rejected reason=stale_term senderTerm=%d sender=%s",
			n.CurrentTerm, n.State.String(), req.Term, req.LeaderId)
		return response
	}

	// If we see a higher or equal term from a Leader, accept their authority
	if req.Term > n.CurrentTerm {
		n.BecomeFollower(req.Term)
		response.Term = n.CurrentTerm
	} else if n.State == Candidate {
		// We were a Candidate but received a heartbeat from a Leader with the same term
		// This means someone else won the election, so we step down
		n.BecomeFollower(req.Term)
	}

	// Reset election timer - the Leader is alive!
	n.ResetElectionTimer()
	response.Success = true

	n.Logger.Printf("term=%d state=%s event=heartbeat_received from=%s",
		n.CurrentTerm, n.State.String(), req.LeaderId)

	return response
}

// =============================================================================
// HANDLE APPEND ENTRIES (Receiver Side)
// =============================================================================
// Called when a Follower receives an AppendEntries RPC from the Leader.
// This is the core of log replication.

// HandleAppendEntries processes an incoming AppendEntries RPC from the Leader.
// This is the most complex handler, implementing the log consistency check.
//
// According to RAFT:
//  1. Reply false if term < currentTerm
//  2. Reply false if log doesn't contain an entry at prevLogIndex with prevLogTerm
//  3. If an existing entry conflicts with a new one, delete it and all following
//  4. Append any new entries not already in the log
//  5. If leaderCommit > commitIndex, set commitIndex = min(leaderCommit, index of last new entry)
func (n *Node) HandleAppendEntries(req AppendEntriesRequest) AppendEntriesResponse {
	n.Mu.Lock()
	defer n.Mu.Unlock()

	response := AppendEntriesResponse{
		Term:      n.CurrentTerm,
		Success:   false,
		LogLength: len(n.Log.Entries),
	}

	// Rule 1: Reject if the leader's term is less than ours
	if req.Term < n.CurrentTerm {
		n.Logger.Printf("term=%d state=%s event=append_entries_rejected reason=stale_term senderTerm=%d sender=%s",
			n.CurrentTerm, n.State.String(), req.Term, req.LeaderId)
		return response
	}

	// If we see a higher or equal term from a Leader, accept their authority
	if req.Term > n.CurrentTerm {
		n.BecomeFollower(req.Term)
		response.Term = n.CurrentTerm
	} else if n.State == Candidate {
		// We were a Candidate but received AppendEntries from a Leader with the same term
		n.BecomeFollower(req.Term)
	}

	// Reset election timer - valid AppendEntries means Leader is alive
	n.ResetElectionTimer()

	// Rule 2: Check log consistency
	// The leader sends prevLogIndex and prevLogTerm to verify our logs match
	// If they don't match, we need a sync-log to catch up
	if req.PrevLogIndex > 0 {
		// Check if we have an entry at prevLogIndex
		if req.PrevLogIndex > len(n.Log.Entries) {
			// Our log is too short - we're missing entries
			n.Logger.Printf("term=%d state=%s event=append_entries_rejected reason=log_too_short "+
				"prevLogIndex=%d ourLogLength=%d sender=%s",
				n.CurrentTerm, n.State.String(), req.PrevLogIndex, len(n.Log.Entries), req.LeaderId)
			return response
		}

		// Check if the entry at prevLogIndex has the expected term
		// (Log indices are 1-based in RAFT, but our slice is 0-based)
		prevEntry := n.Log.Entries[req.PrevLogIndex-1]
		if prevEntry.Term != req.PrevLogTerm {
			// Log inconsistency detected - the entry at prevLogIndex has a different term
			// This means our log diverged from the leader's at some point
			n.Logger.Printf("term=%d state=%s event=append_entries_rejected reason=term_mismatch "+
				"prevLogIndex=%d expectedTerm=%d actualTerm=%d sender=%s",
				n.CurrentTerm, n.State.String(), req.PrevLogIndex, req.PrevLogTerm, prevEntry.Term, req.LeaderId)
			return response
		}
	}

	// Rule 3 & 4: Append new entries
	// We need to handle conflicts: if an existing entry conflicts with a new one,
	// delete the existing entry and all that follow it
	for i, entry := range req.Entries {
		entryIndex := req.PrevLogIndex + 1 + i

		if entryIndex <= len(n.Log.Entries) {
			// We already have an entry at this index
			existingEntry := n.Log.Entries[entryIndex-1]
			if existingEntry.Term != entry.Term {
				// Conflict! Delete this entry and all following entries
				n.Log.Mu.Lock()
				n.Log.Entries = n.Log.Entries[:entryIndex-1]
				n.Log.Mu.Unlock()
				n.Logger.Printf("term=%d state=%s event=log_truncated at_index=%d",
					n.CurrentTerm, n.State.String(), entryIndex)
			}
		}

		// Append the entry if we don't have it yet
		if entryIndex > len(n.Log.Entries) {
			n.Log.Append(entry)
			n.Logger.Printf("term=%d state=%s event=entry_appended index=%d entryTerm=%d",
				n.CurrentTerm, n.State.String(), entry.Index, entry.Term)
		}
	}

	// Rule 5: Update commit index
	if req.LeaderCommit > n.CommitIndex {
		// Set commitIndex to the miniMum of leaderCommit and our last log index
		lastLogIndex := len(n.Log.Entries)
		if req.LeaderCommit < lastLogIndex {
			n.CommitIndex = req.LeaderCommit
		} else {
			n.CommitIndex = lastLogIndex
		}
		n.Logger.Printf("term=%d state=%s event=commit_index_updated newCommitIndex=%d",
			n.CurrentTerm, n.State.String(), n.CommitIndex)
	}

	response.Success = true
	response.LogLength = len(n.Log.Entries)
	return response
}

// =============================================================================
// HANDLE SYNC LOG (Receiver Side)
// =============================================================================
// Called when a Follower receives a SyncLog RPC from the Leader.
// This is used for catch-up after a crash/restart.

// HandleSyncLog processes an incoming SyncLog RPC from the Leader.
// This is used when a Follower is too far behind to use normal AppendEntries.
//
// The Leader sends all committed entries starting from fromIndex.
// The Follower replaces its log with the received entries.
func (n *Node) HandleSyncLog(req SyncLogRequest) SyncLogResponse {
	n.Mu.Lock()
	defer n.Mu.Unlock()

	response := SyncLogResponse{
		Success:    false,
		SyncedUpTo: 0,
	}

	// Clear the log if starting from index 0
	if req.FromIndex == 0 {
		n.Log.Mu.Lock()
		n.Log.Entries = make([]LogEntry, 0)
		n.Log.Mu.Unlock()
	}

	// Append all received entries
	for _, entry := range req.Entries {
		n.Log.Append(entry)
	}

	// Update commit index to the last received entry
	if len(req.Entries) > 0 {
		lastEntry := req.Entries[len(req.Entries)-1]
		n.CommitIndex = lastEntry.Index
		response.SyncedUpTo = lastEntry.Index
	}

	response.Success = true

	n.Logger.Printf("term=%d state=%s event=sync_log_received entriesCount=%d syncedUpTo=%d",
		n.CurrentTerm, n.State.String(), len(req.Entries), response.SyncedUpTo)

	return response
}

// =============================================================================
// LEADER: REPLICATE ENTRY TO FOLLOWERS
// =============================================================================
// When the Leader receives a new stroke from the Gateway, it Must replicate
// that stroke to all followers before committing it.

// ReplicateEntry adds a new stroke to the Leader's log and replicates it to all followers.
// This is called by the Gateway when a client draws a stroke.
//
// Returns:
//   - committed: True if the entry was committed (majority ACK)
//   - error: Non-nil if the node is not the Leader or replication failed
func (n *Node) ReplicateEntry(stroke Stroke) (bool, error) {
	n.Mu.Lock()

	// Only the Leader can accept new entries
	if n.State != Leader {
		n.Mu.Unlock()
		return false, fmt.Errorf("not the leader")
	}

	// Create a new log entry
	lastLogIndex, _ := n.Log.GetLastLogIndexAndTerm()
	newEntry := LogEntry{
		Index:  lastLogIndex + 1,
		Term:   n.CurrentTerm,
		Stroke: stroke,
	}

	// Append to our own log first
	n.Log.Append(newEntry)
	n.Logger.Printf("term=%d state=LEADER event=entry_appended index=%d", n.CurrentTerm, newEntry.Index)

	// Prepare replication state
	currentTerm := n.CurrentTerm
	leaderId := n.ID
	leaderCommit := n.CommitIndex
	peers := make([]string, len(n.PeerURLs))
	copy(peers, n.PeerURLs)

	// Update nextIndex for ourselves (we just appended)
	for _, peer := range peers {
		// Only update if LeaderState exists
		if n.LeaderState != nil {
			n.LeaderState.NextIndex[peer] = newEntry.Index + 1
		}
	}

	n.Mu.Unlock()

	// Replicate to all peers in parallel
	var wg sync.WaitGroup
	var successMu sync.Mutex
	successCount := 1 // We count ourselves as a success

	for _, peerURL := range peers {
		wg.Add(1)
		go func(url string) {
			defer wg.Done()

			// Get the prevLogIndex and prevLogTerm for this peer
			n.Mu.Lock()
			prevLogIndex := newEntry.Index - 1
			prevLogTerm := 0
			if prevLogIndex > 0 && prevLogIndex <= len(n.Log.Entries) {
				prevLogTerm = n.Log.Entries[prevLogIndex-1].Term
			}
			n.Mu.Unlock()

			req := AppendEntriesRequest{
				Term:         currentTerm,
				LeaderId:     leaderId,
				PrevLogIndex: prevLogIndex,
				PrevLogTerm:  prevLogTerm,
				Entries:      []LogEntry{newEntry},
				LeaderCommit: leaderCommit,
			}

			resp, err := n.sendAppendEntries(url, req)
			if err != nil {
				n.Logger.Printf("term=%d state=LEADER event=replication_failed peer=%s error=%v",
					currentTerm, url, err)
				return
			}

			// Check for higher term
			n.Mu.Lock()
			if resp.Term > n.CurrentTerm {
				n.BecomeFollower(resp.Term)
				n.Mu.Unlock()
				return
			}
			n.Mu.Unlock()

			if resp.Success {
				successMu.Lock()
				successCount++
				successMu.Unlock()

				// Update matchIndex for this peer
				n.Mu.Lock()
				if n.LeaderState != nil {
					n.LeaderState.MatchIndex[url] = newEntry.Index
				}
				n.Mu.Unlock()
			} else {
				// AppendEntries failed - peer's log is inconsistent
				// We need to trigger a sync-log for this peer
				n.Logger.Printf("term=%d state=LEADER event=replication_rejected peer=%s logLength=%d",
					currentTerm, url, resp.LogLength)

				// Attempt to sync the follower's log
				go n.syncFollowerLog(url, currentTerm)
			}
		}(peerURL)
	}

	wg.Wait()

	// Check if we achieved majority
	majority := (len(peers)+1)/2 + 1
	committed := successCount >= majority

	if committed {
		// Update commit index
		n.Mu.Lock()
		if n.State == Leader && n.CurrentTerm == currentTerm {
			n.CommitIndex = newEntry.Index
			n.Logger.Printf("term=%d state=LEADER event=entry_committed index=%d", currentTerm, newEntry.Index)
		}
		n.Mu.Unlock()
	}

	return committed, nil
}

// syncFollowerLog sends a SyncLog RPC to bring a follower up to date.
// This is called when AppendEntries fails due to log inconsistency.
func (n *Node) syncFollowerLog(peerURL string, leaderTerm int) {
	n.Mu.Lock()
	if n.State != Leader || n.CurrentTerm != leaderTerm {
		n.Mu.Unlock()
		return
	}

	// Prepare all committed entries to send
	entries := make([]LogEntry, len(n.Log.Entries))
	copy(entries, n.Log.Entries)
	n.Mu.Unlock()

	req := SyncLogRequest{
		FromIndex: 0,
		Entries:   entries,
	}

	resp, err := n.sendSyncLog(peerURL, req)
	if err != nil {
		n.Logger.Printf("term=%d state=LEADER event=sync_log_failed peer=%s error=%v",
			leaderTerm, peerURL, err)
		return
	}

	if resp.Success {
		n.Logger.Printf("term=%d state=LEADER event=sync_log_sent peer=%s syncedUpTo=%d",
			leaderTerm, peerURL, resp.SyncedUpTo)

		// Update matchIndex for this peer
		n.Mu.Lock()
		if n.LeaderState != nil {
			n.LeaderState.MatchIndex[peerURL] = resp.SyncedUpTo
			n.LeaderState.NextIndex[peerURL] = resp.SyncedUpTo + 1
		}
		n.Mu.Unlock()
	}
}

// =============================================================================
// NETWORK HELPERS
// =============================================================================

// sendHeartbeat sends a Heartbeat RPC to a peer via HTTP POST.
func (n *Node) sendHeartbeat(peerURL string, req HeartbeatRequest) (*HeartbeatResponse, error) {
	body, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal request: %w", err)
	}

	client := &http.Client{Timeout: 100 * time.Millisecond}
	url := peerURL + "/heartbeat"
	resp, err := client.Post(url, "application/json", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("failed to send request to %s: %w", url, err)
	}
	defer resp.Body.Close()

	var response HeartbeatResponse
	if err := json.NewDecoder(resp.Body).Decode(&response); err != nil {
		return nil, fmt.Errorf("failed to decode response: %w", err)
	}

	return &response, nil
}

// sendAppendEntries sends an AppendEntries RPC to a peer via HTTP POST.
func (n *Node) sendAppendEntries(peerURL string, req AppendEntriesRequest) (*AppendEntriesResponse, error) {
	body, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal request: %w", err)
	}

	client := &http.Client{Timeout: 500 * time.Millisecond}
	url := peerURL + "/append-entries"
	resp, err := client.Post(url, "application/json", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("failed to send request to %s: %w", url, err)
	}
	defer resp.Body.Close()

	var response AppendEntriesResponse
	if err := json.NewDecoder(resp.Body).Decode(&response); err != nil {
		return nil, fmt.Errorf("failed to decode response: %w", err)
	}

	return &response, nil
}

// sendSyncLog sends a SyncLog RPC to a peer via HTTP POST.
func (n *Node) sendSyncLog(peerURL string, req SyncLogRequest) (*SyncLogResponse, error) {
	body, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal request: %w", err)
	}

	client := &http.Client{Timeout: 5 * time.Second} // Longer timeout for potentially large syncs
	url := peerURL + "/sync-log"
	resp, err := client.Post(url, "application/json", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("failed to send request to %s: %w", url, err)
	}
	defer resp.Body.Close()

	var response SyncLogResponse
	if err := json.NewDecoder(resp.Body).Decode(&response); err != nil {
		return nil, fmt.Errorf("failed to decode response: %w", err)
	}

	return &response, nil
}
