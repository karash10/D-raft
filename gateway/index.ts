import { websocket } from "hono/bun";
import pino from "pino";
import { createGatewayApp } from "./app";
import { LeaderTracker } from "./leader";

const DEFAULT_REPLICA_URLS = [
  "http://localhost:9001",
  "http://localhost:9002",
  "http://localhost:9003",
];

const log = pino({ transport: { target: "pino-pretty" } });

/**
 * Initialize the LeaderTracker with the Go Replicas.
 * This runs locally matching our docker-compose.yml configuration.
 */
const tracker = new LeaderTracker({
  peers: process.env.REPLICA_URLS?.split(",").filter(Boolean) ?? DEFAULT_REPLICA_URLS,
});

if (process.env.DISABLE_GATEWAY_POLLING !== "1") {
  tracker.startPolling();
}

/**
 * Bootstrap the Hono Gateway application, injecting the tracker.
 * This wires up the `/status` checking and the `/ws` WebSocket upgrade endpoints.
 */
const app = createGatewayApp({ tracker });
const port = Number(process.env.GATEWAY_PORT ?? process.env.PORT ?? 3001);

log.info(`Gateway starting on port ${port}`);

export { app, tracker };

export default {
  port,
  fetch: app.fetch,
  websocket,
};
