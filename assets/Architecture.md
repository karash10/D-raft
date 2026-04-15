# Mini-RAFT Architecture Document

## 1. System Overview

This project implements a distributed real-time drawing board with a three-node Mini-RAFT cluster, a gateway, and a browser frontend.

Main components:

- `frontend/`: Next.js drawing UI using an HTML canvas and a WebSocket client.
- `gateway/`: Bun + Hono WebSocket service that accepts browser connections, tracks the current leader, forwards strokes to that leader, and broadcasts committed strokes.
- `replica1/`, `replica2/`, `replica3/`: Go replica nodes that run leader election, heartbeats, log replication, commit tracking, and follower catch-up.

The system is designed so that browsers keep a stable WebSocket connection to the gateway while leadership changes happen behind the gateway.

## 2. Cluster Diagram

```text
Browser Tabs / Clients
         |
         | WebSocket
         v
   +-------------+
   |   Gateway   |
   |   :8080     |
   +------+------+ 
          |
          | HTTP/JSON RPC
          |
   +------+------+------+
   |             |      |
   v             v      v
+------+      +------+ +------+
| R1   |      | R2   | | R3   |
| :9001|      | :9002| | :9003|
+------+      +------+ +------+
```

Replica state roles:

- Follower: waits for heartbeats, votes in elections, appends entries from the leader.
- Candidate: starts an election after timeout, increments term, requests votes.
- Leader: sends heartbeats every 150 ms, accepts new strokes, replicates entries, and advances commit index when a majority confirms.

## 3. Request Flow

### 3.1 Drawing flow

1. A user draws on the canvas.
2. The frontend renders the segment immediately for optimistic UI.
3. The frontend sends the stroke to the gateway over WebSocket.
4. The gateway forwards the stroke to the current leader using `POST /stroke`.
5. The leader appends the stroke to its local log.
6. The leader sends `AppendEntries` to followers.
7. Once a majority acknowledges the entry, the leader marks it committed.
8. The gateway broadcasts the committed stroke to all other connected clients.

### 3.2 New client sync

1. A browser connects to the gateway.
2. The gateway fetches the current log from the active leader through `GET /log`.
3. The gateway sends the history snapshot to the client.
4. The frontend replaces local canvas history with the committed cluster snapshot and redraws.

## 4. Mini-RAFT Design

### 4.1 Election design

- Election timeout is randomized between 500 and 800 ms.
- Each node starts as a follower.
- If a follower stops receiving valid heartbeats in time, it becomes a candidate.
- The candidate increments its term, votes for itself, and sends `RequestVote` to peers.
- Majority quorum for a 3-node cluster is 2 votes.
- If a node discovers a higher term, it immediately steps down to follower.

### 4.2 Replication design

- The leader appends each new stroke as a `LogEntry`.
- Followers validate `prevLogIndex` and `prevLogTerm` before accepting new entries.
- Followers reject stale terms and inconsistent logs.
- The leader commits an entry only after majority acknowledgment.
- Followers update their commit index from the leader’s commit index.

### 4.3 Catch-up design

When a follower restarts with an empty or stale log:

1. It starts in follower mode.
2. A normal append may fail due to missing previous entries.
3. The leader sends `POST /sync-log`.
4. Only committed entries up to the leader’s `CommitIndex` are sent.
5. The follower replaces its local log with that committed suffix and resumes normal participation.

This protects the system from copying entries that were appended locally by a leader but never committed by a majority.

## 5. API Definition

Replica RPC endpoints:

- `POST /request-vote`
- `POST /append-entries`
- `POST /heartbeat`
- `POST /sync-log`
- `POST /stroke`
- `GET /status`
- `GET /log`
- `GET /health`

Gateway endpoints:

- `GET /ws`
- `GET /status`
- `GET /cluster-status`

## 6. Failure Handling

### 6.1 Leader crash

- Followers stop receiving heartbeats.
- One follower becomes candidate after timeout.
- A majority elects a new leader.
- The gateway refreshes leader information frequently and also refreshes immediately after a failed stroke forward.
- The gateway retries once against the newly discovered leader so transient failovers do not drop strokes as easily.

### 6.2 Follower restart / hot reload

- The restarted node rejoins as follower.
- It serves traffic only after its HTTP server becomes healthy again.
- If its log is behind, the leader uses `/sync-log` to bring it up to the committed state.

### 6.3 Client reconnect

- The frontend reconnects the WebSocket automatically with backoff.
- Once connected, the gateway sends history again.
- The frontend replaces local stroke history with the latest committed snapshot to avoid duplicate redraws after reconnect.

## 7. Observability and Demo Support

The codebase includes:

- structured replica logs for elections, heartbeats, replication, and commits
- a live dashboard showing leader, node state, term, and log length
- a `logs/` directory for storing captured demo outputs required for submission
