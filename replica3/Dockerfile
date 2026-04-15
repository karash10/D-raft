# =============================================================================
# DOCKERFILE FOR RAFT REPLICA
# =============================================================================
# This Dockerfile builds and runs a RAFT replica node using a multi-stage build.
#
# Stage 1 (builder): Compiles the Go binary
# Stage 2 (runtime): Runs the binary with Air for hot-reloading
#
# Usage:
#   docker build -t miniraft-replica ./replica1
#   docker run -e REPLICA_ID=replica1 -e PORT=9001 -e PEERS=... miniraft-replica
#
# For development with hot-reload:
#   Use docker-compose which mounts the source code and runs Air

# =============================================================================
# STAGE 1: BUILD
# =============================================================================
# We use the official Go image to compile our application.
# Alpine variant is smaller but we use the full image for better compatibility.
FROM golang:1.21 AS builder

# Set the working directory inside the container
WORKDIR /app

# Copy go.mod and go.sum first (for better Docker layer caching)
# If these files haven't changed, Docker can reuse the cached layer
# for the expensive "go mod download" step.
COPY go.mod go.sum* ./

# Download all dependencies
# This step is cached unless go.mod/go.sum change
RUN go mod download

# Copy the rest of the source code
COPY . .

# Build the Go binary
# CGO_ENABLED=0: Disable CGO for a fully static binary (no libc dependency)
# -o /app/main: Output the binary to /app/main
RUN CGO_ENABLED=0 GOOS=linux go build -o /app/main .

# =============================================================================
# STAGE 2: RUNTIME (Development with Hot-Reload)
# =============================================================================
# For development, we use a full Go image with Air installed for hot-reloading.
# Air watches for file changes and automatically rebuilds/restarts the server.
FROM golang:1.25

# Install Air for hot-reloading
# Air is a live-reload tool for Go applications
RUN apt-get update \
	&& apt-get install -y curl \
	&& rm -rf /var/lib/apt/lists/* \
	&& go install github.com/air-verse/air@latest

# Set the working directory
WORKDIR /app

# Copy go.mod and go.sum for dependency resolution
COPY go.mod go.sum* ./

# Download dependencies (needed for Air to rebuild)
RUN go mod download

# Copy the source code
# In development, this is overridden by a volume mount in docker-compose
COPY . .

# Copy the Air configuration file
COPY .air.toml .

# Expose the port (will be overridden by environment variable)
# This is just documentation; the actual port is set via PORT env var
EXPOSE 9001

# =============================================================================
# ENTRYPOINT
# =============================================================================
# In development mode (with docker-compose volumes), Air watches for changes
# and rebuilds automatically.
#
# The replica reads its configuration from environment variables:
#   - REPLICA_ID: Node identifier (replica1, replica2, replica3)
#   - PORT: HTTP port to listen on
#   - PEERS: Comma-separated list of peer URLs

# Run Air for hot-reloading in development
CMD ["air", "-c", ".air.toml"]
