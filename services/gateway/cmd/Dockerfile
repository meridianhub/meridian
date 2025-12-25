# Gateway Service - Multi-Stage Dockerfile
# Optimized for security, size, and performance
# HTTP-only gateway (no gRPC, no Kafka dependencies)

# Build stage
FROM golang:1.25-bookworm AS builder

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

# Build binary with CGO disabled (static binary)
ARG VERSION=dev
ARG COMMIT=unknown
ARG BUILD_DATE=unknown

ARG TARGETARCH
RUN CGO_ENABLED=0 GOOS=linux GOARCH=${TARGETARCH:-arm64} go build \
    -ldflags="-w -s -X main.Version=${VERSION} -X main.Commit=${COMMIT} -X main.BuildDate=${BUILD_DATE}" \
    -o gateway \
    ./services/gateway/cmd

# Verify the binary exists and is executable
RUN test -x gateway && echo "Binary built successfully"

# Runtime stage - Debian slim
FROM debian:bookworm-slim

# Install runtime dependencies
RUN apt-get update && apt-get install -y --no-install-recommends \
    ca-certificates \
    tzdata \
    && rm -rf /var/lib/apt/lists/*

# Create non-root user for security
RUN groupadd -g 65532 nonroot && \
    useradd -u 65532 -g nonroot -s /bin/false nonroot

# Copy binary from builder
COPY --from=builder /build/gateway /gateway

# Use non-root user
USER nonroot:nonroot

# Expose HTTP port
EXPOSE 8080

# Note: Health checks handled by /health and /ready endpoints

# Run the binary
ENTRYPOINT ["/gateway"]
