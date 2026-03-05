---
name: financial-gateway
description: External payment provider integration gateway for dispatching and reconciling financial instructions
triggers:
  - Working on external payment provider integrations
  - Implementing payment instruction dispatch and reconciliation
  - Configuring provider connections and webhooks
  - Debugging failed payment instructions
instructions: |
  Financial Gateway manages outbound payment instructions to external providers
  and inbound webhook reconciliation.

  Key concepts:
  - Provider connections with health monitoring
  - Instruction lifecycle: PENDING → DISPATCHING → DELIVERED → ACKNOWLEDGED
  - Webhook handling for provider callbacks
  - Retry and circuit breaker patterns for provider communication

  Port: gRPC + HTTP
---

# Financial Gateway

External payment provider integration gateway for dispatching and reconciling financial instructions.

## Overview

| Attribute | Value |
|-----------|-------|
| **Domain** | Financial Gateway |
| **Language** | Go |
| **Database** | CockroachDB |
| **Standalone** | No (requires provider connections) |

## Purpose

The Financial Gateway manages the boundary between Meridian's internal ledger
and external payment providers by:

- Dispatching payment instructions to configured provider connections
- Tracking instruction lifecycle (pending, dispatching, delivered, acknowledged)
- Processing inbound webhooks from external providers
- Monitoring provider connection health

## Directory Structure

```text
services/financial-gateway/
├── adapters/           # External adapters (provider clients)
├── atlas/              # Atlas migration configuration
├── client/             # Go client library
├── cmd/                # Entry point (main.go, Dockerfile)
├── config/             # Configuration loading
├── e2e/                # End-to-end tests
├── migrations/         # CockroachDB migrations
├── observability/      # Prometheus metrics and tracing
└── service/            # gRPC service implementation
```

## References

- [Service Architecture](../README.md)
