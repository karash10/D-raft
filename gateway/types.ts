import * as z from "zod";

/**
 * Zod Schema for strict runtime validation of incoming strokes.
 * This perfectly mirrors the `Stroke` struct in the Go replica code (raft/log.go).
 * By running `StrokeSchema.parse()`, we ensure bad data never crashes the Gateway or Go backend.
 */
//schema builder for validatiing plain js objects 
// when we call parse() it will validate the object and return the object
// if the object is not valid it will throw an error
export const StrokeSchema = z.object({
    x0: z.number(),
    y0: z.number(),
    x1: z.number(),
    y1: z.number(),
    color: z.string(),
    width: z.number(),
})

// extracting static typescript type from this zod schema 
//soo that we can use this schema inferred type directly inside the typescript file.
//basically we are making sure all the tyoes are in sync everywhere
/**
 * Extracts a static TypeScript type from the Zod schema.
 * This guarantees our compile-time types and runtime checks are perfectly synced.
 */
export type Stroke = z.infer<typeof StrokeSchema>

/**
 * NodeStatus reflects the exact JSON payload returned by the Go replica's `GET /status` endpoint.
 * (Mapped from fiber.Map in handlers/rpc.go).
 */
export type NodeStatus = {
    replicaId: string;
    state: string;
    term: number;
    commitIndex: number;
    logLength: number;
}

