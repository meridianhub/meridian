# Utilization Metering Consumer - Multi-Stage Dockerfile
# Optimized for security, size, and performance
# Uses distroless base image (~2MB) enabled by pure-Go franz-go Kafka client

# Build stage
FROM golang:1.25.6-bookworm AS builder

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
# NOTE: Build assumes Docker context is repository root (required for multi-module builds)
ARG VERSION=dev
ARG COMMIT=unknown
ARG BUILD_DATE=unknown

ARG TARGETARCH
RUN CGO_ENABLED=0 GOOS=linux GOARCH=${TARGETARCH:-amd64} go build \
    -ldflags="-w -s -X main.Version=${VERSION} -X main.Commit=${COMMIT} -X main.BuildDate=${BUILD_DATE}" \
    -o utilization-metering-consumer \
    ./services/utilization-metering-consumer/cmd

# Verify the binary exists and is executable
RUN test -x utilization-metering-consumer && echo "Binary built successfully"

# Runtime stage - distroless for minimal attack surface (~2MB base)
# Pinned to specific digest for reproducible builds
FROM gcr.io/distroless/static-debian12:latest@sha256:cd64bec9cec257044ce3a8dd3620cf83b387920100332f2b041f19c4d2febf93

# Copy timezone data from builder for time-sensitive operations
COPY --from=builder /usr/share/zoneinfo /usr/share/zoneinfo

# Copy binary from builder
COPY --from=builder /build/utilization-metering-consumer /utilization-metering-consumer

# Use non-root user (distroless provides nonroot user at uid 65532)
USER nonroot:nonroot

# Expose HTTP port for health checks and metrics
EXPOSE 8080

# Note: Health checks handled by HTTP endpoints (/healthz, /ready, /metrics)

# Run the binary
ENTRYPOINT ["/utilization-metering-consumer"]
