import { Hono } from "hono";
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

  return app;
}
