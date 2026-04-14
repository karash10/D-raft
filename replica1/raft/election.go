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
// REQUEST VOTE RPC STRUCTURES
// =============================================================================
// These structs define the JSON payloads for the RequestVote RPC.
// A Candidate sends RequestVoteRequest to all peers, and each peer responds
// with RequestVoteResponse.

// RequestVoteRequest is sent by a Candidate to request a vote from a peer.
// According to RAFT:
//   - Term: The candidate's current term (Must be >= receiver's term to be considered)
//   - CandidateId: The unique ID of the candidate requesting the vote
//   - LastLogIndex: Index of the candidate's last log entry (for log comparison)
//   - LastLogTerm: Term of the candidate's last log entry (for log comparison)
type RequestVoteRequest struct {
	Term         int    `json:"term"`
	CandidateId  string `json:"candidateId"`
	LastLogIndex int    `json:"lastLogIndex"`
	LastLogTerm  int    `json:"lastLogTerm"`
}

// RequestVoteResponse is returned by a peer in response to a vote request.
//   - Term: The responder's current term (candidate Must step down if this is higher)
//   - VoteGranted: True if the responder voted for this candidate
type RequestVoteResponse struct {
	Term        int  `json:"term"`
	VoteGranted bool `json:"voteGranted"`
}

// =============================================================================
// ELECTION TIMEOUT LOOP
// =============================================================================
// This is the background goroutine that runs continuously on every node.
// It monitors the election timer and triggers elections when the timer expires.

// RunElectionTimer is meant to be started as a goroutine.
// It continuously monitors whether the node should start an election.
//
// The loop works as follows:
//  1. Wait for the election timer to fire (500-800ms randomized)
//  2. When it fires, check if we're still a Follower or Candidate
//  3. If we're a Leader, we don't need to do anything (we send heartbeats instead)
//  4. If we're a Follower/Candidate, start a new election
//  5. Reset the timer with a new random duration and repeat

// CRITICAL: This function Must be run as a goroutine: `go node.RunElectionTimer()`
func (n *Node) RunElectionTimer() {
	for {
		// Block until the election timer fires
		<-n.electionTimer.C
		n.Mu.Lock()
		// Only Followers and Candidates should start elections.
		// Leaders maintain their authority by sending heartbeats, so they never
		// need to trigger an election for themselves.
		if n.State == Leader {
			// Leader doesn't need election timer, but we still reset it
			// in case we step down later
			n.electionTimer.Reset(n.RandomElectionTimeout())
			n.Mu.Unlock()
			continue
		}

		// If we reach here, we're a Follower or Candidate whose timer expired.
		// This means we haven't heard from a Leader in time, so we start an election.
		n.Logger.Printf("term=%d state=%s event=election_timeout", n.CurrentTerm, n.State)
		n.Mu.Unlock()

		// Start the election process (this will increment term and send RequestVote RPCs)
		n.StartElection()
	}
}

// =============================================================================
// START ELECTION
// =============================================================================
// This function is called when the election timer expires.
// It transitions the node to Candidate state and requests votes from all peers.

// StartElection transitions the node to Candidate and requests votes from all peers.
//
// According to RAFT, when starting an election:
//  1. Increment currentTerm
//  2. Vote for self
//  3. Reset election timer (with new random timeout)
//  4. Send RequestVote RPCs to all other servers
//  5. If votes received from majority: become Leader
//  6. If AppendEntries RPC received from new Leader: revert to Follower
//  7. If election timeout elapses: start new election
func (n *Node) StartElection() {
	n.Mu.Lock()

	// Step 1: Increment term and transition to Candidate
	n.CurrentTerm++
	n.State = Candidate
	n.VotedFor = n.ID // Vote for ourselves
	currentTerm := n.CurrentTerm
	candidateId := n.ID

	// Get our log info for the vote request (peers compare logs to decide)
	lastLogIndex, lastLogTerm := n.Log.GetLastLogIndexAndTerm()

	// Reset the election timer with a new random timeout
	// This is important: if this election fails (split vote), we'll timeout again
	// and start a new election with a fresh random delay
	n.electionTimer.Reset(n.RandomElectionTimeout())

	// Copy peer URLs so we can release the lock before making HTTP calls
	peers := make([]string, len(n.PeerURLs))
	copy(peers, n.PeerURLs)

	n.Logger.Printf("term=%d state=CANDIDATE event=election_started lastLogIndex=%d lastLogTerm=%d",
		currentTerm, lastLogIndex, lastLogTerm)

	n.Mu.Unlock()

	// Step 2: Send RequestVote RPCs to all peers in parallel
	// We use a WaitGroup to wait for all responses (or timeouts)
	var wg sync.WaitGroup
	var votesMu sync.Mutex
	votesReceived := 1 // Start with 1 because we voted for ourselves

	for _, peerURL := range peers {
		wg.Add(1)
		go func(url string) {
			defer wg.Done()

			// Prepare the RequestVote request
			req := RequestVoteRequest{
				Term:         currentTerm,
				CandidateId:  candidateId,
				LastLogIndex: lastLogIndex,
				LastLogTerm:  lastLogTerm,
			}

			// Send the request and get the response
			resp, err := n.sendRequestVote(url, req)
			if err != nil {
				// Network error or timeout - peer is unreachable
				n.Logger.Printf("term=%d state=CANDIDATE event=vote_request_failed peer=%s error=%v",
					currentTerm, url, err)
				return
			}

			// Check if the peer's term is higher than ours
			// If so, we Must immediately step down to Follower
			n.Mu.Lock()
			if resp.Term > n.CurrentTerm {
				n.Logger.Printf("term=%d state=CANDIDATE event=higher_term_discovered newTerm=%d",
					n.CurrentTerm, resp.Term)
				n.BecomeFollower(resp.Term)
				n.Mu.Unlock()
				return
			}

			// Only count the vote if we're still a Candidate in the same term
			// (We might have already won, lost, or stepped down)
			if n.State != Candidate || n.CurrentTerm != currentTerm {
				n.Mu.Unlock()
				return
			}
			n.Mu.Unlock()

			// Count the vote if granted
			if resp.VoteGranted {
				votesMu.Lock()
				votesReceived++
				currentVotes := votesReceived
				votesMu.Unlock()

				n.Logger.Printf("term=%d state=CANDIDATE event=vote_granted from=%s votes=%d",
					currentTerm, url, currentVotes)

				// Check if we've reached majority (2 out of 3 nodes)
				// Majority = (total nodes / 2) + 1 = (3 / 2) + 1 = 2
				majority := (len(peers)+1)/2 + 1
				if currentVotes >= majority {
					n.Mu.Lock()
					// Double-check we're still a Candidate in the same term
					if n.State == Candidate && n.CurrentTerm == currentTerm {
						n.BecomeLeader()
					}
					n.Mu.Unlock()
				}
			} else {
				n.Logger.Printf("term=%d state=CANDIDATE event=vote_denied from=%s", currentTerm, url)
			}
		}(peerURL)
	}

	// Wait for all vote requests to complete (or timeout)
	wg.Wait()
}

// =============================================================================
// BECOME LEADER
// =============================================================================
// Called when a Candidate receives votes from a majority of nodes.

// BecomeLeader transitions the node from Candidate to Leader.
// This is called when the node receives votes from a majority of peers.
//
// As Leader, the node will:
//  1. Stop participating in elections (reset election timer but ignore it)
//  2. Initialize nextIndex and matchIndex for each follower
//  3. Start sending periodic heartbeats to maintain authority
//
// IMPORTANT: The caller Must hold n.Mu.Lock() before calling this function.
func (n *Node) BecomeLeader() {
	n.State = Leader

	// Initialize leader-specific state for tracking follower progress
	// nextIndex[peer]: Index of the next log entry to send to that peer
	// matchIndex[peer]: Index of highest log entry known to be replicated on that peer
	// These are initialized in the Node struct and managed by replication.go

	n.Logger.Printf("term=%d state=LEADER event=election_won", n.CurrentTerm)

	// Start the heartbeat loop in a separate goroutine
	// This will send empty AppendEntries RPCs to all peers every 150ms
	go n.RunHeartbeatLoop()
}

// =============================================================================
// HANDLE REQUEST VOTE (Receiver Side)
// =============================================================================
// This is called when another node sends us a RequestVote RPC.

// HandleRequestVote processes an incoming RequestVote RPC from a Candidate.
// This implements the receiver side of the RequestVote RPC.
//
// According to RAFT, a node grants a vote if ALL of the following are true:
//  1. The candidate's term is >= our current term
//  2. We haven't voted for anyone else in this term (or we already voted for this candidate)
//  3. The candidate's log is at least as up-to-date as our log
//
// Log comparison (Section 5.4.1 of RAFT paper):
//   - If the logs have last entries with different terms, the log with the
//     later term is more up-to-date.
//   - If the logs end with the same term, the longer log is more up-to-date.
func (n *Node) HandleRequestVote(req RequestVoteRequest) RequestVoteResponse {
	n.Mu.Lock()
	defer n.Mu.Unlock()

	response := RequestVoteResponse{
		Term:        n.CurrentTerm,
		VoteGranted: false,
	}

	// Rule 1: If the candidate's term is less than ours, reject immediately
	// A candidate from an old term cannot become leader
	if req.Term < n.CurrentTerm {
		n.Logger.Printf("term=%d state=%s event=vote_denied reason=stale_term candidateTerm=%d candidate=%s",
			n.CurrentTerm, n.State.String(), req.Term, req.CandidateId)
		return response
	}

	// Rule 2: If the candidate's term is greater than ours, we Must step down
	// and update our term (this applies to Leaders and Candidates too)
	if req.Term > n.CurrentTerm {
		n.Logger.Printf("term=%d state=%s event=higher_term_discovered newTerm=%d",
			n.CurrentTerm, n.State.String(), req.Term)
		n.BecomeFollower(req.Term)
		response.Term = n.CurrentTerm
	}

	// Rule 3: Check if we can vote for this candidate
	// We can only vote if:
	//   - We haven't voted yet in this term (VotedFor == ""), OR
	//   - We already voted for this same candidate (VotedFor == req.CandidateId)
	canVote := n.VotedFor == "" || n.VotedFor == req.CandidateId

	if !canVote {
		n.Logger.Printf("term=%d state=%s event=vote_denied reason=already_voted votedFor=%s candidate=%s",
			n.CurrentTerm, n.State.String(), n.VotedFor, req.CandidateId)
		return response
	}

	// Rule 4: Check if the candidate's log is at least as up-to-date as ours
	// This is the "election restriction" that ensures only nodes with complete logs
	// can become leader
	ourLastLogIndex, ourLastLogTerm := n.Log.GetLastLogIndexAndTerm()
	candidateLogUpToDate := n.isLogUpToDate(req.LastLogTerm, req.LastLogIndex, ourLastLogTerm, ourLastLogIndex)

	if !candidateLogUpToDate {
		n.Logger.Printf("term=%d state=%s event=vote_denied reason=log_not_up_to_date candidate=%s "+
			"candidateLastLogTerm=%d candidateLastLogIndex=%d ourLastLogTerm=%d ourLastLogIndex=%d",
			n.CurrentTerm, n.State.String(), req.CandidateId,
			req.LastLogTerm, req.LastLogIndex, ourLastLogTerm, ourLastLogIndex)
		return response
	}

	// All checks passed - grant the vote!
	n.VotedFor = req.CandidateId
	response.VoteGranted = true

	// Reset election timer because we just heard from a valid candidate
	// This prevents us from starting our own election while this candidate is active
	n.electionTimer.Reset(n.RandomElectionTimeout())
	n.lastHeartbeat = time.Now()

	n.Logger.Printf("term=%d state=%s event=vote_granted for=%s",
		n.CurrentTerm, n.State.String(), req.CandidateId)

	return response
}

// isLogUpToDate determines if a candidate's log is at least as up-to-date as ours.
// This implements Section 5.4.1 of the RAFT paper:
//   - If the logs have last entries with different terms, the one with the
//     later (higher) term is more up-to-date.
//   - If the logs end with the same term, the longer log is more up-to-date.
//
// Parameters:
//   - candidateLastTerm: The term of the candidate's last log entry
//   - candidateLastIndex: The index of the candidate's last log entry
//   - ourLastTerm: The term of our last log entry
//   - ourLastIndex: The index of our last log entry
//
// Returns true if the candidate's log is at least as up-to-date as ours.
func (n *Node) isLogUpToDate(candidateLastTerm, candidateLastIndex, ourLastTerm, ourLastIndex int) bool {
	// If candidate has a higher last term, their log is more up-to-date
	if candidateLastTerm > ourLastTerm {
		return true
	}
	// If same term, longer log (higher index) is more up-to-date
	if candidateLastTerm == ourLastTerm && candidateLastIndex >= ourLastIndex {
		return true
	}
	// Otherwise, our log is more up-to-date
	return false
}

// =============================================================================
// NETWORK HELPER: SEND REQUEST VOTE
// =============================================================================
// This function sends an HTTP POST request to a peer's /request-vote endpoint.

// sendRequestVote sends a RequestVote RPC to a peer via HTTP POST.
// It handles JSON serialization, HTTP transport, and response parsing.
//
// The function uses a 200ms timeout for the HTTP request. If the peer is
// unreachable or slow, the function returns an error instead of blocking.
func (n *Node) sendRequestVote(peerURL string, req RequestVoteRequest) (*RequestVoteResponse, error) {
	// Serialize the request to JSON
	body, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal request: %w", err)
	}

	// Create HTTP client with timeout
	// We use a short timeout because elections should be fast
	// If a peer is slow, we don't want to wait forever
	client := &http.Client{
		Timeout: 200 * time.Millisecond,
	}

	// Send POST request to peer's /request-vote endpoint
	url := peerURL + "/request-vote"
	resp, err := client.Post(url, "application/json", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("failed to send request to %s: %w", url, err)
	}
	defer resp.Body.Close()

	// Parse the response
	var response RequestVoteResponse
	if err := json.NewDecoder(resp.Body).Decode(&response); err != nil {
		return nil, fmt.Errorf("failed to decode response from %s: %w", url, err)
	}

	return &response, nil
}

// =============================================================================
// RESET ELECTION TIMER
// =============================================================================
// Public method to reset the election timer from outside the election.go file.
// This is called when we receive a valid heartbeat from the Leader.

// ResetElectionTimer resets the election timer with a new random timeout.
// This should be called whenever we receive a valid heartbeat from the Leader,
// preventing us from starting an unnecessary election.
//
// IMPORTANT: The caller Must hold n.Mu.Lock() before calling this function.
func (n *Node) ResetElectionTimer() {
	// Stop the existing timer to avoid race conditions
	if !n.electionTimer.Stop() {
		// Drain the channel if the timer already fired
		select {
		case <-n.electionTimer.C:
		default:
		}
	}
	// Reset with a new random timeout
	r := n.RandomElectionTimeout()
	n.Logger.Print("Reseting Election timeout: ", r)
	n.electionTimer.Reset(r)
	n.lastHeartbeat = time.Now()
}
