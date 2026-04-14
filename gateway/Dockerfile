# Gateway Dockerfile
# Multi-stage build for Bun + Hono WebSocket Gateway

FROM oven/bun:latest

WORKDIR /app

# Copy package files first (for layer caching)
COPY package.json bun.lock* ./

# Install dependencies
RUN bun install

# Copy source code
COPY . .

# Expose the gateway port
EXPOSE 8080

# Start the gateway with hot-reload
CMD ["bun", "run", "--watch", "index.ts"]
