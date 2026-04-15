/**
 * useWebSocket.ts - WebSocket Hook for Real-Time Drawing Synchronization
 * 
 * This hook manages the WebSocket connection to the Gateway server, handling:
 * 1. Connection establishment and auto-reconnect on failure
 * 2. Receiving stroke history when first connecting
 * 3. Receiving real-time strokes from other users
 * 4. Sending local strokes to the Gateway for RAFT consensus
 * 
 * Architecture Flow:
 * ┌──────────┐   WebSocket    ┌─────────┐    HTTP     ┌──────────┐
 * │ Frontend │ ◄────────────► │ Gateway │ ──────────► │  Leader  │
 * └──────────┘   (port 8080)  └─────────┘  /stroke    │ (RAFT)   │
 *                                                      └──────────┘
 * 
 * Message Types:
 * - Outgoing: { type: "stroke", stroke: Stroke }
 * - Incoming: { type: "stroke", stroke: Stroke } (from other users)
 * - Incoming: { type: "history", strokes: LogEntry[] } (on connect)
 */

import { useEffect, useRef, useCallback, useState } from 'react';

// ============================================================================
// Type Definitions - Must match gateway/types.ts exactly
// ============================================================================

/**
 * Stroke represents a single line segment drawn on the canvas.
 * This matches the Go `Stroke` struct in replica1/raft/log.go
 * and the Zod schema in gateway/types.ts.
 */
export type Stroke = {
  x0: number;  // Start X coordinate
  y0: number;  // Start Y coordinate
  x1: number;  // End X coordinate
  y1: number;  // End Y coordinate
  color: string;  // CSS color string (e.g., "#2563eb")
  width: number;  // Stroke width in pixels
};

/**
 * LogEntry wraps a Stroke with RAFT metadata.
 * This matches the Go `LogEntry` struct in replica1/raft/log.go.
 */
export type LogEntry = {
  index: number;   // 1-based log index
  term: number;    // RAFT term when entry was created
  stroke: Stroke;  // The actual stroke data
};

/**
 * Incoming WebSocket message types from the Gateway.
 */
type IncomingMessage = 
  | { type: 'stroke'; stroke: Stroke }      // Real-time stroke from another user
  | { type: 'history'; strokes: LogEntry[] }; // Full history on connect

/**
 * Connection state for UI feedback.
 */
export type ConnectionState = 'connecting' | 'connected' | 'disconnected';

/**
 * Hook return type - everything the Canvas component needs.
 */
export type UseWebSocketReturn = {
  /** Send a stroke to the Gateway for RAFT consensus */
  sendStroke: (stroke: Stroke) => void;
  /** Current connection state for UI indicators */
  connectionState: ConnectionState;
  /** Manual reconnect trigger (for "Retry" buttons) */
  reconnect: () => void;
};

// ============================================================================
// Configuration Constants
// ============================================================================

/** 
 * Default Gateway WebSocket URL.
 * In production, this would come from environment variables.
 * The Gateway runs on port 8080 as defined in docker-compose.yml.
 */
const DEFAULT_WS_URL = 'ws://localhost:8080/ws';

/**
 * Reconnection delay in milliseconds.
 * We use exponential backoff: 1s, 2s, 4s, 8s, max 30s.
 */
const INITIAL_RECONNECT_DELAY = 1000;
const MAX_RECONNECT_DELAY = 30000;

// ============================================================================
// The Hook
// ============================================================================

type UseWebSocketOptions = {
  /** WebSocket URL (defaults to ws://localhost:8080/ws) */
  url?: string;
  /** Called when stroke history is received on connect */
  onHistory?: (entries: LogEntry[]) => void;
  /** Called when a real-time stroke is received from another user */
  onStroke?: (stroke: Stroke) => void;
};

export function useWebSocket({
  url = DEFAULT_WS_URL,
  onHistory,
  onStroke,
}: UseWebSocketOptions = {}): UseWebSocketReturn {
  // -------------------------------------------------------------------------
  // Refs - Persist across renders without triggering re-renders
  // -------------------------------------------------------------------------
  
  /** The WebSocket instance. Stored in ref to avoid re-render loops. */
  const wsRef = useRef<WebSocket | null>(null);
  
  /** 
   * Current reconnect delay (exponential backoff).
   * Resets to INITIAL_RECONNECT_DELAY on successful connection.
   */
  const reconnectDelayRef = useRef(INITIAL_RECONNECT_DELAY);
  
  /** Timeout ID for scheduled reconnection attempts. */
  const reconnectTimeoutRef = useRef<ReturnType<typeof setTimeout> | null>(null);
  const reconnectAttemptRef = useRef<() => void>(() => {});
  
  /**
   * Refs for callbacks to avoid stale closures.
   * This pattern ensures we always call the latest callback version
   * without needing to re-establish the WebSocket connection.
   */
  const onHistoryRef = useRef(onHistory);
  const onStrokeRef = useRef(onStroke);

  // Keep refs in sync with latest props
  useEffect(() => {
    onHistoryRef.current = onHistory;
    onStrokeRef.current = onStroke;
  }, [onHistory, onStroke]);

  // -------------------------------------------------------------------------
  // State - Triggers re-renders for UI updates
  // -------------------------------------------------------------------------
  
  const [connectionState, setConnectionState] = useState<ConnectionState>('disconnected');

  // -------------------------------------------------------------------------
  // Connection Logic
  // -------------------------------------------------------------------------

  /**
   * Establishes a new WebSocket connection to the Gateway.
   * This function is idempotent - calling it when already connected is safe.
   */
  const connect = useCallback(() => {
    // Don't create duplicate connections
    if (wsRef.current?.readyState === WebSocket.OPEN || 
        wsRef.current?.readyState === WebSocket.CONNECTING) {
      return;
    }

    setConnectionState('connecting');

    const ws = new WebSocket(url);
    wsRef.current = ws;

    /**
     * onopen: Connection established successfully.
     * The Gateway will immediately send us the stroke history.
     */
    ws.onopen = () => {
      console.log('[WebSocket] Connected to Gateway');
      setConnectionState('connected');
      // Reset backoff on successful connection
      reconnectDelayRef.current = INITIAL_RECONNECT_DELAY;
    };

    /**
     * onmessage: Incoming data from the Gateway.
     * Two message types: "history" (on connect) and "stroke" (real-time).
     */
    ws.onmessage = (event) => {
      try {
        const message = JSON.parse(event.data) as IncomingMessage;

        if (message.type === 'history') {
          // History arrives once on connection - contains all committed strokes
          console.log(`[WebSocket] Received history: ${message.strokes.length} entries`);
          onHistoryRef.current?.(message.strokes);
        } else if (message.type === 'stroke') {
          // Real-time stroke from another user (already RAFT-committed)
          onStrokeRef.current?.(message.stroke);
        }
      } catch (error) {
        console.error('[WebSocket] Failed to parse message:', error);
      }
    };

    /**
     * onerror: Connection error occurred.
     * The browser will automatically call onclose after onerror.
     */
    ws.onerror = (error) => {
      console.error('[WebSocket] Connection error:', error);
    };

    /**
     * onclose: Connection closed (intentionally or due to error).
     * We implement auto-reconnect with exponential backoff here.
     */
    ws.onclose = (event) => {
      console.log(`[WebSocket] Disconnected (code: ${event.code}, reason: ${event.reason})`);
      setConnectionState('disconnected');
      wsRef.current = null;

      // Schedule reconnection with exponential backoff
      // This handles: network failures, Gateway restarts, Leader failover
      const delay = reconnectDelayRef.current;
      console.log(`[WebSocket] Reconnecting in ${delay}ms...`);
      
      reconnectTimeoutRef.current = setTimeout(() => {
        // Increase delay for next attempt (exponential backoff)
        reconnectDelayRef.current = Math.min(
          reconnectDelayRef.current * 2,
          MAX_RECONNECT_DELAY
        );
        reconnectAttemptRef.current();
      }, delay);
    };
  }, [url]);

  useEffect(() => {
    reconnectAttemptRef.current = connect;
  }, [connect]);

  /**
   * Manual reconnect - useful for "Retry" buttons in the UI.
   * Cancels any pending reconnect and connects immediately.
   */
  const reconnect = useCallback(() => {
    // Cancel any scheduled reconnect
    if (reconnectTimeoutRef.current) {
      clearTimeout(reconnectTimeoutRef.current);
      reconnectTimeoutRef.current = null;
    }
    
    // Close existing connection if any
    if (wsRef.current) {
      wsRef.current.close();
      wsRef.current = null;
    }
    
    // Reset backoff and connect immediately
    reconnectDelayRef.current = INITIAL_RECONNECT_DELAY;
    connect();
  }, [connect]);

  // -------------------------------------------------------------------------
  // Lifecycle - Connect on mount, cleanup on unmount
  // -------------------------------------------------------------------------

  useEffect(() => {
    const timeoutId = setTimeout(() => {
      connect();
    }, 0);

    // Cleanup: close connection and cancel pending reconnects
    return () => {
      clearTimeout(timeoutId);
      if (reconnectTimeoutRef.current) {
        clearTimeout(reconnectTimeoutRef.current);
      }
      if (wsRef.current) {
        // Use code 1000 (normal closure) to signal intentional disconnect
        wsRef.current.close(1000, 'Component unmounted');
      }
    };
  }, [connect]);

  // -------------------------------------------------------------------------
  // Send Function - Exposed to Canvas component
  // -------------------------------------------------------------------------

  /**
   * Sends a stroke to the Gateway for RAFT consensus.
   * 
   * Flow:
   * 1. Frontend calls sendStroke()
   * 2. Gateway receives { type: "stroke", stroke: {...} }
   * 3. Gateway validates with Zod and forwards to Leader's POST /stroke
   * 4. Leader replicates to Followers via AppendEntries
   * 5. Once majority ACKs, Leader responds with { committed: true }
   * 6. Gateway broadcasts stroke to all other connected clients
   * 
   * Note: The sending client draws immediately (optimistic UI).
   * Other clients receive the stroke only after RAFT commits it.
   */
  const sendStroke = useCallback((stroke: Stroke) => {
    if (wsRef.current?.readyState === WebSocket.OPEN) {
      const message = JSON.stringify({
        type: 'stroke',
        stroke,
      });
      wsRef.current.send(message);
    } else {
      console.warn('[WebSocket] Cannot send stroke - not connected');
    }
  }, []);

  // -------------------------------------------------------------------------
  // Return Value
  // -------------------------------------------------------------------------

  return {
    sendStroke,
    connectionState,
    reconnect,
  };
}
