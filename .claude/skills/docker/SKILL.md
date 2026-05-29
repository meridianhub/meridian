---
name: skill-docker-configuration
description: Docker setup and multi-stage build configuration for production deployments
triggers:

  - Building Docker images
  - Optimizing Dockerfile
  - Debugging container issues
  - Understanding image layers

instructions: |
  Multi-stage Dockerfile for Go applications with minimal runtime image.
  Uses distroless base for security. Build with docker build -t meridian .
---

# Docker Configuration

This document describes the Docker setup for Meridian, optimized for production deployments.

## Overview

Meridian uses a multi-stage Docker build to create minimal, secure production images:

- **Build stage**: golang:1.26.2-bookworm for compiling static binaries
- **Runtime stage**: gcr.io/distroless/static:nonroot for minimal attack surface
- **Image size**: ~3-5MB (binary: 1.4MB + distroless base: ~2MB)
- **Security**: Non-root user, no shell, minimal dependencies

## Building Images

### Using Make

```bash

# Build with default settings

make docker

# Build with custom version

VERSION=1.0.0 make docker
```

### Using Docker CLI

```bash

# Basic build

docker build -t meridian:latest .

# Build with version metadata

docker build \
  --build-arg VERSION=1.0.0 \
  --build-arg COMMIT=$(git rev-parse --short HEAD) \
  --build-arg BUILD_DATE=$(date -u +"%Y-%m-%dT%H:%M:%SZ") \
  -t meridian:1.0.0 \
  .
```

## Image Features

### Multi-Stage Build

1. **Builder Stage**
   - Base: `golang:1.26.2-bookworm`
   - Installs: git, ca-certificates, tzdata
   - Compiles: Static binary with CGO disabled
   - Optimizations: `-ldflags="-w -s"` for stripped, reduced size

1. **Runtime Stage**
   - Base: `gcr.io/distroless/static:nonroot`
   - Copies: Binary, CA certificates, timezone data
   - User: nonroot (UID 65532)
   - Minimal: No shell, no package manager, no utilities

### Security Features

- **Distroless base**: Eliminates shell and reduces attack surface
- **Non-root user**: Runs as UID 65532 by default
- **Static binary**: No runtime dependencies to exploit
- **Stripped binary**: Debug symbols removed for reduced size
- **CA certificates**: HTTPS/TLS support included
- **Health checks**: Built-in endpoint for orchestration

### Build Optimizations

- **Layer caching**: Go modules downloaded before source code
- **Static linking**: CGO_ENABLED=0 for fully static binaries
- **Size optimization**: `-ldflags="-w -s"` strips debug info
- **.dockerignore**: Excludes unnecessary files from build context

## Running Containers

### Basic Run

```bash
docker run -p 8080:8080 meridian:latest
```

### With Environment Variables

```bash
docker run \
  -p 8080:8080 \
  -e PORT=8080 \
  -e LOG_LEVEL=info \
  meridian:latest
```

### Health Check

The image includes a health check that runs every 30 seconds:

```bash

# Manual health check

docker exec <container> /meridian healthcheck
```

## Image Verification

### Check Image Size

```bash
docker images meridian:latest

# Expected: 3-5MB total

```

### Scan for Vulnerabilities

```bash

# Using Trivy

trivy image meridian:latest

# Using Docker Scout

docker scout cves meridian:latest
```

### Verify Static Binary

```bash

# Extract binary from image

docker create --name temp meridian:latest
docker cp temp:/meridian ./meridian-binary
docker rm temp

# Verify it's static

file meridian-binary

# Output: ELF 64-bit LSB executable, x86-64, statically linked

# Check size

ls -lh meridian-binary

# Output: ~1.4M

# Clean up

rm meridian-binary
```

## Dockerfile Breakdown

### Build Arguments

- `VERSION`: Version string (default: "dev")
- `COMMIT`: Git commit hash (default: "unknown")
- `BUILD_DATE`: ISO 8601 timestamp (default: "unknown")

These are injected into the binary at build time via `-ldflags`.

### Exposed Ports

- `8080`: Default HTTP port (configurable via environment)

### Health Check

- **Interval**: 30 seconds
- **Timeout**: 3 seconds
- **Start period**: 5 seconds
- **Retries**: 3
- **Command**: `/meridian healthcheck`

## Best Practices

### Development

- Use `make docker` for consistent builds
- Test locally before pushing to registry
- Use specific version tags, avoid `:latest` in production

### Production

- Always specify version tags
- Scan images for vulnerabilities before deployment
- Use read-only root filesystem
- Set resource limits (CPU, memory)
- Enable security contexts in Kubernetes

### CI/CD

```yaml

# Example GitHub Actions snippet

- name: Build Docker image

  run: |
    docker build \
      --build-arg VERSION=${{ github.ref_name }} \
      --build-arg COMMIT=${{ github.sha }} \
      --build-arg BUILD_DATE=$(date -u +"%Y-%m-%dT%H:%M:%SZ") \
      -t meridian:${{ github.ref_name }} \
      .

- name: Scan image

  run: trivy image --exit-code 1 --severity HIGH,CRITICAL meridian:${{ github.ref_name }}

- name: Push to registry

  run: docker push meridian:${{ github.ref_name }}
```

## Troubleshooting

### Binary Not Found

If the container fails to start with "binary not found":

- Verify GOOS and GOARCH match your target platform
- Check that the binary was copied to the correct location
- Ensure the binary has execute permissions

### Health Check Failures

If health checks are failing:

- Verify the `/meridian healthcheck` command is implemented
- Check that the service is listening on the expected port
- Review container logs: `docker logs <container>`

### Large Image Size

If the image is larger than expected:

- Check .dockerignore is properly configured
- Verify multi-stage build is working correctly
- Use `docker history meridian:latest` to see layer sizes

## Related Documentation

- Kubernetes Deployment
- [Security Best Practices](./security.md)
- Development Setup
