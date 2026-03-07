# Event Router - Multi-Stage Dockerfile
# Optimized for security, size, and performance
# Uses distroless base image (~2MB) enabled by pure-Go franz-go Kafka client

# Build stage
FROM golang:1.26.1-bookworm AS builder

# Install build dependencies
RUN apt-get update && apt-get install -y --no-install-recommends \
    git \
    ca-certificates \
    && rm -rf /var/lib/apt/lists/*

# Set working directory
WORKDIR /build

# Copy go mod files first for better caching
COPY go.mod go.sum* ./
RUN go mod download && go mod verify

# Copy source code
COPY . .

# Build static binary (no CGO required - using franz-go pure Go Kafka client)
ARG VERSION=dev
ARG COMMIT=unknown
ARG BUILD_DATE=unknown

ARG TARGETARCH
RUN CGO_ENABLED=0 GOOS=linux GOARCH=${TARGETARCH:-amd64} go build \
    -ldflags="-w -s -X main.Version=${VERSION} -X main.Commit=${COMMIT} -X main.BuildDate=${BUILD_DATE}" \
    -o event-router \
    ./services/event-router/cmd

# Verify the binary exists and is executable
RUN test -x event-router && echo "Binary built successfully"

# Runtime stage - distroless for minimal attack surface (~2MB base)
FROM gcr.io/distroless/static-debian12

# Copy timezone data from builder for time-sensitive operations
COPY --from=builder /usr/share/zoneinfo /usr/share/zoneinfo

# Copy binary from builder
COPY --from=builder /build/event-router /event-router

# Use non-root user (distroless provides nonroot user at uid 65532)
USER nonroot:nonroot

# Expose HTTP port for health checks and metrics
EXPOSE 8080

# Note: Health checks handled by HTTP endpoints (/healthz, /ready, /metrics)

# Run the binary
ENTRYPOINT ["/event-router"]
