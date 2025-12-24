# Utilization Metering Consumer - Multi-Stage Dockerfile
# Optimized for security, size, and performance
# Note: Requires CGO for confluent-kafka-go (librdkafka) - uses Debian for glibc

# Build stage - Debian for glibc compatibility with librdkafka
FROM golang:1.25-bookworm AS builder

# Install build dependencies including librdkafka for Kafka support
RUN apt-get update && apt-get install -y --no-install-recommends \
    git \
    ca-certificates \
    librdkafka-dev \
    && rm -rf /var/lib/apt/lists/*

# Set working directory
WORKDIR /build

# Copy go mod files first for better caching
COPY go.mod go.sum* ./
RUN go mod download && go mod verify

# Copy source code
COPY . .

# Build binary with CGO enabled for librdkafka
ARG VERSION=dev
ARG COMMIT=unknown
ARG BUILD_DATE=unknown

ARG TARGETARCH
RUN CGO_ENABLED=1 GOOS=linux GOARCH=${TARGETARCH:-arm64} go build \
    -ldflags="-w -s -X main.Version=${VERSION} -X main.Commit=${COMMIT} -X main.BuildDate=${BUILD_DATE}" \
    -o utilization-metering-consumer \
    ./services/utilization-metering-consumer/cmd

# Verify the binary exists and is executable
RUN test -x utilization-metering-consumer && echo "Binary built successfully"

# Runtime stage - Debian slim for glibc + librdkafka
FROM debian:bookworm-slim

# Install runtime dependencies
RUN apt-get update && apt-get install -y --no-install-recommends \
    ca-certificates \
    librdkafka1 \
    tzdata \
    && rm -rf /var/lib/apt/lists/*

# Create non-root user for security
RUN groupadd -g 65532 nonroot && \
    useradd -u 65532 -g nonroot -s /bin/false nonroot

# Copy binary from builder
COPY --from=builder /build/utilization-metering-consumer /utilization-metering-consumer

# Use non-root user
USER nonroot:nonroot

# Expose HTTP port for health checks and metrics
EXPOSE 8080

# Note: Health checks handled by HTTP endpoints (/healthz, /ready, /metrics)

# Run the binary
ENTRYPOINT ["/utilization-metering-consumer"]
