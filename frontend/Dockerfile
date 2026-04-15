# Frontend Dockerfile
# Multi-stage build for Next.js with Bun

FROM oven/bun:latest

WORKDIR /app

# Install curl for container health checks
RUN apt-get update \
  && apt-get install -y curl \
  && rm -rf /var/lib/apt/lists/*

# Copy package files first (for layer caching)
COPY package.json bun.lock* package-lock.json* ./

# Install dependencies using Bun
RUN bun install

# Copy source code
COPY . .

# Expose the Next.js dev port
EXPOSE 3000

# Start Next.js dev server with Turbopack (Next.js 16 default)
CMD ["bun", "run", "next", "dev", "--turbopack"]
