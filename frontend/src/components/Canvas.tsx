'use client'

/**
 * Canvas.tsx - Distributed Real-Time Drawing Canvas
 * 
 * This component provides a collaborative drawing canvas backed by RAFT consensus.
 * Multiple users can draw simultaneously, and all strokes are:
 * 1. Drawn locally immediately (optimistic UI)
 * 2. Sent to Gateway via WebSocket
 * 3. Forwarded to RAFT Leader for consensus
 * 4. Replicated to majority of replicas
 * 5. Broadcast to all other connected clients
 * 
 * Architecture:
 * ┌────────────────┐     WebSocket      ┌─────────┐     HTTP      ┌─────────────┐
 * │  Canvas.tsx    │ ◄────────────────► │ Gateway │ ────────────► │ RAFT Leader │
 * │  (this file)   │   ws://...:8080/ws │ (Bun)   │  POST /stroke │   (Go)      │
 * └────────────────┘                    └─────────┘               └─────────────┘
 *        │                                   │
 *        │ draws locally                     │ broadcasts to others
 *        ▼                                   ▼
 *   ┌─────────┐                        ┌──────────────┐
 *   │ <canvas>│                        │ Other Users' │
 *   └─────────┘                        │   Canvases   │
 *                                      └──────────────┘
 */

import { useEffect, useRef, useState, useCallback } from 'react'
import { useWebSocket, type Stroke, type LogEntry } from '../hooks/useWebSocket'
import Toolbar from './Toolbar'


const CANVAS_BG_COLOR = '#fefefe'


export default function Canvas() {

  // Refs - DOM elements and drawing state that shouldn't trigger re-renders
  /** Reference to the <canvas> DOM element */
  const canvasRef = useRef<HTMLCanvasElement>(null)

  /** Reference to the container div (used for sizing) */
  const containerRef = useRef<HTMLDivElement>(null)

  /** 
   * Last pointer position during drag.
   * Used to create line segments from lastPos -> currentPos.
   */
  const lastPos = useRef<{ x: number; y: number } | null>(null)

  /**
   * Store all strokes for redrawing on resize.
   * canvas resize clears all content.
   */
  const strokesRef = useRef<Stroke[]>([])


  // State - Triggers re-renders for UI updates
  /** Whether the user is currently drawing (pointer down) */
  const [isDrawing, setIsDrawing] = useState(false)
  const [selectedColor, setSelectedColor] = useState('#3b82f6')
  const [strokeWidth, setStrokeWidth] = useState(4)
  const [isEraser, setIsEraser] = useState(false)


  // Drawing Utilities
  /**
   * Draws a single stroke segment on the canvas.
   * This is the core rendering function used for both local and remote strokes.
   * 
   * @param ctx - Canvas 2D rendering context
   * @param stroke - The stroke data to render
   */
  const drawStroke = useCallback((ctx: CanvasRenderingContext2D, stroke: Stroke) => {
    ctx.beginPath()
    ctx.moveTo(stroke.x0, stroke.y0)
    ctx.lineTo(stroke.x1, stroke.y1)
    ctx.strokeStyle = stroke.color
    ctx.lineWidth = stroke.width
    ctx.lineCap = 'round'
    ctx.lineJoin = 'round'
    ctx.stroke()
  }, [])

  /**
   * Redraws all stored strokes on the canvas.
   * Called after resize to restore the drawing.
   * 
   * @param ctx - Canvas 2D rendering context
   * @param width - Canvas width for background fill
   * @param height - Canvas height for background fill
   */
  const redrawAllStrokes = useCallback((
    ctx: CanvasRenderingContext2D,
    width: number,
    height: number
  ) => {
    // Clear and fill background
    ctx.fillStyle = CANVAS_BG_COLOR
    ctx.fillRect(0, 0, width, height)

    // Redraw all strokes
    for (const stroke of strokesRef.current) {
      drawStroke(ctx, stroke)
    }
  }, [drawStroke])


  // WebSocket Integration


  /**
   * Called when connection is established and history is received.
   * Renders all existing strokes that were committed before we joined.
   */
  const handleHistory = useCallback((entries: LogEntry[]) => {
    const canvas = canvasRef.current
    if (!canvas) return
    const ctx = canvas.getContext('2d')
    if (!ctx) return

    console.log(`[Canvas] Rendering ${entries.length} historical strokes`)

    // Store and render each historical stroke
    for (const entry of entries) {
      strokesRef.current.push(entry.stroke)
      drawStroke(ctx, entry.stroke)
    }
  }, [drawStroke])

  /**
   * Called when another user's stroke is received.
   * The stroke has already been RAFT-committed by the time we receive it.
   */
  const handleRemoteStroke = useCallback((stroke: Stroke) => {
    const canvas = canvasRef.current
    if (!canvas) return
    const ctx = canvas.getContext('2d')
    if (!ctx) return

    // Store and render the remote stroke
    strokesRef.current.push(stroke)
    drawStroke(ctx, stroke)
  }, [drawStroke])

  /** WebSocket hook - manages connection, sends/receives strokes */
  const { sendStroke, connectionState, reconnect } = useWebSocket({
    onHistory: handleHistory,
    onStroke: handleRemoteStroke,
  })


  // Canvas Setup & Resize Handling


  useEffect(() => {
    const canvas = canvasRef.current
    const container = containerRef.current
    if (!canvas || !container) return
    const ctx = canvas.getContext('2d')
    if (!ctx) return

    /**
     * Sets up the canvas with proper high-DPI scaling.
     * 
     * Why this matters:
     * - devicePixelRatio (dpr) is typically 2 on Retina/HiDPI displays
     * - Without scaling, the canvas looks blurry on high-DPI screens
     * - We create a canvas 2x larger internally, then scale it down via CSS
     */
    const setupCanvas = () => {
      const dpr = window.devicePixelRatio || 1
      const width = container.clientWidth
      const height = container.clientHeight

      // Set actual canvas size (internal resolution)
      canvas.width = width * dpr
      canvas.height = height * dpr

      // Scale context to match DPI
      ctx.scale(dpr, dpr)

      // Set display size via CSS
      canvas.style.width = `${width}px`
      canvas.style.height = `${height}px`

      // Redraw all existing strokes after resize
      redrawAllStrokes(ctx, width, height)
    }

    //setup canvas
    setupCanvas()

    // Handle window resize - preserves drawing content
    const handleResize = () => {
      setupCanvas()
    }

    window.addEventListener('resize', handleResize)
    return () => window.removeEventListener('resize', handleResize)
  }, [redrawAllStrokes])


  // Drawing Event Handlers


  /**
   * Pointer down - Start drawing.
   * Records the starting position for the first line segment.
   */
  const startDrawing = (e: React.PointerEvent<HTMLCanvasElement>) => {
    setIsDrawing(true)
    const { nativeEvent } = e
    lastPos.current = { x: nativeEvent.offsetX, y: nativeEvent.offsetY }
  }

  /**
   * Pointer move while drawing - Create line segments.
   * 
   * For each movement:
   * 1. Draw locally (user sees immediate feedback)
   * 2. Send to Gateway (for RAFT consensus and broadcast)
   * 3. Store in strokesRef (for resize redraw)
   */
  const draw = (e: React.PointerEvent<HTMLCanvasElement>) => {
    if (!isDrawing || !lastPos.current) return

    const canvas = canvasRef.current
    if (!canvas) return
    const ctx = canvas.getContext('2d')
    if (!ctx) return

    const { nativeEvent } = e
    const currentX = nativeEvent.offsetX
    const currentY = nativeEvent.offsetY

    // Create stroke data
    const stroke: Stroke = {
      x0: lastPos.current.x,
      y0: lastPos.current.y,
      x1: currentX,
      y1: currentY,
      color: isEraser ? CANVAS_BG_COLOR : selectedColor,
      width: isEraser ? strokeWidth * 2 : strokeWidth,
    }

    // 1. Draw locally immediately (optimistic UI)
    drawStroke(ctx, stroke)

    // 2. Store for resize redraw
    strokesRef.current.push(stroke)

    // 3. Send to Gateway for RAFT consensus
    //    The Gateway will forward to the Leader, which replicates to Followers.
    //    Once committed, the Gateway broadcasts to other connected clients.
    sendStroke(stroke)

    // Update last position for next segment
    lastPos.current = { x: currentX, y: currentY }
  }

  /**
   * Pointer up/cancel/leave - Stop drawing.
   * Clears the last position to prevent accidental line connections.
   */
  const stopDrawing = () => {
    setIsDrawing(false)
    lastPos.current = null
  }


  // Render
  return (
    <div ref={containerRef} className={`w-full h-full relative ${isEraser ? 'cursor-cell' : 'cursor-crosshair'}`}>
      <Toolbar
        selectedColor={selectedColor}
        onSelectColor={setSelectedColor}
        strokeWidth={strokeWidth}
        onSelectWidth={setStrokeWidth}
        isEraser={isEraser}
        onToggleEraser={setIsEraser}
      />
      <div className="absolute top-4 right-4 z-10 flex items-center gap-2">
        <div
          className={`w-3 h-3 rounded-full transition-colors ${connectionState === 'connected'
            ? 'bg-green-500'
            : connectionState === 'connecting'
              ? 'bg-yellow-500 animate-pulse'
              : 'bg-red-500'
            }`}
          title={`Status: ${connectionState}`}
        />
        {connectionState === 'disconnected' && (
          <button
            onClick={reconnect}
            className="text-xs px-2 py-1 bg-neutral-100 hover:bg-neutral-200 rounded text-neutral-600 transition-colors"
          >
            Retry
          </button>
        )}
      </div>
      <canvas
        ref={canvasRef}
        onPointerDown={startDrawing}
        onPointerMove={draw}
        onPointerUp={stopDrawing}
        onPointerCancel={stopDrawing}
        onPointerLeave={stopDrawing}
        className="touch-none absolute inset-0 rounded-2xl shadow-[inset_0_2px_20px_rgba(0,0,0,0.05)] border border-neutral-100"
      />
    </div>
  )
}
