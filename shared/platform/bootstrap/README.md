---
name: service-bootstrap
description: Shared service initialization for database, gRPC, Redis, and observability
triggers:
  - Service initialization and startup
  - Database connection setup
  - gRPC server configuration
  - Graceful shutdown handling
  - Authentication interceptors
instructions: |
  Bootstrap provides NewDatabase(), NewTracer(), GrpcServerBuilder, and shutdown utilities.
  Use ShutdownOrchestrator for full gRPC lifecycle with LIFO cleanup.
  See doc.go for complete service initialization example.
---

# Bootstrap Package

The bootstrap package provides shared infrastructure initialization utilities for Meridian services.
It consolidates duplicated patterns for database, Redis, gRPC, authentication, observability, and
graceful shutdown.

## Components

### Database

- `NewDatabase()` - Creates GORM connection with health check
- `CloseDatabase()` - Graceful connection cleanup

### Redis

- `NewRedisClient()` - Creates Redis client with connection pooling

### Observability

- `NewTracer()` - Creates OpenTelemetry tracer with OTLP exporter
- `ShutdownTracer()` - Flushes pending spans

### Authentication

- `NewAuthInterceptor()` - JWT validation interceptor for gRPC

### gRPC Server

- `GrpcServerBuilder` - Fluent builder with interceptor chain

### Shutdown Utilities

Utilities for graceful service shutdown:

- `SignalHandler()` - SIGINT/SIGTERM handler with cleanup function
- `ServerErrorChannel()` - Properly-sized buffered channel for server errors
- `WaitForShutdownSignal()` - Blocks until signal or server error
- `GracefulShutdown()` - Shuts down multiple servers with timeout
- `ShutdownOrchestrator` - Full gRPC lifecycle with LIFO cleanup

See [SHUTDOWN_USAGE.md](./SHUTDOWN_USAGE.md) for detailed examples and migration guide.

## Quick Start

See `doc.go` for a complete service initialization example.

## Environment Variables

Configuration is loaded from environment variables. See `doc.go` for the complete list.
