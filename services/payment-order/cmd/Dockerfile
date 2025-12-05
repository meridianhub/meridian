# Payment-Order Service - Multi-Stage Dockerfile
# Optimized for security, size, and performance
# Note: Requires CGO for confluent-kafka-go (librdkafka)

# Build stage
FROM golang:1.25-alpine AS builder

# Install build dependencies including librdkafka for Kafka client
RUN apk add --no-cache \
    git \
    ca-certificates \
    tzdata \
    gcc \
    musl-dev \
    librdkafka-dev \
    pkgconf

# Set working directory
WORKDIR /build

# Copy go mod files first for better caching
COPY go.mod go.sum* ./
RUN go mod download && go mod verify

# Copy source code
COPY . .

# Build binary with CGO enabled (required for confluent-kafka-go)
ARG VERSION=dev
ARG COMMIT=unknown
ARG BUILD_DATE=unknown
ARG TARGETARCH

# Build with CGO enabled and static linking for librdkafka
RUN CGO_ENABLED=1 GOOS=linux GOARCH=${TARGETARCH:-arm64} go build \
    -tags musl \
    -ldflags="-w -s -X main.Version=${VERSION} -X main.Commit=${COMMIT} -X main.BuildDate=${BUILD_DATE} -linkmode external -extldflags '-static'" \
    -o payment-order \
    ./services/payment-order/cmd

# Verify the binary exists and is executable
RUN test -x payment-order && echo "Binary built successfully"

# Runtime stage - alpine for CGO binary (distroless doesn't work with CGO)
FROM alpine:3.19

# Install runtime dependencies
RUN apk add --no-cache ca-certificates tzdata

# Create non-root user
RUN addgroup -g 65532 -S nonroot && \
    adduser -u 65532 -S nonroot -G nonroot

# Copy binary from builder
COPY --from=builder /build/payment-order /payment-order

# Use non-root user
USER nonroot:nonroot

# Expose gRPC port
EXPOSE 50054

# Run the binary
ENTRYPOINT ["/payment-order"]
