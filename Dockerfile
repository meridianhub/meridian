# Meridian - Multi-Stage Dockerfile for Production Images
# Optimized for security, size, and performance

# Build stage
FROM golang:1.25-alpine AS builder

# Install build dependencies
RUN apk add --no-cache \
    git \
    ca-certificates \
    tzdata

# Set working directory
WORKDIR /build

# Copy go mod files first for better caching
COPY go.mod go.sum* ./
RUN go mod download && go mod verify

# Copy source code
COPY . .

# Build static binary with optimizations
ARG VERSION=dev
ARG COMMIT=unknown
ARG BUILD_DATE=unknown

RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build \
    -ldflags="-w -s -X main.Version=${VERSION} -X main.Commit=${COMMIT} -X main.BuildDate=${BUILD_DATE}" \
    -a -installsuffix cgo \
    -o meridian \
    ./cmd/meridian

# Verify the binary exists and is executable
RUN test -x meridian && echo "Binary built successfully"

# Runtime stage - distroless for minimal attack surface
FROM gcr.io/distroless/static:nonroot

# Copy CA certificates from builder
COPY --from=builder /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/

# Copy timezone data from builder (optional, for logging)
COPY --from=builder /usr/share/zoneinfo /usr/share/zoneinfo

# Copy binary from builder
COPY --from=builder /build/meridian /meridian

# Use non-root user (distroless default: 65532:65532)
USER nonroot:nonroot

# Expose port (adjust as needed)
EXPOSE 8080

# Note: Health checks handled by Kubernetes probes (/health/live, /health/ready, /health/startup)
# HEALTHCHECK not needed in distroless image (lacks curl/wget)

# Run the binary
ENTRYPOINT ["/meridian"]
