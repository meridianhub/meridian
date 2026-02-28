---
name: prd-030-asyncapi-specification
description: Adopt AsyncAPI 3.0 to formally specify Meridian's Kafka event contracts alongside existing OpenAPI specs
triggers:

  - Adding or modifying Kafka event topics
  - Defining new event schemas or proto messages in api/proto/meridian/events/
  - Working on event-driven architecture documentation
  - Reviewing or extending the outbox pattern event publishing

instructions: |
  Meridian adopts AsyncAPI 3.0 as the formal specification for all Kafka event contracts,
  mirroring how OpenAPI (Swagger) documents the REST/gRPC API surface. AsyncAPI specs live
  in api/asyncapi/ and are generated from event proto definitions via a build target.

  Key conventions: one spec file per BIAN service domain, topic naming follows the existing
  <service>.<event-name>.<version> convention (ADR-0004), protobuf is the payload format
  with JSON Schema derived from proto for the AsyncAPI payload definitions.
---

# PRD-030: AsyncAPI Specification for Kafka Event Contracts

**Author:** Meridian Platform Team
**Status:** Not Started
**Date:** 2026-02-28

---

## 1. Problem Statement

Meridian has a mature event-driven architecture: 7 services publish domain events to Kafka
via the transactional outbox pattern, with protobuf serialization, structured topic naming
(`<service>.<event>.<version>`), and correlation/causation tracing. The sync API surface is
well-documented — `buf generate` produces a merged OpenAPI spec at `api/openapi/meridian.swagger.json`,
served via Swagger UI at `:8091`.

The async API surface has no equivalent formal specification. Event contracts exist only as:

- Protobuf message definitions in `api/proto/meridian/events/v1/`
- Topic name constants scattered across service source files
- Prose documentation in ADR-0004

This creates gaps:

- **No single source of truth** for what events exist, which topics carry them, and what
  payloads consumers should expect
- **No machine-readable contract** for tooling (code generation, mock consumers, contract testing)
- **No browseable documentation** equivalent to Swagger UI for the event layer
- **BIAN v14 introduced AsyncAPI 3.0 specs** for all 259 service domains — Meridian should
  adopt the same standard to maintain specification alignment, while retaining its own topic
  naming and serialization choices (per ADR-0004 amendment)

### What Exists Today

| Layer | Sync (gRPC/REST) | Async (Kafka) |
|-------|-------------------|---------------|
| Schema | Proto definitions | Proto definitions |
| Spec format | OpenAPI 2.0 (Swagger) | None |
| Generation | `buf generate` → `meridian.swagger.json` | None |
| UI | Swagger UI at `:8091` | None |
| Per-service split | `make swagger-split` | None |

### What This PRD Delivers

| Layer | Async (Kafka) — After |
|-------|----------------------|
| Spec format | AsyncAPI 3.0.0 |
| Generation | `make asyncapi` → `api/asyncapi/` |
| UI | AsyncAPI Studio or equivalent |
| Per-service split | One YAML per service domain |

---

## 2. Goals

| # | Goal | Success Metric |
|---|------|----------------|
| G1 | Machine-readable event contracts | AsyncAPI 3.0 YAML for every Kafka topic |
| G2 | Parity with OpenAPI workflow | `make asyncapi` generates specs from proto, analogous to `make proto` |
| G3 | Browseable event documentation | Developers can explore event schemas in a UI |
| G4 | Per-service specification files | One AsyncAPI YAML per BIAN service domain |
| G5 | CI validation | Spec validity checked in CI alongside `buf lint` |
| G6 | BIAN v14 alignment | Adopt the same specification standard BIAN uses for async contracts |

### Non-Goals

- Replacing protobuf with JSON for event serialization (protobuf remains the wire format)
- Adopting BIAN's transport-agnostic channel naming (`OutboundMessage/Created`) — Meridian
  retains `<service>.<event>.<version>` per ADR-0004
- Code generation from AsyncAPI (proto remains the source of truth for Go types)
- External consumer portal or developer portal (internal documentation only)
- AsyncAPI-based contract testing (future consideration, not in scope)

---

## 3. Architecture

### Generation Pipeline

```text
Proto Event Definitions              Topic Constants
(api/proto/meridian/events/v1/)      (services/*/service/*.go)
         │                                    │
         └──────────┬─────────────────────────┘
                    │
            Generation Script
            (scripts/gen-asyncapi.sh)
                    │
                    ▼
         api/asyncapi/
         ├── current-account.yaml
         ├── financial-accounting.yaml
         ├── market-information.yaml
         ├── payment-order.yaml
         ├── position-keeping.yaml
         ├── reconciliation.yaml
         └── index.yaml (multi-file reference)
```

### Source of Truth

Protobuf event definitions remain the source of truth for payload schemas. The AsyncAPI
specs are **generated artifacts** (like `meridian.swagger.json`), not manually maintained.
This prevents drift between the proto definitions developers write and the async contracts
consumers read.

### Spec Structure (Per Service)

Each service gets one AsyncAPI 3.0 YAML file following this structure:

```yaml
asyncapi: 3.0.0
info:
  title: Position Keeping Events
  version: 1.0.0
  description: >
    Domain events published by the Position Keeping service (BIAN: PositionKeeping).
    Events are published via the transactional outbox pattern to Apache Kafka
    using protobuf serialization.

servers:
  kafka:
    host: kafka:9092
    protocol: kafka
    description: Internal Kafka cluster

channels:
  position-keeping.transaction-captured.v1:
    address: position-keeping.transaction-captured.v1
    messages:
      TransactionCapturedEvent:
        $ref: '#/components/messages/TransactionCapturedEvent'
    description: >
      Published when a new financial transaction is captured against
      a position log. BIAN qualifier: Initiate.

  position-keeping.transaction-posted.v1:
    address: position-keeping.transaction-posted.v1
    messages:
      TransactionPostedEvent:
        $ref: '#/components/messages/TransactionPostedEvent'
    description: >
      Published when a captured transaction is posted (confirmed).
      BIAN qualifier: Execute.

operations:
  publishTransactionCaptured:
    action: send
    channel:
      $ref: '#/channels/position-keeping.transaction-captured.v1'
    summary: Publish transaction captured event
    bindings:
      kafka:
        groupId: position-keeping-service
        partitionKey:
          type: string
          description: Position log ID for ordering guarantee

components:
  messages:
    TransactionCapturedEvent:
      name: TransactionCapturedEvent
      title: Transaction Captured
      contentType: application/protobuf
      payload:
        $ref: '#/components/schemas/TransactionCapturedEvent'
      headers:
        type: object
        properties:
          event_type:
            type: string
            const: position_keeping.transaction_captured.v1
          correlation_id:
            type: string
            format: uuid
          causation_id:
            type: string
            format: uuid
          tenant_id:
            type: string

  schemas:
    TransactionCapturedEvent:
      type: object
      description: >
        Proto: meridian.events.v1.TransactionCapturedEvent
        Source: api/proto/meridian/events/v1/position_keeping_events.proto
      properties:
        event_id:
          type: string
          format: uuid
        position_log_id:
          type: string
        # ... derived from proto field definitions
```

### Kafka Bindings

AsyncAPI supports protocol-specific bindings. Meridian specs include Kafka bindings for:

- **Partition key**: which field determines message ordering (typically aggregate ID)
- **Consumer group**: service name convention
- **Headers**: standard headers (`event_type`, `correlation_id`, `causation_id`, `tenant_id`)

### Relationship to BIAN AsyncAPI

| Aspect | BIAN v14 AsyncAPI | Meridian AsyncAPI |
|--------|-------------------|-------------------|
| Spec version | 3.0.0 | 3.0.0 |
| Channel naming | `OutboundMessage/Created` | `<service>.<event>.<version>` |
| Payload format | JSON (OpenAPI schemas) | Protobuf (JSON Schema derived from proto) |
| Granularity | 2 channels per domain | N channels per domain (one per event type) |
| Transport bindings | None (transport-agnostic) | Kafka bindings (partition keys, headers) |
| Purpose | Reference specification | Operational contract |

Meridian's specs are more operationally specific — they document the actual Kafka topics,
partition strategies, and header conventions that a consumer needs to integrate.

---

## 4. Event Inventory

Current Kafka topics across all services (source: service source code constants):

### Current Account (6 topics)

| Topic | Event Proto | BIAN Qualifier |
|-------|-------------|----------------|
| `current-account.account-frozen.v1` | AccountCreatedEvent (status) | Control |
| `current-account.account-unfrozen.v1` | AccountCreatedEvent (status) | Control |
| `current-account.account-closed.v1` | AccountCreatedEvent (status) | Terminate |
| `current-account.withdrawal-status.v1` | WithdrawalStatusUpdated | Execute |
| `current-account.account-created.v1` | AccountCreatedEvent | Initiate |
| `current-account.balance-posted.v1` | BalancePostedEvent | Update |

### Payment Order (7 topics)

| Topic | Event Proto | BIAN Qualifier |
|-------|-------------|----------------|
| `payment-order.initiated.v1` | PaymentOrderInitiatedEvent | Initiate |
| `payment-order.reserved.v1` | PaymentOrderReservedEvent | Execute |
| `payment-order.executing.v1` | PaymentOrderExecutingEvent | Execute |
| `payment-order.completed.v1` | PaymentOrderCompletedEvent | Execute |
| `payment-order.failed.v1` | PaymentOrderFailedEvent | Execute |
| `payment-order.cancelled.v1` | PaymentOrderCancelledEvent | Control |
| `payment-order.reversed.v1` | PaymentOrderReversedEvent | Control |

### Position Keeping (9 topics)

| Topic | Event Proto | BIAN Qualifier |
|-------|-------------|----------------|
| `position-keeping.transaction-captured.v1` | TransactionCapturedEvent | Initiate |
| `position-keeping.transaction-amended.v1` | TransactionAmendedEvent | Update |
| `position-keeping.transaction-reconciled.v1` | TransactionReconciledEvent | Execute |
| `position-keeping.transaction-posted.v1` | TransactionPostedEvent | Execute |
| `position-keeping.transaction-rejected.v1` | TransactionRejectedEvent | Control |
| `position-keeping.transaction-failed.v1` | TransactionFailedEvent | Control |
| `position-keeping.transaction-cancelled.v1` | TransactionCancelledEvent | Control |
| `position-keeping.bulk-transaction-captured.v1` | BulkTransactionCapturedEvent | Initiate |
| `position-keeping.opening-balance-recorded.v1` | OpeningBalanceRecordedEvent | Initiate |

### Financial Accounting (1 topic)

| Topic | Event Proto | BIAN Qualifier |
|-------|-------------|----------------|
| `financial-accounting.booking-log.controlled` | FinancialBookingLogControlled | Control |

### Market Information (1 topic)

| Topic | Event Proto | BIAN Qualifier |
|-------|-------------|----------------|
| `market-information.observation-recorded.v1` | ObservationRecordedEvent | Initiate |

### Reconciliation (6 topics)

| Topic | Event Proto | BIAN Qualifier |
|-------|-------------|----------------|
| `reconciliation.run-started.v1` | ReconciliationRunStartedEvent | Initiate |
| `reconciliation.run-completed.v1` | ReconciliationRunCompletedEvent | Execute |
| `reconciliation.variance-detected.v1` | VarianceDetectedEvent | Execute |
| `reconciliation.position-lock-requested.v1` | PositionLockRequestedEvent | Request |
| `reconciliation.dispute-created.v1` | DisputeCreatedEvent | Initiate |
| `reconciliation.dispute-resolved.v1` | DisputeResolvedEvent | Execute |

### Party (1 topic)

| Topic | Event Proto | BIAN Qualifier |
|-------|-------------|----------------|
| `party.updated.v1` | PartyUpdatedEvent | Update |

Total: ~31 topics across 7 services.

---

## 5. Implementation

### Work Stream 1: Generation Tooling (3 points)

#### Task 1.1: Proto-to-AsyncAPI generation script

Create `scripts/gen-asyncapi.sh` that:

1. Reads event proto files from `api/proto/meridian/events/v1/`
2. Reads topic constants from service source files (or a topic registry file)
3. Generates one AsyncAPI 3.0 YAML per service domain
4. Outputs to `api/asyncapi/`

**Approach options:**

- **Option A (recommended)**: Use `protoc-gen-jsonschema` to derive JSON Schema from proto
  messages, then template AsyncAPI YAML around those schemas. Keeps proto as single source
  of truth.
- **Option B**: Use a Go program that reads proto descriptors (`descriptor.binpb`) and
  emits AsyncAPI YAML. More type-safe but heavier tooling.
- **Option C**: Maintain a `topics.yaml` registry mapping topics to proto messages, and
  generate AsyncAPI from that. Simple but introduces a second source of truth for topic names.

#### Task 1.2: Topic registry

Consolidate topic name constants (currently scattered across service source files) into a
single registry that the generation script and services both reference. This could be:

- A Go package (`shared/platform/events/topics/topics.go`) with exported constants
- A YAML file (`api/asyncapi/topics.yaml`) read by both the generator and a Go embed

#### Task 1.3: Makefile integration

Add `make asyncapi` target:

```makefile
.PHONY: asyncapi
asyncapi: ## Generate AsyncAPI specs from proto definitions
    @echo "Generating AsyncAPI specifications..."
    @./scripts/gen-asyncapi.sh
    @echo "AsyncAPI specs generated at api/asyncapi/"
```

Consider adding to `make proto` so AsyncAPI specs regenerate alongside OpenAPI.

### Work Stream 2: Spec Files (2 points)

#### Task 2.1: Generate initial specs for all 7 services

Run the generation tooling against the current event protos and topic constants to produce
the initial set of AsyncAPI YAML files.

#### Task 2.2: Validate specs

Use the [AsyncAPI CLI](https://www.asyncapi.com/tools/cli) to validate generated specs:

```bash
asyncapi validate api/asyncapi/position-keeping.yaml
```

#### Task 2.3: Add Kafka bindings

Enrich generated specs with Kafka-specific bindings:

- Partition key fields (from service publisher code)
- Standard headers (`event_type`, `correlation_id`, `causation_id`, `tenant_id`)
- Consumer group naming convention

### Work Stream 3: Documentation UI (2 points)

#### Task 3.1: AsyncAPI docs generation

Generate browseable HTML documentation from the specs. Options:

- **AsyncAPI Studio** (web-based editor/viewer)
- **AsyncAPI Generator** with HTML template (`@asyncapi/html-template`)
- **Embed in existing docs** alongside Swagger UI

#### Task 3.2: Makefile target for docs

```makefile
.PHONY: asyncapi-ui
asyncapi-ui: ## Serve AsyncAPI documentation
    @npx @asyncapi/studio api/asyncapi/position-keeping.yaml
```

Or generate static HTML:

```makefile
.PHONY: asyncapi-docs
asyncapi-docs: ## Generate AsyncAPI HTML documentation
    @npx @asyncapi/cli generate fromTemplate api/asyncapi/index.yaml @asyncapi/html-template -o api/asyncapi/docs/
```

### Work Stream 4: CI Integration (1 point)

#### Task 4.1: AsyncAPI validation in CI

Add AsyncAPI spec validation to the GitHub Actions workflow alongside existing `buf lint`:

```yaml
- name: Validate AsyncAPI specs
  run: npx @asyncapi/cli validate api/asyncapi/*.yaml
```

#### Task 4.2: Drift detection

Optionally, regenerate specs in CI and fail if the committed specs differ from what the
generator produces (same pattern as checking if generated Go code is up to date).

---

## 6. Story Point Summary

| Work Stream | Points | Description |
|-------------|--------|-------------|
| WS1: Generation Tooling | 3 | Script, topic registry, Makefile |
| WS2: Spec Files | 2 | Initial generation, validation, Kafka bindings |
| WS3: Documentation UI | 2 | Browseable docs, serve target |
| WS4: CI Integration | 1 | Validation, drift detection |
| **Total** | **8** | |

**Critical path:** WS1 → WS2 → WS3 (WS4 can parallel with WS3)

**Dependencies:**

- No external service dependencies
- Requires Node.js for AsyncAPI CLI tooling (already available for buf)
- No changes to existing proto definitions or Kafka infrastructure

---

## 7. Success Criteria

- [ ] `make asyncapi` generates valid AsyncAPI 3.0 YAML for all 7 services
- [ ] Every Kafka topic in the event inventory has a corresponding channel definition
- [ ] Payload schemas are derived from proto definitions (not manually duplicated)
- [ ] Developers can browse event documentation in a UI (locally served)
- [ ] CI validates AsyncAPI spec correctness on every PR
- [ ] ADR-0004 references AsyncAPI specs as the canonical event contract documentation

---

## 8. Related Documents

- [ADR-0004: Event Schema Evolution](../adr/0004-event-schema-evolution.md) — topic naming,
  outbox pattern, idempotency
- [ADR-0005: Adapter Pattern](../adr/0005-adapter-pattern-layer-translation.md) — layer
  translation between BIAN models and internal schemas
- [PRD-025: Real-Time Event Streaming](025-real-time-event-streaming.md) — WebSocket delivery
  of events to the operations console
- [BIAN v14 AsyncAPI Specs](https://github.com/bian-official/public/tree/main/release14.0.0/semantic-apis/asyncapi) —
  BIAN's reference AsyncAPI definitions
- [AsyncAPI 3.0 Specification](https://www.asyncapi.com/docs/reference/specification/v3.0.0)
