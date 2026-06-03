# audit-worker - Multi-Stage Dockerfile for Production Images
# Optimized for security, size, and performance
# Uses distroless base image (~2MB) enabled by pure-Go franz-go Kafka client

# Build stage
FROM golang:1.26.4-bookworm AS builder

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
    -o audit-worker \
    ./services/audit-worker/cmd

# Verify the binary exists and is executable
RUN test -x audit-worker && echo "Binary built successfully"

# Runtime stage - distroless for minimal attack surface (~2MB base)
FROM gcr.io/distroless/static-debian12

# Copy timezone data from builder for time-sensitive operations
COPY --from=builder /usr/share/zoneinfo /usr/share/zoneinfo

# Copy binary from builder
COPY --from=builder /build/audit-worker /audit-worker

# Use non-root user (distroless provides nonroot user at uid 65532)
USER nonroot:nonroot

# Expose port
EXPOSE 8080

# Note: Health checks handled by Kubernetes probes (/health/live, /health/ready, /health/startup)

# Run the binary
ENTRYPOINT ["/audit-worker"]
