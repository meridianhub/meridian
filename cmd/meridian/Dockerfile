# meridian unified binary - Multi-Stage Dockerfile
# Optimized for security, size, and performance
# Uses distroless base image (~2MB) enabled by CGO_ENABLED=0 static binary

# Build stage
FROM golang:1.26.0-bookworm AS builder

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

# Copy full source (needed for //go:embed directives spanning services/*/migrations/)
COPY . .

# Build static binary
ARG VERSION=dev
ARG COMMIT=unknown
ARG BUILD_DATE=unknown

ARG TARGETARCH
RUN CGO_ENABLED=0 GOOS=linux GOARCH=${TARGETARCH:-amd64} go build \
    -ldflags="-w -s -X main.Version=${VERSION} -X main.Commit=${COMMIT} -X main.BuildDate=${BUILD_DATE}" \
    -o meridian \
    ./cmd/meridian/

RUN CGO_ENABLED=0 GOOS=linux GOARCH=${TARGETARCH:-amd64} go build \
    -ldflags="-w -s" \
    -o seed-dev \
    ./cmd/seed-dev/

# Verify the binaries exist and are executable
RUN test -x meridian && test -x seed-dev && echo "Binaries built successfully"

# Runtime stage - distroless for minimal attack surface (~2MB base)
FROM gcr.io/distroless/static-debian12

# Copy timezone data from builder for time-sensitive operations
COPY --from=builder /usr/share/zoneinfo /usr/share/zoneinfo

# Copy binaries from builder
COPY --from=builder /build/meridian /meridian
COPY --from=builder /build/seed-dev /seed-dev

# Use non-root user (distroless provides nonroot user at uid 65532)
USER nonroot:nonroot

# gRPC port and HTTP gateway port
EXPOSE 50051 8090

# Note: Health checks handled by gRPC health check service

# Run the binary
ENTRYPOINT ["/meridian"]
