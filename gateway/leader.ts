import type { NodeStatus } from "./types";

type Logger = Pick<Console, "log">;
type FetchImpl = typeof fetch;

export type LeaderSource = {
  getLeaderUrl(): string | null;
  getClusterStatus(): { peer: string; status: NodeStatus }[];
  refreshLeader(): Promise<string | null>;
};

type LeaderTrackerOptions = {
  peers: string[];
  fetchImpl?: FetchImpl;
  logger?: Logger;
  pollIntervalMs?: number;
  requestTimeoutMs?: number;
};

/**
 * LeaderTracker handles the background discovery process to find the active Go RAFT Leader.
 * It constantly polls all known replicas and checks their `GET /status` endpoint.
 * Because RAFT leadership can change at any time (e.g., if a node crashes), the Gateway Must
 * always know the `currentLeader` to determine where to safely forward WebSocket strokes.
 */
export class LeaderTracker implements LeaderSource {
  private leaderId: string | null = null;
  private leaderUrl: string | null = null;
  private term = 0;
  private clusterStatus: { peer: string; status: NodeStatus }[] = [];
  private readonly peers: string[];
  private readonly fetchImpl: FetchImpl;
  private readonly logger: Logger;
  private readonly pollIntervalMs: number;
  private readonly requestTimeoutMs: number;
  private poller: Timer | null = null;

  constructor({
    peers,
    fetchImpl = fetch,
    logger = console,
    // Poll much faster than the election timeout window so the gateway can
    // notice a new leader shortly after a failover instead of serving stale routes.
    pollIntervalMs = 300,
    requestTimeoutMs = 500,
  }: LeaderTrackerOptions) {
    this.peers = peers;
    this.fetchImpl = fetchImpl;
    this.logger = logger;
    this.pollIntervalMs = pollIntervalMs;
    this.requestTimeoutMs = requestTimeoutMs;
  }

  /**
   * pollOnce pings all configured replicas in parallel using Promise.all().
   * It inspects the RAFT state and term of each replica to pinpoint the current Leader.
   */
  async pollOnce() {
    const statuses = await Promise.all(
      this.peers.map(async (peer) => {
        try {
          const response = await this.fetchImpl(`${peer}/status`, {
            signal: AbortSignal.timeout(this.requestTimeoutMs),
          });

          if (!response.ok) {
            return null;
          }

          const status = (await response.json()) as NodeStatus;
          return { peer, status };
        } catch {
          return null;
        }
      }),
    );

    const validStatuses = statuses.filter((candidate): candidate is { peer: string; status: NodeStatus } => candidate !== null);
    
    // Save to instance property for Dashboard API
    this.clusterStatus = validStatuses;

    // If multiple candidates claim to be Leader (e.g., during network partitions),
    // we sort by RAFT `term` in descending order. The node with the highest term is the true Leader.
    const nextLeader = validStatuses
      .filter(({ status }) => status.state === "LEADER")
      .sort((left, right) => right.status.term - left.status.term)[0];

    if (!nextLeader) {
      // Clear stale leader info when no replica currently reports leadership.
      // This forces callers to refresh instead of continuing to target an old leader.
      this.leaderId = null;
      this.leaderUrl = null;
      return null;
    }

    const { peer, status } = nextLeader;
    const changedLeader = peer !== this.leaderUrl || status.term !== this.term;

    this.leaderId = status.replicaId;
    this.leaderUrl = peer;
    this.term = status.term;

    if (changedLeader) {
      this.logger.log(`[Gateway] Found Leader: ${peer} (Term ${status.term})`);
    }

    return this.leaderUrl;
  }

  /**
   * startPolling initiates the background interval that keeps the gateway's leader knowledge up to date.
   */
  startPolling() {
    if (this.poller) {
      return;
    }

    void this.pollOnce();
    this.poller = setInterval(() => {
      void this.pollOnce();
    }, this.pollIntervalMs);
  }

  stopPolling() {
    if (!this.poller) {
      return;
    }

    clearInterval(this.poller);
    this.poller = null;
  }

  getLeaderUrl() {
    return this.leaderUrl;
  }

  getLeaderId() {
    return this.leaderId;
  }

  getClusterStatus() {
    return this.clusterStatus;
  }

  async refreshLeader() {
    // Used by the WebSocket path after a failed request so we can re-discover
    // leadership immediately instead of waiting for the next background poll.
    return this.pollOnce();
  }
}
