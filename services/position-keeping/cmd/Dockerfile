# Position-Keeping Service - Multi-Stage Dockerfile
# Optimized for security, size, and performance
# Uses distroless base image (~2MB) enabled by pure-Go franz-go Kafka client

# Build stage
FROM golang:1.26.2-bookworm AS builder

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
    -o position-keeping \
    ./services/position-keeping/cmd

# Verify the binary exists and is executable
RUN test -x position-keeping && echo "Binary built successfully"

# Runtime stage - distroless for minimal attack surface (~2MB base)
FROM gcr.io/distroless/static-debian12

# Copy timezone data from builder for time-sensitive operations
COPY --from=builder /usr/share/zoneinfo /usr/share/zoneinfo

# Copy binary from builder
COPY --from=builder /build/position-keeping /position-keeping

# Use non-root user (distroless provides nonroot user at uid 65532)
USER nonroot:nonroot

# Expose gRPC port
EXPOSE 50053

# Note: Health checks handled by gRPC health check service

# Run the binary
ENTRYPOINT ["/position-keeping"]
