import type { LeaderSource } from "./leader";
import { StrokeSchema } from "./types";
import type { WSEvents, WSContext } from "hono/ws";

type Logger = Pick<Console, "error" | "log">;
type FetchImpl = typeof fetch;

type WebSocketOptions = {
  fetchImpl?: FetchImpl;
  logger?: Logger;
};

/**
 * ClientManager handles tracking all connected WebSocket clients
 * and broadcasting messages to all of them (except the sender).
 * 
 * This is necessary because Hono's WebSocket adapter doesn't expose
 * Bun's native pub/sub methods (subscribe/publish/unsubscribe).
 */
class ClientManager {
  /** Set of all connected WebSocket clients */
  private clients = new Set<WSContext>();
  private logger: Logger;

  constructor(logger: Logger = console) {
    this.logger = logger;
  }

  /** Add a new client when they connect */
  add(ws: WSContext) {
    this.clients.add(ws);
    this.logger.log(`[ClientManager] Client connected. Total: ${this.clients.size}`);
  }

  /** Remove a client when they disconnect */
  remove(ws: WSContext) {
    this.clients.delete(ws);
    this.logger.log(`[ClientManager] Client disconnected. Total: ${this.clients.size}`);
  }

  /**
   * Broadcast a message to all connected clients.
   * We include the sender so every browser renders only committed strokes
   * from the gateway and stays visually consistent during failover.
   * 
   * @param message - JSON string to send
   */
  broadcast(message: string) {
    let sentCount = 0;
    for (const client of this.clients) {
      try {
        client.send(message);
        sentCount++;
      } catch (error) {
        // Client might have disconnected, remove them
        this.logger.error("[ClientManager] Failed to send to client, removing", error);
        this.clients.delete(client);
      }
    }
    this.logger.log(`[ClientManager] Broadcast to ${sentCount} clients`);
  }

  /** Get the number of connected clients */
  get size() {
    return this.clients.size;
  }
}

// Global client manager instance (shared across all WebSocket connections)
const clientManager = new ClientManager();

/**
 * setupWebSocket wires the Bun/Hono WebSocket events to the Go RAFT backend APIs.
 * It handles loading initial history, validating incoming strokes, forwarding them to 
 * the Leader replica, and broadcasting committed strokes back to browsers.
 */
export function setupWebSocket(
  tracker: LeaderSource,
  { fetchImpl = fetch, logger = console }: WebSocketOptions = {},
): WSEvents {
  const resolveLeader = async () => {
    const existingLeader = tracker.getLeaderUrl();
    if (existingLeader) {
      return existingLeader;
    }

    // If the cache is empty, force an immediate leader lookup.
    // This avoids waiting for the next polling tick before serving the request.
    return tracker.refreshLeader();
  };

  return {
    /**
     * onOpen: Triggered when a new user connects via WebSocket.
     * We grab the full canvas history from the Go Leader's `GET /log` endpoint
     * and push it down the socket so they can see existing drawings.
     */
    onOpen(_event, ws) {
      // Track this client for broadcasting
      clientManager.add(ws);

      void resolveLeader()
        .then((leaderUrl) => {
          if (!leaderUrl) {
            throw new Error("no leader available to fetch history");
          }

          return fetchImpl(`${leaderUrl}/log`);
        })
        .then((response) => {
          if (!response.ok) {
            throw new Error(`history fetch failed with status ${response.status}`);
          }

          return response.json();
        })
        .then((data: unknown) => {
          const typedData = data as { entries?: unknown[] };
          const historyMessage = JSON.stringify({ 
            type: "history", 
            strokes: typedData.entries ?? [] 
          });
          ws.send(historyMessage);
          logger.log(`[WebSocket] Sent ${typedData.entries?.length ?? 0} history entries`);
        })
        .catch((error) => logger.error("[WebSocket] Failed to fetch history", error));
    },

    /**
     * onMessage: Triggered when a user draws a stroke and sends JSON.
     * We validate it with Zod, forward it to the Go Leader's `POST /stroke` API,
     * and wait safely for RAFT confirmation before broadcasting.
     */
    async onMessage(event, ws) {
      try {
        const payload = JSON.parse(String(event.data));

        if (payload.type !== "stroke") {
          return;
        }

        // Validate stroke data with Zod schema
        const validStroke = StrokeSchema.parse(payload.stroke);
        let leaderUrl = await resolveLeader();

        if (!leaderUrl) {
          logger.error("[WebSocket] No leader available to accept stroke");
          return;
        }

        const forwardStroke = async (targetLeaderUrl: string) => {
          const response = await fetchImpl(`${targetLeaderUrl}/stroke`, {
            method: "POST",
            headers: { "Content-Type": "application/json" },
            body: JSON.stringify(validStroke),
          });

          const data = await response.json() as { committed?: boolean };
          return { response, data };
        };

        // Forward the stroke to the RAFT Leader.
        let { response, data } = await forwardStroke(leaderUrl);

        // A failover can happen after we read the cached leader but before the POST lands.
        // Refresh leadership immediately and retry once so transient leader changes do not
        // drop the user's stroke during the election/recovery window.
        if (!response.ok || !data.committed) {
          const refreshedLeaderUrl = await tracker.refreshLeader();
          if (refreshedLeaderUrl && refreshedLeaderUrl !== leaderUrl) {
            leaderUrl = refreshedLeaderUrl;
            ({ response, data } = await forwardStroke(leaderUrl));
          }
        }

        // Only broadcast after RAFT commits (majority ACK)
        // This ensures all clients see consistent, committed data
        if (response.ok && data.committed) {
          const broadcastMessage = JSON.stringify({ 
            type: "stroke", 
            stroke: validStroke 
          });
          
          // Send committed strokes to every client, including the sender.
          // This keeps all tabs in sync even if failover caused some writes to miss commit.
          clientManager.broadcast(broadcastMessage);
        } else {
          logger.error("[WebSocket] Stroke not committed by RAFT");
        }
      } catch (error) {
        logger.error("[WebSocket] Failed to process stroke", error);
      }
    },

    /**
     * onClose: Triggered when a browser tab closes.
     * Remove the client from our tracking set.
     */
    onClose(_event, ws) {
      clientManager.remove(ws);
    },
  };
}

// Export for testing
export { ClientManager, clientManager };
