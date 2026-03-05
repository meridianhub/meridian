---
name: event-router
description: CEL-filtered saga dispatcher that routes domain events from Kafka to saga workflows and platform billing handlers
triggers:
  - Working on event-driven saga triggering
  - Implementing CEL-based event filtering and routing
  - Configuring platform billing and utilization metering
  - Debugging event routing pipelines
  - Understanding how domain events trigger saga workflows
instructions: |
  Event Router is a Kafka consumer that routes domain events to registered handlers
  using CEL filter expressions. Its primary responsibilities are saga triggering
  and platform utilization metering.

  Key concepts:
  - Consumes domain events from multiple Kafka topics
  - Routes events to handlers based on channel and event type
  - Triggers saga workflows via the control-plane's SagaExecutionService
  - Transforms audit events into utilization measurements for platform billing
  - Idempotent saga triggering via idempotency keys

  Architecture patterns:
  - Handler registry with pluggable EventHandler interface
  - CEL expressions for event filtering
  - Fire-and-forget event consumption (at-least-once semantics)
  - Saga triggering via gRPC to control-plane
  - Utilization metering via Position Keeping tenant-zero

  Port: 8080 (HTTP - health checks and metrics)
---

# Event Router

CEL-filtered saga dispatcher that routes domain events from Kafka to saga workflows and platform billing handlers.

## Overview

| Attribute | Value |
|-----------|-------|
| **Domain** | Infrastructure (Event Routing) |
| **Port** | 8080 (HTTP) |
| **Language** | Go |
| **Database** | None (stateless consumer) |
| **Standalone** | No (requires Kafka, Control Plane, Position Keeping) |

## Purpose

The Event Router provides event-driven workflow orchestration by:

- Consuming domain events from Kafka topics (audit events, transaction events)
- Routing events to registered handlers via the `EventHandler` interface
- Triggering saga workflows through the control-plane's `SagaExecutionService`
- Transforming audit events into utilization measurements for platform billing (tenant-zero)

## HTTP Endpoints

| Endpoint | Method | Purpose |
|----------|--------|---------|
| `/healthz` | GET | Liveness probe |
| `/ready` | GET | Readiness probe (checks consumer initialization) |
| `/metrics` | GET | Prometheus metrics endpoint |

## Architecture

### Handler Registry

The Event Router uses a pluggable handler model:

- **`EventHandler`** interface: `Handle(ctx, channel, event, metadata) error`
- **`SagaTrigger`** interface: Triggers sagas via control-plane gRPC with idempotency keys
- **`UtilizationPublisher`**: Transforms audit events into billing measurements

```mermaid
flowchart LR
    subgraph Kafka["Kafka Topics"]
        T1["audit.events.*"]
        T2["position-keeping<br/>.transaction-captured.v1"]
    end

    subgraph ER["Event Router"]
        Consumer["Multi-Topic<br/>Consumer"]
        Router["Handler<br/>Router"]
        SH["Saga<br/>Handler"]
        UH["Utilization<br/>Handler"]
    end

    subgraph Downstream["Downstream Services"]
        CP["Control Plane<br/>(Saga Execution)"]
        PK["Position Keeping<br/>(Tenant-Zero)"]
    end

    T1 --> Consumer
    T2 --> Consumer
    Consumer --> Router
    Router --> SH
    Router --> UH
    SH -->|TriggerSaga| CP
    UH -->|RecordMeasurement| PK

    classDef kafka fill:#50c878,stroke:#2d7a4a,color:#fff
    classDef service fill:#4a90d9,stroke:#2d5a87,color:#fff
    classDef downstream fill:#9c27b0,stroke:#6a1b9a,color:#fff
    class T1,T2 kafka
    class Consumer,Router,SH,UH service
    class CP,PK downstream
```

### Event Processing Pipeline

1. **Consume**: Read event from Kafka topic
2. **Deserialize**: Parse Protobuf event
3. **Route**: Dispatch to registered handler(s) based on channel
4. **Handle**: Handler processes event (trigger saga or record measurement)
5. **Commit**: Commit Kafka offset (at-least-once semantics)

## Service Dependencies

| Service | Port | Purpose |
|---------|------|---------|
| Kafka | 9092 | Domain event streaming |
| Control Plane | gRPC | Saga workflow triggering |
| Position Keeping | 50053 | Utilization measurements (tenant-zero billing) |

## Configuration

| Variable | Required | Default | Purpose |
|----------|----------|---------|---------|
| `KAFKA_BOOTSTRAP_SERVERS` | Yes | `kafka:9092` | Kafka broker addresses |
| `CONSUMER_GROUP_ID` | Yes | `event-router` | Consumer group identifier |
| `AUDIT_TOPICS` | Yes | - | Comma-separated list of topics to consume |
| `POSITION_KEEPING_ENDPOINT` | Yes | `position-keeping:50053` | Position Keeping gRPC endpoint |
| `TENANT_ZERO_ID` | Yes | - | UUID of platform billing tenant |
| `TENANT_ACCOUNT_MAPPING` | No | `{}` | JSON mapping of tenant IDs to billing accounts |
| `HTTP_PORT` | No | `8080` | HTTP server port for health/metrics |
| `CONTROL_PLANE_ENDPOINT` | Yes | - | Control Plane gRPC endpoint for saga triggering |
| `MARKET_DATA_ENDPOINT` | No | - | Market Information gRPC endpoint |

## Key Patterns

### Idempotent Saga Triggering

Saga triggers include an idempotency key derived from the source event. Duplicate triggers
(e.g., Kafka redelivery) return the existing saga ID without re-executing.

### Stateless Consumer

No local database. All state resides in downstream services (Control Plane for sagas,
Position Keeping for billing). This enables horizontal scaling without data partitioning.

### At-Least-Once Semantics

Manual Kafka offset commits after successful processing. Duplicate events are handled
by downstream idempotency.

## Directory Structure

```text
services/event-router/
├── cmd/                    # Entry point (main.go, Dockerfile)
├── domain/                 # Domain models
│   ├── handler.go          # EventHandler interface
│   ├── saga_trigger.go     # SagaTrigger interface
│   ├── measurement.go      # UtilizationMeasurement type
│   ├── instruments.go      # Instrument code mapping
│   ├── metrics.go          # Prometheus metrics
│   └── tenant_mapping.go   # Tenant-to-account mapping
├── adapters/               # External adapters
│   ├── grpc/               # Control Plane saga trigger client
│   ├── mds/                # Market Data Service client
│   └── messaging/          # Kafka consumer
├── app/                    # Application configuration
│   └── config.go           # Config loading and validation
├── internal/               # Internal implementation
├── k8s/                    # Kubernetes manifests
└── tests/                  # Integration tests
```

## References

- [Service Architecture](../README.md)
- [Kubernetes Deployment Guide](k8s/README.md)
- [Position Keeping Service](../position-keeping/README.md)
- [Control Plane Service](../control-plane/README.md)
