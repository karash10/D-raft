# Distributed Real-Time Drawing Board with Mini-RAFT Consensus

> A fault-tolerant, collaborative whiteboard backed by a 3-replica RAFT consensus cluster — built with TypeScript (Gateway + Frontend) and Go (Replicas).

---

## Table of Contents

- [Overview](#overview)
- [Architecture](#architecture)
- [Tech Stack](#tech-stack)
- [Project Structure](#project-structure)
- [RAFT Protocol Specification](#raft-protocol-specification)
- [API Reference](#api-reference)
- [Getting Started](#getting-started)
- [Development Workflow](#development-workflow)
- [Testing & Fault Injection](#testing--fault-injection)
- [Docker & Deployment](#docker--deployment)
- [Environment Variables](#environment-variables)
- [Week-by-Week Milestones](#week-by-week-milestones)
- [Submission Checklist](#submission-checklist)
- [Bonus Challenges](#bonus-challenges)
- [Team](#team)

---

## Overview

This project simulates a distributed system by implementing a collaborative drawing board where:

- Multiple browser clients draw simultaneously on a shared canvas
- A **Gateway** (TypeScript/bun) manages all WebSocket connections
- Three **Replica** nodes (Go) maintain a shared stroke log via a Mini-RAFT consensus protocol
- The system survives leader crashes, hot-reloads, and rolling replica restarts with **zero downtime**

This mirrors the architecture used in real-world systems:

| This Project | Real-World Equivalent |
|---|---|
| Replica cluster | etcd inside Kubernetes |
| Leader election | Kubernetes controller-manager |
| Gateway re-routing | AWS ALB with health-check failover |
| Hot-reload rolling restart | Blue-green / rolling deployment |
| Stroke log | Distributed append-only event log |

---

## Architecture

```
 Browser 1        Browser 2        Browser 3
    │                │                │
    └────────────────┴────────────────┘
                     │  WebSocket
                     ▼
           ┌──────────────────┐
           │     Gateway      │ 
           │    (Port 8080)   │  
           └─────────┬────────┘  
                     │  HTTP RPC
          ┌──────────┼─────────────┐
          ▼          ▼             ▼
    ┌──────────┐ ┌──────────┐ ┌──────────┐
    │Replica 1 │ │Replica 2 │ │Replica 3 │
    │  LEADER  │ │ Follower │ │ Follower │
    │  :9001   │ │  :9002   │ │  :9003   │
    └──────────┘ └──────────┘ └──────────┘
         Go · Fiber · air (hot-reload)
         ◄──────────────────────────────►
              Shared Docker network
```

**Flow for a single stroke:**

```
Client draws → Gateway → Leader.AppendEntries
                             │
                    ┌────────┴────────┐
                    ▼                 ▼
             Replica 2 ACK     Replica 3 ACK
                    │
             Majority (2/3) reached
                    │
       Leader commits → Gateway → Broadcast → All clients
```

---

## Tech Stack

| Layer | Language | Framework / Libraries |
|---|---|---|
| Frontend | TypeScript | Next.js · Tailwind CSS · HTML5 Canvas API · native WebSocket |
| Gateway | TypeScript (Bun) | Bun · Hono · `ws` (WebSocket) · gRPC · Zod · Pino |
| Replicas | Go | Fiber v2 · `air` (hot-reload) · standard library |
| Containerization | — | Docker · docker-compose v3.8 |
| Testing | — | `k6` (load) · `curl` / shell scripts (fault injection) |

---

## Project Structure

```
.
├── docker-compose.yml
├── README.md
│
├── frontend/                   # TypeScript · Next.js
│   ├── src/
│   │   ├── canvas.ts           # Drawing logic, stroke serialization
│   │   ├── socket.ts           # WebSocket client, reconnect logic
│   │   └── main.ts             # Entry point
│   ├── index.html
│   ├── package.json
│   └── tsconfig.json
│
├── gateway/                    # TypeScript · Bun + Hono
│   ├── index.ts                # Hono + ws WebSocket server
│   ├── src/
│   │   ├── leaderRouter.ts     # Tracks current leader, re-routes on failover (gRPC)
│   │   └── broadcaster.ts      # Pushes committed strokes to all clients
│   ├── package.json
│   └── tsconfig.json
│
├── replica1/                   # Go · Fiber
│   ├── main.go
│   ├── raft/
│   │   ├── node.go             # State machine: Follower / Candidate / Leader
│   │   ├── election.go         # RequestVote logic, election timeout
│   │   ├── log.go              # Append-only stroke log, commit index
│   │   └── replication.go      # AppendEntries, heartbeat, sync-log
│   ├── handlers/
│   │   └── rpc.go              # HTTP handlers for all RPC endpoints
│   ├── go.mod
│   └── .air.toml               # air hot-reload config
│
├── replica2/                   # Same structure as replica1
│   └── ...
│
├── replica3/                   # Same structure as replica1
│   └── ...
│
└── logs/                       # Captured failover event logs (for submission)
    ├── election-demo.log
    ├── failover-demo.log
    └── sync-log-demo.log
```

---

## RAFT Protocol Specification

### Node States

![](./assets/state_transition.png)

| State | Behavior |
|---|---|
| **Follower** | Waits for heartbeats. Votes for candidates. Appends log entries from leader. |
| **Candidate** | Increments term, votes for self, sends `RequestVote` to all peers. |
| **Leader** | Sends heartbeats every **150ms**. Replicates log entries. Commits on majority ACK. |

### RAFT uses majority quorum in two separate places:
| Where | Why majority is needed |
|---|---|
|Election|A candidate needs ≥2/3 votes to become leader — prevents two leaders at the same time
|Log commit|A leader needs ≥2/3 ACKs before marking a stroke as committed — prevents data loss if the leader crashes right after writing

### Timing

| Parameter | Value |
|---|---|
| Heartbeat interval | 150 ms |
| Election timeout | Random 500–800 ms (per node, re-randomized each election) |
| Majority quorum | ≥ 2 out of 3 nodes |

### Safety Rules

1. **Committed entries are never overwritten.**
2. A node seeing a higher term immediately reverts to Follower.
3. Split votes trigger a new election after another random timeout.
4. A restarted node always starts as Follower with an empty log and catches up via `/sync-log`.

### Catch-Up Protocol (Restarted Node)

```
Restarted node (empty log, term=0)
    │
    │ receives AppendEntries from leader
    │ prevLogIndex check FAILS
    │
    ▼
Node responds with { success: false, logLength: 0 }
    │
    ▼
Leader calls POST /sync-log on follower
    sending all committed entries from index 0 onward
    │
    ▼
Follower appends all entries, updates commitIndex
    │
    ▼
Follower participates normally in future AppendEntries
```

---

## API Reference

All replica RPC endpoints are accessible via HTTP/JSON and gRPC. The Gateway calls these internally via gRPC.

### `POST /request-vote`

Called by a Candidate during election.

**Request:**
```json
{
  "term": 3,
  "candidateId": "replica2",
  "lastLogIndex": 12,
  "lastLogTerm": 2
}
```

**Response:**
```json
{
  "term": 3,
  "voteGranted": true
}
```

---

### `POST /append-entries`

Called by the Leader to replicate a log entry (or as a heartbeat when `entries` is empty).

**Request:**
```json
{
  "term": 3,
  "leaderId": "replica1",
  "prevLogIndex": 11,
  "prevLogTerm": 2,
  "entries": [
    {
      "index": 12,
      "term": 3,
      "stroke": {
        "x0": 100, "y0": 200,
        "x1": 150, "y1": 250,
        "color": "#e63946",
        "width": 3
      }
    }
  ],
  "leaderCommit": 11
}
```

**Response:**
```json
{
  "term": 3,
  "success": true,
  "logLength": 12
}
```

---

### `POST /heartbeat`

Lightweight keep-alive from Leader to Followers (no log entries).

**Request:**
```json
{
  "term": 3,
  "leaderId": "replica1"
}
```

**Response:**
```json
{
  "term": 3,
  "success": true
}
```

---

### `POST /sync-log`

Called by the Leader on a rejoining Follower to send all missing committed entries.

**Request:**
```json
{
  "fromIndex": 0,
  "entries": [ "...all committed log entries from index 0 onward..." ]
}
```

**Response:**
```json
{
  "success": true,
  "syncedUpTo": 47
}
```

---

### `GET /status`

Returns current node state. Used by the Gateway to discover the active leader.

**Response:**
```json
{
  "replicaId": "replica1",
  "state": "leader",
  "term": 3,
  "commitIndex": 47,
  "logLength": 48
}
```

---

## Getting Started

### Prerequisites

- [Docker](https://docs.docker.com/get-docker/) and [docker-compose](https://docs.docker.com/compose/)
- Node.js ≥ 18 (for local Gateway/Frontend development)
- Go ≥ 1.21 (for local Replica development)

### Run the full cluster

```bash
# Clone the repo
git clone https://github.com/jeevan4476/MiniRaft
cd MiniRaft

# Start everything
docker-compose up --build

# Open the drawing board
open http://localhost:3000
```

### Run services individually (for development)

```bash
# Frontend (Vite dev server)
cd frontend && bun install && bun run dev

# Gateway
cd gateway && bun install && bun run index.ts

# Replica 1
cd replica1 && go mod download && air
```

---

## Development Workflow

### Hot-Reload (Replicas)

Each replica uses [`air`](https://github.com/air-verse/air) for live-reload. Edit any `.go` file inside `replica1/`, `replica2/`, or `replica3/` and the container automatically:

1. Detects the file change (via bind mount)
2. Gracefully shuts down the old instance
3. Recompiles and restarts
4. New instance joins the cluster as a Follower
5. RAFT election runs — system stays live

```bash
# Watch a replica's logs while hot-reloading
docker-compose logs -f replica1
```

### Hot-Reload (Gateway)

The Gateway uses `bun --watch`. Same bind-mount behavior.

### Observability

All replicas log key events to stdout in structured format:

```
[replica1] term=3 state=LEADER    event=heartbeat_sent      peers=2
[replica1] term=3 state=LEADER    event=entry_committed     index=47
[replica2] term=3 state=FOLLOWER  event=vote_granted        for=replica1
[replica3] term=4 state=CANDIDATE event=election_started    timeout=612ms
[replica3] term=4 state=LEADER    event=election_won        votes=2
```

---

## Testing & Fault Injection

### Kill the leader

```bash
docker-compose stop replica1

# Watch replica2 or replica3 win the election
docker-compose logs -f replica2 replica3

# Bring it back — it should catch up automatically
docker-compose start replica1
```

### Trigger a hot-reload

```bash
# Touch any Go file inside replica2 to trigger air reload
touch replica2/raft/node.go
docker-compose logs -f replica2
```

### Stress test with multiple clients

```bash
# Open 5 browser tabs at http://localhost:3000 and draw simultaneously
# Or use k6:
k6 run tests/load-test.js
```

### Verify canvas consistency after failover

1. Open two browser tabs at `http://localhost:3000`
2. Draw several strokes in tab 1
3. Run `docker-compose stop replica1` to kill the leader
4. Immediately draw more strokes in tab 2
5. Confirm both tabs show identical canvas state after failover completes (~1–2 seconds)

---

## Docker & Deployment

### `docker-compose.yml` overview

```yaml
version: "3.8"

services:
  frontend:
    build: ./frontend
    ports: ["3000:3000"]
    volumes: ["./frontend/src:/app/src"]

  gateway:
    build: ./gateway
    ports: ["8080:8080"]
    volumes: ["./gateway/src:/app/src"]
    environment:
      - REPLICA_URLS=http://replica1:9001,http://replica2:9002,http://replica3:9003
    depends_on: [replica1, replica2, replica3]

  replica1:
    build: ./replica1
    ports: ["9001:9001"]
    volumes: ["./replica1:/app"]
    environment:
      - REPLICA_ID=replica1
      - PORT=9001
      - PEERS=http://replica2:9002,http://replica3:9003

  replica2:
    build: ./replica2
    ports: ["9002:9002"]
    volumes: ["./replica2:/app"]
    environment:
      - REPLICA_ID=replica2
      - PORT=9002
      - PEERS=http://replica1:9001,http://replica3:9003

  replica3:
    build: ./replica3
    ports: ["9003:9003"]
    volumes: ["./replica3:/app"]
    environment:
      - REPLICA_ID=replica3
      - PORT=9003
      - PEERS=http://replica1:9001,http://replica2:9002

networks:
  default:
    name: miniraft-network
```

---

## Environment Variables

| Variable | Service | Description |
|---|---|---|
| `REPLICA_ID` | Replica | Unique node identifier (`replica1`, `replica2`, `replica3`) |
| `PORT` | Replica | HTTP port for RPC endpoints |
| `PEERS` | Replica | Comma-separated URLs of all other replicas |
| `REPLICA_URLS` | Gateway | Comma-separated URLs of all replicas (for leader discovery) |
| `GATEWAY_PORT` | Gateway | WebSocket + HTTP port (default: `8080`) |
| `ELECTION_TIMEOUT_MIN` | Replica | Minimum election timeout in ms (default: `500`) |
| `ELECTION_TIMEOUT_MAX` | Replica | Maximum election timeout in ms (default: `800`) |
| `HEARTBEAT_INTERVAL` | Replica | Leader heartbeat interval in ms (default: `150`) |

---

## Week-by-Week Milestones

### Week 1 — Design & Architecture

- [ ] System diagram
- [ ] RAFT state transition diagram
- [ ] API spec for all 4 RPCs (VoteRequest, AppendEntries, Heartbeat, SyncLog)
- [ ] `docker-compose.yml` draft
- [ ] Failure scenario list with expected system behavior per scenario

### Week 2 — Core Implementation

- [ ] Go: RAFT state machine (Follower / Candidate / Leader)
- [ ] Go: `RequestVote` + `AppendEntries` + `Heartbeat` handlers
- [ ] TypeScript: Gateway WebSocket server with leader routing
- [ ] TypeScript: Frontend canvas with stroke serialization over WebSocket
- [ ] End-to-end: single client draws, stroke appears on all connected tabs

### Week 3 — Reliability & Zero-Downtime

- [ ] Go: Catch-up sync (`/sync-log`) for restarted replicas
- [ ] TypeScript: Gateway graceful failover (no client disconnects on leader change)
- [ ] Docker: Hot-reload triggers RAFT election, system stays live throughout
- [ ] Demo: kill leader mid-draw, canvas remains consistent across all clients
- [ ] Logs: captured election, failover, and sync-log events saved in `/logs`

---

## Submission Checklist

- [ ] Source code in `/gateway`, `/replica1`, `/replica2`, `/replica3`, `/frontend`
- [ ] `docker-compose.yml` — full cluster starts with `docker-compose up --build`
- [ ] `/logs` — at least 3 captured failover event logs
- [ ] Architecture document (2–3 pages): cluster diagram, state transitions, API definition, failure-handling design
- [ ] Demo video (8–10 min):
  - [ ] Multiple clients drawing simultaneously
  - [ ] Leader killed → automatic failover shown
  - [ ] Hot-reload of a replica → system stays live
  - [ ] Canvas state consistent after restarts
  - [ ] System under chaotic conditions (multiple rapid failures)

---

## Bonus Challenges

- [ ] Simulate a network partition (split-brain scenario with iptables rules)
- [ ] Add a 4th replica dynamically without downtime
- [ ] Implement undo/redo using log compensation entries
- [ ] Build a live dashboard showing leader, term, and log size per replica
- [ ] Deploy to AWS EC2 or Google Cloud VM

---

## References

- [The RAFT Paper — Ongaro & Ousterhout (2014)](https://raft.github.io/raft.pdf)
- [RAFT Visualization](https://raft.github.io/)
- [Go Fiber Docs](https://docs.gofiber.io/)
- [air — Go live-reload](https://github.com/air-verse/air)
- [Bun — Fast all-in-one JavaScript runtime](https://bun.sh/)
- [Hono — Ultrafast web framework](https://hono.dev/)
- [Next.js — React Framework](https://nextjs.org/)

---

## Team

| Member | Primary Responsibility |
|---|---|
| TBD | Go Replicas — RAFT state machine & RPC handlers |
| TBD | TypeScript (Bun) Gateway — WebSocket server & leader routing via gRPC |
| TBD | TypeScript (Next.js) Frontend — Canvas, stroke serialization |
| TBD | DevOps — Docker, docker-compose, hot-reload, integration |
