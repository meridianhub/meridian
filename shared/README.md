# shared/

This directory contains code shared across Meridian services. It is split into two
sub-directories with distinct responsibilities.

---

## pkg/ vs platform/

| | `pkg/` | `platform/` |
|---|---|---|
| **Contains** | Domain-level shared types, algorithms, and utilities | Infrastructure wrappers and cross-cutting concerns |
| **Depends on** | `shared/platform/quantity`, standard library, domain primitives | External libraries (gRPC, Kafka, Redis, OpenTelemetry, GORM) |
| **Domain semantics** | Yes — models BIAN/Meridian concepts (Money, Amount, Saga, etc.) | No — generic infrastructure (connection pools, locks, auth) |
| **Imported by** | Services and other `shared/` packages | Services and `shared/pkg/` |

### Decision Guide

Place new code in **`pkg/`** if it:

- Encodes a domain concept (e.g., a new quantity dimension or settlement type)
- Is a utility tightly coupled to domain types (e.g., amount arithmetic)
- Must remain independent of infrastructure concerns for testability

Place new code in **`platform/`** if it:

- Wraps or configures an external dependency (database, message broker, cache, auth)
- Provides cross-cutting infrastructure with no domain meaning (health checks, rate limiting, sandboxing)
- Is consumed identically by all services regardless of domain context

---

## shared/pkg/

Domain-level shared packages.

| Package | Description |
|---|---|
| `amount` | Dimension-agnostic Amount type (CURRENCY, ENERGY, CARBON, COMPUTE, etc.) |
| `bucketing` | Canonical bucket ID calculation for the Universal Asset System |
| `cel` | CEL expression compilation and evaluation for validation and key generation |
| `clients` | Resilience patterns for inter-service calls (circuit breaker, retry, saga) |
| `credentials` | Password hashing, validation, and policy enforcement |
| `dispatch` | Event dispatch utilities for publishing domain events |
| `email` | Transactional email delivery; template rendering, outbox persistence, per-tenant preference enforcement, and suppression management |
| `grpc` | gRPC client connection factory with DNS-based load balancing |
| `health` | Health-check aggregator and HTTP handler for Kubernetes probes |
| `idempotency` | Distributed idempotency checking and locking |
| `interceptors` | Shared gRPC interceptors (logging, tracing, error mapping) |
| `mapping` | Bidirectional JSON-to-gRPC payload transformation engine |
| `money` | Instrument-aware Money type restricted to the CURRENCY dimension ([Precision Guarantees](domain/money/PRECISION_GUARANTEES.md)) |
| `proto/mappers` | Conversion utilities between domain types and protobuf types |
| `refdata` | InstrumentResolver interface and caching implementations |
| `saga` | Saga orchestration runtime and persistence for durable execution |
| `tokens` | Secure single-use token generation, hashing, and validation |
| `types` | Shared primitive types (e.g., strongly-typed identifiers) |
| `validation` | Input validation utilities mirroring proto validation rules |
| `valuation` | Engine interface and implementations for Starlark/CEL valuation methods |
| `valuationfeature` | Shared domain, persistence, and CRUD for valuation feature assignments |

---

## shared/platform/

Infrastructure packages with no domain semantics.

| Package | Description |
|---|---|
| `audit` | Application-level audit logging via transactional outbox and Kafka |
| `auth` | JWT authentication middleware for gRPC (JWKS, OAuth, disabled modes) |
| `await` | Test polling utility — replaces `time.Sleep` with condition-based waits |
| `bootstrap` | Shared infrastructure initialisation (DB, Redis, gRPC, auth, observability) ([README](platform/bootstrap/README.md)) |
| `db` | Database abstraction layer for CockroachDB (connection pool + transaction interface) |
| `defaults` | Shared default configuration values (timeouts, retry counts, etc.) |
| `env` | Typed environment variable helpers (`MustGetString`, `GetInt`, etc.) |
| `events` | Transactional outbox pattern for reliable event delivery to Kafka |
| `gateway` | HTTP middleware for API gateway tenant resolution |
| `kafka` | Protocol Buffer Kafka consumer and producer utilities |
| `observability` | OpenTelemetry tracing setup and gRPC instrumentation helpers ([README](platform/observability/README.md)) |
| `ports` | Centralized port constant definitions for all Meridian services |
| `quantity` | Generic `Qty[D]` type for the Universal Asset System with dimension safety |
| `ratelimit` | Per-tenant, per-method gRPC rate limiting with Prometheus metrics |
| `redislock` | Distributed locking and leader election backed by Redis |
| `sandbox` | Starlark security configuration and thread hardening for tenant scripts |
| `scheduler` | Cron-based and catch-up job scheduling for tenant workflows |
| `tenant` | Tenant identity types and context propagation helpers |
| `testdb` | CockroachDB testcontainer setup for integration tests |
