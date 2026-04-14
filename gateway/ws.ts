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
   * Broadcast a message to all clients EXCEPT the sender.
   * This is the key function for real-time sync - when one user draws,
   * all other users see the stroke immediately.
   * 
   * @param message - JSON string to send
   * @param sender - The client who sent the original stroke (excluded from broadcast)
   */
  broadcast(message: string, sender?: WSContext) {
    let sentCount = 0;
    for (const client of this.clients) {
      // Skip the sender - they already drew the stroke locally
      if (client === sender) continue;
      
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
  return {
    /**
     * onOpen: Triggered when a new user connects via WebSocket.
     * We grab the full canvas history from the Go Leader's `GET /log` endpoint
     * and push it down the socket so they can see existing drawings.
     */
    onOpen(_event, ws) {
      // Track this client for broadcasting
      clientManager.add(ws);

      const leaderUrl = tracker.getLeaderUrl();
      if (!leaderUrl) {
        logger.error("[WebSocket] No leader available to fetch history");
        return;
      }

      // Fetch and send stroke history to the new client
      void fetchImpl(`${leaderUrl}/log`)
        .then((response) => response.json())
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
        const leaderUrl = tracker.getLeaderUrl();

        if (!leaderUrl) {
          logger.error("[WebSocket] No leader available to accept stroke");
          return;
        }

        // Forward the stroke to the RAFT Leader
        // The Leader will replicate it to Followers via AppendEntries
        const response = await fetchImpl(`${leaderUrl}/stroke`, {
          method: "POST",
          headers: { "Content-Type": "application/json" },
          body: JSON.stringify(validStroke),
        });

        const data = await response.json() as { committed?: boolean };

        // Only broadcast after RAFT commits (majority ACK)
        // This ensures all clients see consistent, committed data
        if (data.committed) {
          const broadcastMessage = JSON.stringify({ 
            type: "stroke", 
            stroke: validStroke 
          });
          
          // Send to all clients EXCEPT the sender
          // The sender already drew the stroke locally (optimistic UI)
          clientManager.broadcast(broadcastMessage, ws);
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
