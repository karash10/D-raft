import { Hono } from "hono";
import { cors } from "hono/cors";
import { upgradeWebSocket } from "hono/bun";
import type { WSEvents } from "hono/ws";
import type { LeaderSource } from "./leader";
import { setupWebSocket } from "./ws";

type CreateGatewayAppOptions = {
  tracker: LeaderSource;
  createWebSocketEvents?: (tracker: LeaderSource) => WSEvents;
};

export function createGatewayApp({
  tracker,
  createWebSocketEvents = setupWebSocket,
}: CreateGatewayAppOptions) {
  const app = new Hono();

  app.get("/ws", upgradeWebSocket(() => createWebSocketEvents(tracker)));

  app.get("/status", (c) => {
    return c.json({ status: "ok", currentLeader: tracker.getLeaderUrl() });
  });

  app.get("/cluster-status", cors(), (c) => {
    return c.json({ nodes: tracker.getClusterStatus() });
  });

  return app;
}
