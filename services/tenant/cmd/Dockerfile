# Tenant Service - Multi-Stage Dockerfile
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

ARG TARGETARCH
RUN CGO_ENABLED=0 GOOS=linux GOARCH=${TARGETARCH:-arm64} go build \
    -ldflags="-w -s -X main.Version=${VERSION} -X main.Commit=${COMMIT} -X main.BuildDate=${BUILD_DATE}" \
    -a -installsuffix cgo \
    -o tenant \
    ./services/tenant/cmd

# Verify the binary exists and is executable
RUN test -x tenant && echo "Binary built successfully"

# Runtime stage - distroless for minimal attack surface
FROM gcr.io/distroless/static:nonroot

# Copy CA certificates from builder
COPY --from=builder /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/

# Copy timezone data from builder
COPY --from=builder /usr/share/zoneinfo /usr/share/zoneinfo

# Copy binary from builder
COPY --from=builder /build/tenant /tenant

# Copy service migrations for tenant provisioning (with ownership for nonroot user)
# These are applied to org_{tenant_id} schemas during tenant provisioning
COPY --from=builder --chown=65532:65532 /build/services/party/migrations /migrations/party
COPY --from=builder --chown=65532:65532 /build/services/current-account/migrations /migrations/current-account
COPY --from=builder --chown=65532:65532 /build/services/position-keeping/migrations /migrations/position-keeping
COPY --from=builder --chown=65532:65532 /build/services/financial-accounting/migrations /migrations/financial-accounting
COPY --from=builder --chown=65532:65532 /build/services/payment-order/migrations /migrations/payment-order

# Use non-root user (distroless default: 65532:65532)
USER nonroot:nonroot

# Expose gRPC port
EXPOSE 50056

# Expose metrics port for Prometheus scraping
EXPOSE 9090

# Note: Health checks handled by gRPC health check service
# HEALTHCHECK not needed in distroless image (lacks grpcurl)

# Run the binary
ENTRYPOINT ["/tenant"]
