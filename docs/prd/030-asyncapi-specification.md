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

  This PRD also addresses event publishing tech debt: aligning all services to the
  transactional outbox pattern, adding missing domain events for services that
  currently publish none, and generating type-safe event publishers from the
  AsyncAPI specs so that invalid events cannot compile.
---

# PRD-030: AsyncAPI Specification for Kafka Event Contracts

**Author:** Meridian Platform Team
**Status:** Not Started
**Date:** 2026-02-28

---

## 1. Problem Statement

Meridian's event-driven architecture has two categories of debt that this PRD addresses together.

### 1.1 No Formal Async API Specification

The sync API surface is well-documented — `buf generate` produces a merged OpenAPI spec at
`api/openapi/meridian.swagger.json`, served via Swagger UI at `:8091`. The async API surface
has no equivalent. Event contracts exist only as:

- Protobuf message definitions in `api/proto/meridian/events/v1/`
- Topic name constants scattered across service source files
- Prose documentation in ADR-0004

This means no single source of truth, no machine-readable contract for tooling, and no
browseable documentation for event consumers.

BIAN v14 introduced AsyncAPI 3.0 specs for all 259 service domains. Meridian should adopt
the same standard while retaining its own topic naming and serialization choices
(per ADR-0004 amendment).

### 1.2 Inconsistent Event Publishing Patterns

An audit of all services reveals that event publishing is inconsistent across the platform:

| Service | Outbox Pattern | Publishing Method | Transactional Guarantee |
|---------|---------------|-------------------|------------------------|
| financial-accounting | Full | OutboxPublisher within DB tx | Yes |
| current-account | Infra present, unused | Direct Kafka (fire-and-forget) | No |
| position-keeping | Infra present, unused | Direct Kafka (fire-and-forget) | No |
| payment-order | None | Direct Kafka (fire-and-forget) | No |
| market-information | None | Direct Kafka (fire-and-forget) | No |
| reconciliation | None | Direct Kafka (fire-and-forget) | No |
| control-plane | None | Direct Kafka (Stripe webhooks) | No |
| party | None | No events published | N/A |
| internal-account | None | No events published | N/A |
| audit-worker | N/A | Consumer only | N/A |
| gateway | N/A | Consumer only (event distribution) | N/A |

Only `financial-accounting` uses the transactional outbox pattern (ADR-0004 amendment).
All other publishers use fire-and-forget Kafka writes outside the database transaction,
risking event loss on service crash. Two core BIAN services (Party, InternalBankAccount)
publish no domain events at all.

### 1.3 No Type Safety in Event Publishing

An audit of the event publishing code path reveals that every layer is string-typed with
no compile-time enforcement:

| Layer | Mechanism | Type Safe? | Risk |
|-------|-----------|-----------|------|
| Publisher API | `PublishConfig{EventType, Topic}` strings | No | Typo in EventType or Topic → silent misrouting |
| Outbox Table | All string columns, `[]byte` payload | No | Invalid data persists in DB unchecked |
| Domain Events | `EventType() string`, `ToProto() interface{}` | No | String return + untyped interface → runtime errors |
| Topic Routing | `map[string]string` lookup | No | Missing key → empty topic → silent failure |
| Proto Validation | `protovalidate` imported but never called | No | Validation rules defined in proto but unenforced |
| Error Handling | `_ = publish()` in some services | No | Silent event loss, zero observability |

The `protovalidate` annotations on event proto messages (required fields, UUID formats,
string patterns) are compiled into the generated Go code but **never invoked** in the
publish path. Events can be written to the outbox with invalid payloads.

Position-keeping service silently discards publish errors (`_ = s.eventPublisher.Publish()`),
meaning business operations complete successfully but downstream services never receive
the event.

The core issue: the publish path is a chain of `string → string → []byte` with no
structural guarantee that the right event reaches the right topic with a valid payload.
This is the opposite of Correctness by Construction.

### 1.4 Topic Naming Inconsistency

One topic does not follow the `<service>.<event>.<version>` convention from ADR-0004:

- `financial-accounting.booking-log.controlled` — missing `.v1` suffix (legacy exception)

Additionally, some services still dual-publish to deprecated topic names during migration
(reconciliation, market-information).

### What Exists Today

| Layer | Sync (gRPC/REST) | Async (Kafka) |
|-------|-------------------|---------------|
| Schema | Proto definitions | Proto definitions |
| Spec format | OpenAPI 2.0 (Swagger) | None |
| Generation | `buf generate` → `meridian.swagger.json` | None |
| UI | Swagger UI at `:8091` | None |
| Per-service split | `make swagger-split` | None |
| Publishing pattern | N/A | Inconsistent (1 of 7 publishers uses outbox) |

### What This PRD Delivers

| Layer | Async (Kafka) — After |
|-------|----------------------|
| Spec format | AsyncAPI 3.0.0 |
| Generation | `make asyncapi` → `api/asyncapi/` |
| UI | AsyncAPI Studio or equivalent |
| Per-service split | One YAML per service domain |
| Publishing pattern | All services use transactional outbox |
| Type safety | Generated typed publishers per domain |
| Payload validation | `protovalidate` enforced at publish boundary |
| Contract enforcement | CI: `asyncapi diff` + drift detection |

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
| G7 | Consistent event publishing | All event-publishing services use the transactional outbox pattern |
| G8 | Event coverage | Party and InternalBankAccount services publish domain events |
| G9 | Type-safe publishing | Generated typed publishers eliminate string-based topic/event routing |
| G10 | Payload validation | `protovalidate` enforced before outbox write — invalid events rejected at boundary |
| G11 | Contract enforcement | CI detects breaking async contract changes via `asyncapi diff` |

### Non-Goals

- Replacing protobuf with JSON for event serialization (protobuf remains the wire format)
- Adopting BIAN's transport-agnostic channel naming (`OutboundMessage/Created`) — Meridian
  retains `<service>.<event>.<version>` per ADR-0004
- External consumer portal or developer portal (internal documentation only)
- Implementing all 82 BIAN AsyncAPI channels — only channels matching existing service
  operations are in scope

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
         ├── internal-bank-account.yaml
         ├── market-information.yaml
         ├── party.yaml
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
| Granularity | 2-3 channels per BQ | N channels per domain (one per event type) |
| Transport bindings | None (transport-agnostic) | Kafka bindings (partition keys, headers) |
| Purpose | Reference specification | Operational contract |

Meridian's specs are more operationally specific — they document the actual Kafka topics,
partition strategies, and header conventions that a consumer needs to integrate.

---

## 4. Event Inventory

### 4.1 Current Topics (Verified from Source Code)

Topics verified against service source code constants as of 2026-03-01.

#### Current Account (4 topics)

| Topic | Event Proto | BIAN Qualifier | Source |
|-------|-------------|----------------|--------|
| `current-account.account-frozen.v1` | AccountCreatedEvent (status) | Control | `service/grpc_service.go:71` |
| `current-account.account-unfrozen.v1` | AccountCreatedEvent (status) | Control | `service/grpc_service.go:73` |
| `current-account.account-closed.v1` | AccountCreatedEvent (status) | Terminate | `service/grpc_service.go:75` |
| `current-account.withdrawal-status.v1` | WithdrawalStatusUpdated | Execute | `service/grpc_withdrawal_execute.go:394` |

#### Payment Order (7 topics)

| Topic | Event Proto | BIAN Qualifier | Source |
|-------|-------------|----------------|--------|
| `payment-order.initiated.v1` | PaymentOrderInitiatedEvent | Initiate | `service/grpc_service.go:49` |
| `payment-order.reserved.v1` | PaymentOrderReservedEvent | Execute | `service/grpc_service.go:50` |
| `payment-order.executing.v1` | PaymentOrderExecutingEvent | Execute | `service/grpc_service.go:51` |
| `payment-order.completed.v1` | PaymentOrderCompletedEvent | Execute | `service/grpc_service.go:52` |
| `payment-order.failed.v1` | PaymentOrderFailedEvent | Execute | `service/grpc_service.go:53` |
| `payment-order.cancelled.v1` | PaymentOrderCancelledEvent | Control | `service/grpc_service.go:54` |
| `payment-order.reversed.v1` | PaymentOrderReversedEvent | Control | `service/grpc_service.go:55` |

#### Position Keeping (9 topics)

| Topic | Event Proto | BIAN Qualifier | Source |
|-------|-------------|----------------|--------|
| `position-keeping.transaction-captured.v1` | TransactionCapturedEvent | Initiate | `adapters/messaging/kafka_event_publisher.go:63` |
| `position-keeping.transaction-amended.v1` | TransactionAmendedEvent | Update | `kafka_event_publisher.go:64` |
| `position-keeping.transaction-reconciled.v1` | TransactionReconciledEvent | Execute | `kafka_event_publisher.go:65` |
| `position-keeping.transaction-posted.v1` | TransactionPostedEvent | Execute | `kafka_event_publisher.go:66` |
| `position-keeping.transaction-rejected.v1` | TransactionRejectedEvent | Control | `kafka_event_publisher.go:67` |
| `position-keeping.transaction-failed.v1` | TransactionFailedEvent | Control | `kafka_event_publisher.go:68` |
| `position-keeping.transaction-cancelled.v1` | TransactionCancelledEvent | Control | `kafka_event_publisher.go:69` |
| `position-keeping.bulk-transaction-captured.v1` | BulkTransactionCapturedEvent | Initiate | `kafka_event_publisher.go:70` |
| `position-keeping.opening-balance-recorded.v1` | OpeningBalanceRecordedEvent | Initiate | `kafka_event_publisher.go:71` |

#### Financial Accounting (1 topic)

| Topic | Event Proto | BIAN Qualifier | Source |
|-------|-------------|----------------|--------|
| `financial-accounting.booking-log.controlled` | FinancialBookingLogControlled | Control | `service/grpc_control_endpoints.go:197` |

Note: This topic is a legacy exception to the `<service>.<event>.<version>` naming convention
(missing `.v1` suffix). It should be migrated to `financial-accounting.booking-log-controlled.v1`
with dual-publishing during transition.

#### Market Information (1 topic)

| Topic | Event Proto | BIAN Qualifier | Source |
|-------|-------------|----------------|--------|
| `market-information.observation-recorded.v1` | ObservationRecordedEvent | Initiate | `service/event_publisher.go:19` |

Also dual-publishes to deprecated topic `meridian.market_information.v1.ObservationRecorded`.

#### Reconciliation (6 topics)

| Topic | Event Proto | BIAN Qualifier | Source |
|-------|-------------|----------------|--------|
| `reconciliation.run-started.v1` | ReconciliationRunStartedEvent | Initiate | `adapters/messaging/kafka_publisher.go:19` |
| `reconciliation.run-completed.v1` | ReconciliationRunCompletedEvent | Execute | `kafka_publisher.go:20` |
| `reconciliation.variance-detected.v1` | VarianceDetectedEvent | Execute | `kafka_publisher.go:21` |
| `reconciliation.position-lock-requested.v1` | PositionLockRequestedEvent | Request | `kafka_publisher.go:22` |
| `reconciliation.dispute-created.v1` | DisputeCreatedEvent | Initiate | `kafka_publisher.go:23` |
| `reconciliation.dispute-resolved.v1` | DisputeResolvedEvent | Execute | `kafka_publisher.go:24` |

Also dual-publishes to deprecated topic names (`reconciliation.run.started`, etc.).

#### Audit (1 topic + DLQ)

| Topic | Event Proto | BIAN Qualifier | Source |
|-------|-------------|----------------|--------|
| `audit.events.v1` | AuditEvent | N/A (platform) | `shared/platform/kafka/config.go` |
| `audit.events.v1.dlq` | AuditEvent (failed) | N/A (platform) | `shared/platform/kafka/config.go` |

#### Party (0 topics)

No Kafka publisher implemented. Proto defines `PartyVerificationCompletedEvent` but no
topic constant or publisher wiring exists.

#### Internal Bank Account (0 topics)

No event publishing infrastructure.

#### Current totals: 29 active topics across 7 publishing services

### 4.2 BIAN v14 AsyncAPI Channel Coverage

BIAN v14 defines AsyncAPI channels per Behavior Qualifier (BQ). This table maps BIAN
channels to Meridian's existing events and identifies gaps where adding events would be
straightforward based on existing service operations.

| BIAN Domain | BIAN Channels | Meridian Topics | Coverage | Key Gaps |
|-------------|---------------|-----------------|----------|----------|
| CurrentAccount | 20 (10 BQs x Created/Updated) | 4 | 20% | Account Created/Updated, Deposit, Payment, Interest, Charge |
| FinancialAccounting | 4 (2 BQs) | 1 | 25% | BookingLog Created/Updated, LedgerPosting Created |
| PaymentOrderInitiation | 8 (4 BQs) | 7 | 87% | Compliance events |
| PositionKeeping | 4 (2 BQs) | 9 | 100%+ | Exceeds BIAN (more granular) |
| MarketInformationManagement | 16 (8 BQs across 2 files) | 1 | 6% | Feed, Distribution, Reporting events |
| PartyReferenceDataDirectory | 10 (5 BQs) | 0 | 0% | All party lifecycle events |
| InternalBankAccount | 5 (3 BQs incl. Notify) | 0 | 0% | Facility and Booking events |
| AccountReconciliation | 15 (9 BQs incl. Notify) | 6 | 40% | Assessment, Resolution workflow events |

Meridian total: 29 topics. BIAN total for these domains: 82 channels.

Not all 82 channels need implementing — many BIAN BQs map to operations Meridian doesn't
perform (e.g., IssuedDevice, Sweep). The gap analysis below identifies events that are
low-hanging fruit based on existing service capabilities.

### 4.3 Recommended New Events (Low-Hanging Fruit)

These events map directly to existing Meridian service operations that currently execute
without publishing domain events:

| Service | Proposed Topic | Trigger | Effort |
|---------|---------------|---------|--------|
| current-account | `current-account.account-created.v1` | Account initiation (already exists in gRPC) | Low |
| current-account | `current-account.account-updated.v1` | Account status/detail changes | Low |
| current-account | `current-account.deposit-completed.v1` | Deposit processing | Low |
| party | `party.created.v1` | Party registration | Low |
| party | `party.updated.v1` | Party detail changes | Low |
| party | `party.verification-completed.v1` | KYC/AML verification (proto exists) | Low |
| internal-account | `internal-account.facility-created.v1` | Account facility initiation | Low |
| internal-account | `internal-account.booking-created.v1` | Posting to internal account | Low |
| financial-accounting | `financial-accounting.booking-log-created.v1` | New booking log initiation | Low |
| financial-accounting | `financial-accounting.ledger-posting-created.v1` | New ledger posting captured | Low |

These 10 new topics would bring coverage from 29 to 39 topics and close the most visible
gaps (Party 0% → basic lifecycle, InternalBankAccount 0% → facility events,
CurrentAccount 20% → core operations).

---

## 5. Implementation

### Work Stream 1: Outbox Pattern Alignment (5 points)

Prerequisite for accurate AsyncAPI specs — services must publish events consistently
before we can document the contracts.

#### Task 1.1: Migrate current-account to OutboxPublisher

Current state: has outbox infrastructure (repository, worker) but publishes directly to
Kafka via `PublishWithTenant()`. Migrate control endpoint events (frozen/unfrozen/closed)
and withdrawal events to use `OutboxPublisher` within the database transaction.

#### Task 1.2: Migrate position-keeping to OutboxPublisher

Current state: has outbox infrastructure but publishes via `KafkaEventPublisher`
fire-and-forget. Migrate all transaction lifecycle events to use `OutboxPublisher`.

#### Task 1.3: Migrate payment-order to OutboxPublisher

Current state: no outbox infrastructure. Add `OutboxPublisher` dependency, outbox worker,
and migrate all 7 topic publications to transactional pattern.

#### Task 1.4: Migrate market-information to OutboxPublisher

Current state: custom `KafkaObservationPublisher` with direct Kafka writes. Migrate to
`OutboxPublisher`. Remove deprecated dual-publishing to old topic name.

#### Task 1.5: Migrate reconciliation to OutboxPublisher

Current state: custom `KafkaPublisher` with direct writes. Migrate all 6 topics. Remove
deprecated dual-publishing to old topic names.

#### Task 1.6: Fix financial-accounting topic naming

Rename `financial-accounting.booking-log.controlled` to
`financial-accounting.booking-log-controlled.v1` with dual-publishing during transition.

### Work Stream 2: Missing Event Publishers (3 points)

Add event publishing to services that currently have none.

#### Task 2.1: Party service event publishing

Add `OutboxPublisher` to the party service. Implement:

- `party.created.v1` — published on party registration
- `party.updated.v1` — published on party detail changes
- `party.verification-completed.v1` — published on KYC/AML completion (proto already
  defines `PartyVerificationCompletedEvent`)

#### Task 2.2: Internal bank account event publishing

Add `OutboxPublisher` to the internal-account service. Implement:

- `internal-account.facility-created.v1` — published on account initiation
- `internal-account.booking-created.v1` — published on posting to internal account

#### Task 2.3: Current account additional events

Add events for operations that currently execute silently:

- `current-account.account-created.v1` — published on account initiation
- `current-account.account-updated.v1` — published on account detail changes
- `current-account.deposit-completed.v1` — published on deposit processing

#### Task 2.4: Financial accounting additional events

Add events for core operations:

- `financial-accounting.booking-log-created.v1` — published on new booking log
- `financial-accounting.ledger-posting-created.v1` — published on new posting

### Work Stream 3: AsyncAPI Generation Tooling (3 points)

#### Task 3.1: Proto-to-AsyncAPI generation script

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

#### Task 3.2: Topic registry

Consolidate topic name constants (currently scattered across service source files) into a
single registry that the generation script and services both reference. This could be:

- A Go package (`shared/platform/events/topics/topics.go`) with exported constants
- A YAML file (`api/asyncapi/topics.yaml`) read by both the generator and a Go embed

#### Task 3.3: Makefile integration

Add `make asyncapi` target:

```makefile
.PHONY: asyncapi
asyncapi: ## Generate AsyncAPI specs from proto definitions
    @echo "Generating AsyncAPI specifications..."
    @./scripts/gen-asyncapi.sh
    @echo "AsyncAPI specs generated at api/asyncapi/"
```

Consider adding to `make proto` so AsyncAPI specs regenerate alongside OpenAPI.

### Work Stream 4: Spec Files (2 points)

#### Task 4.1: Generate initial specs for all 8 services

Run the generation tooling against the current event protos and topic constants to produce
the initial set of AsyncAPI YAML files for all 8 event-producing BIAN service domains.

#### Task 4.2: Validate specs

Use the [AsyncAPI CLI](https://www.asyncapi.com/tools/cli) to validate generated specs:

```bash
asyncapi validate api/asyncapi/position-keeping.yaml
```

#### Task 4.3: Add Kafka bindings

Enrich generated specs with Kafka-specific bindings:

- Partition key fields (from service publisher code)
- Standard headers (`event_type`, `correlation_id`, `causation_id`, `tenant_id`)
- Consumer group naming convention

### Work Stream 5: Documentation UI (2 points)

#### Task 5.1: AsyncAPI docs generation

Generate browseable HTML documentation from the specs. Options:

- **AsyncAPI Studio** (web-based editor/viewer)
- **AsyncAPI Generator** with HTML template (`@asyncapi/html-template`)
- **Embed in existing docs** alongside Swagger UI

#### Task 5.2: Makefile target for docs

```makefile
.PHONY: asyncapi-ui
asyncapi-ui: ## Serve AsyncAPI documentation
    @npx -p @asyncapi/cli asyncapi start studio api/asyncapi/position-keeping.yaml
```

Or generate static HTML:

```makefile
.PHONY: asyncapi-docs
asyncapi-docs: ## Generate AsyncAPI HTML documentation
    @npx @asyncapi/cli generate fromTemplate api/asyncapi/index.yaml \
      @asyncapi/html-template -o api/asyncapi/docs/
```

### Work Stream 6: CI Integration (2 points)

#### Task 6.1: AsyncAPI validation in CI

Add AsyncAPI spec validation to the GitHub Actions workflow alongside existing `buf lint`:

```yaml
- name: Validate AsyncAPI specs
  run: npx @asyncapi/cli validate api/asyncapi/*.yaml
```

#### Task 6.2: Drift detection

Regenerate specs in CI and fail if the committed specs differ from what the generator
produces (same pattern as checking if generated Go code is up to date).

#### Task 6.3: Breaking change detection

Use [AsyncAPI Diff](https://github.com/asyncapi/diff) to detect breaking changes
to event contracts in PRs, analogous to `buf breaking` for proto schemas:

```yaml
- name: Check for breaking async contract changes
  run: npx @asyncapi/diff api/asyncapi/old/ api/asyncapi/ --breaking-only
```

### Work Stream 7: Type-Safe Event Publishing (5 points)

Transform AsyncAPI from a documentation tool into an enforcement mechanism. The goal is
Correctness by Construction: invalid events should not compile, not just fail at runtime.

#### Design Philosophy

The current publish path is a chain of untyped operations:

```text
Developer writes string literals
    → PublishConfig{EventType: "string", Topic: "string"}
        → OutboxEntry{EventPayload: []byte}
            → Kafka record
```

The target is a fully typed pipeline where the AsyncAPI spec drives code generation:

```text
AsyncAPI spec (reviewed contract)
    → Code generation (asyncapi-codegen or custom)
        → Typed publisher per service domain
            → protovalidate at outbox boundary
                → Proven-valid event in outbox
                    → Kafka record
```

#### Task 7.1: Generated typed event publishers

Generate a typed publisher interface per service domain from the AsyncAPI spec. Each
event gets a dedicated publish method that locks topic, event type, and payload type
together at compile time.

```go
// GENERATED from api/asyncapi/position-keeping.yaml
// DO NOT EDIT

package positionkeepingevents

// Publisher provides type-safe event publishing for the Position Keeping domain.
// Each method publishes to a specific Kafka topic via the transactional outbox,
// with protovalidate enforcement on the payload.
type Publisher struct {
    outbox *events.OutboxPublisher
}

// PublishTransactionCaptured publishes a TransactionCapturedEvent to
// topic "position-keeping.transaction-captured.v1".
//
// The event is validated via protovalidate before writing to the outbox.
// Returns an error if validation fails — the event is never persisted.
func (p *Publisher) PublishTransactionCaptured(
    ctx context.Context,
    tx *gorm.DB,
    event *eventsv1.TransactionCapturedEvent,
    opts ...PublishOption,
) error {
    if err := protovalidate.Validate(event); err != nil {
        return fmt.Errorf("invalid TransactionCapturedEvent: %w", err)
    }
    return p.outbox.Publish(ctx, tx, event, events.PublishConfig{
        EventType:     "position_keeping.transaction_captured.v1",
        Topic:         "position-keeping.transaction-captured.v1",
        AggregateType: "FinancialPositionLog",
        AggregateID:   event.GetPositionLogId(),
    })
}

// PublishTransactionPosted publishes a TransactionPostedEvent to
// topic "position-keeping.transaction-posted.v1".
func (p *Publisher) PublishTransactionPosted(
    ctx context.Context,
    tx *gorm.DB,
    event *eventsv1.TransactionPostedEvent,
    opts ...PublishOption,
) error { ... }
```

This eliminates the entire class of string-typo bugs. A developer cannot:

- Publish a `TransactionCaptured` event to the wrong topic (method enforces it)
- Use the wrong event type string (hardcoded in generated code)
- Pass the wrong proto message type (compiler rejects it)
- Publish an invalid payload (protovalidate rejects it before outbox write)

#### Task 7.2: Protovalidate enforcement at publish boundary

Add `protovalidate.Validate()` call in the generated publishers (as shown above) and
in the `OutboxPublisher.Publish()` method as a safety net. This ensures that even if
a service bypasses the generated publisher, the outbox itself rejects invalid payloads.

```go
// shared/platform/events/publisher.go
func (p *OutboxPublisher) Publish(ctx context.Context, tx *gorm.DB,
    msg proto.Message, config PublishConfig) error {
    // Boundary validation: reject invalid proto messages before persistence
    if err := protovalidate.Validate(msg); err != nil {
        return fmt.Errorf("event payload validation failed: %w", err)
    }
    // ... existing outbox write logic
}
```

This is the "Parse, don't validate" principle: once an event is in the outbox, it is
structurally proven to be valid. Consumers never see invalid payloads.

#### Task 7.3: Eliminate silent publish errors

Audit all services for `_ = publish()` patterns and replace with proper error
propagation. Event publishing in a financial platform must be observable:

- Return errors to callers (not discard with `_`)
- Log publish failures with structured fields (event type, aggregate ID)
- Add `event.publish.error` metric for monitoring

#### Task 7.4: Service migration to generated publishers

Migrate each service from string-based `PublishConfig` to the generated typed publisher:

| Service | Current | Target |
|---------|---------|--------|
| financial-accounting | `OutboxPublisher` + string config | Generated `financialaccountingevents.Publisher` |
| current-account | Direct Kafka + string config | Generated `currentaccountevents.Publisher` |
| position-keeping | `KafkaEventPublisher` + string map | Generated `positionkeepingevents.Publisher` |
| payment-order | Direct Kafka + string config | Generated `paymentorderevents.Publisher` |
| market-information | Custom publisher + string config | Generated `marketinformationevents.Publisher` |
| reconciliation | Custom publisher + string config | Generated `reconciliationevents.Publisher` |
| party | None | Generated `partyevents.Publisher` |
| internal-account | None | Generated `internalbankaccountevents.Publisher` |

After migration, the string-based `PublishConfig` fields in services become internal
implementation details of the generated code — developers never construct them manually.

#### Task 7.5: Code generation tooling

Evaluate and implement the generation approach:

- **Option A**: [asyncapi-codegen](https://github.com/lerenn/asyncapi-codegen) — existing
  Go code generator supporting AsyncAPI 3.0 and Kafka. Generates typed publishers and
  subscribers. May need adaptation for protobuf payloads (currently JSON-focused).
- **Option B**: Custom Go generator reading AsyncAPI YAML + proto descriptors. More
  control over output, direct protobuf integration, but more development effort.
- **Option C**: Template-based generation using `text/template` with AsyncAPI parsed
  as input. Middle ground — uses AsyncAPI as source of truth with custom Go output.

Whichever approach, integrate into `make asyncapi` so generated publishers regenerate
alongside specs. Add generated files to version control (like `.pb.go` files) with
`// Code generated ... DO NOT EDIT` headers.

---

## 6. Story Point Summary

| Work Stream | Points | Description |
|-------------|--------|-------------|
| WS1: Outbox Pattern Alignment | 5 | Migrate 5 services + fix topic naming |
| WS2: Missing Event Publishers | 3 | Party, InternalBankAccount, additional CA/FA events |
| WS3: AsyncAPI Generation Tooling | 3 | Script, topic registry, Makefile |
| WS4: Spec Files | 2 | Initial generation, validation, Kafka bindings |
| WS5: Documentation UI | 2 | Browseable docs, serve target |
| WS6: CI Integration | 2 | Validation, drift detection, breaking change detection |
| WS7: Type-Safe Event Publishing | 5 | Generated publishers, protovalidate, error propagation |
| **Total** | **22** | |

**Dependency graph:**

```text
WS1 (Outbox Alignment) ──┐
                          ├── WS3 (Generation Tooling) ── WS4 (Spec Files) ──┬── WS5 (Docs UI)
WS2 (Missing Publishers) ┘                                                   ├── WS6 (CI)
                                                                              └── WS7 (Type Safety)
```

WS1 and WS2 can parallel — outbox migration is independent per service.
WS5, WS6, WS7 can parallel — all consume the specs but produce independent outputs.
WS7 depends on WS3-4 (needs AsyncAPI specs to generate from) and WS1-2 (services must
use outbox before migrating to generated publishers).

**Dependencies:**

- WS3-7 depend on WS1-2 for accurate event inventory and consistent outbox usage
- Requires Node.js for AsyncAPI CLI tooling (already available for buf)
- No external infrastructure changes (outbox table exists in services that already have the
  infrastructure; WS1 provisions it for services that currently lack it)

---

## 7. Success Criteria

### Foundational (WS1-2)

- [ ] All event-publishing services use the transactional outbox pattern (`OutboxPublisher`)
- [ ] Party and InternalBankAccount services publish domain events
- [ ] All topic names follow `<service>.<event>.<version>` convention
- [ ] Deprecated dual-published topics are removed
- [ ] Zero instances of `_ = publish()` (silent error discard)

### Specification (WS3-5)

- [ ] `make asyncapi` generates valid AsyncAPI 3.0 YAML for all 8 services
- [ ] Every Kafka topic has a corresponding channel definition in the AsyncAPI spec
- [ ] Payload schemas are derived from proto definitions (not manually duplicated)
- [ ] Developers can browse event documentation in a UI (locally served)

### Enforcement (WS6-7)

- [ ] CI validates AsyncAPI spec correctness on every PR
- [ ] CI detects breaking async contract changes via `asyncapi diff`
- [ ] Generated typed publishers exist for all 8 BIAN service domains
- [ ] All services use generated publishers (no manual `PublishConfig` construction)
- [ ] `protovalidate` is enforced before outbox write — invalid events are rejected
- [ ] ADR-0004 references AsyncAPI specs as the canonical event contract documentation

---

## 8. Related Documents

### Internal

- [ADR-0004: Event Schema Evolution](../adr/0004-event-schema-evolution.md) — topic naming,
  outbox pattern, idempotency, BIAN v14 AsyncAPI awareness amendment
- [ADR-0005: Adapter Pattern](../adr/0005-adapter-pattern-layer-translation.md) — layer
  translation between BIAN models and internal schemas
- [PRD-025: Real-Time Event Streaming](025-real-time-event-streaming.md) — WebSocket delivery
  of events to the operations console

### Standards and Specifications

- [AsyncAPI 3.0 Specification](https://www.asyncapi.com/docs/reference/specification/v3.0.0)
- [BIAN v14 AsyncAPI Specs](https://github.com/bian-official/public/tree/main/release14.0.0/semantic-apis/asyncapi)
- [protovalidate](https://github.com/bufbuild/protovalidate) — proto validation rules
  (already in use for gRPC, not yet enforced on event publishing)

### Tooling

- [asyncapi-codegen](https://github.com/lerenn/asyncapi-codegen) — Go code generator
  for AsyncAPI 3.0, candidate for typed publisher generation
- [AsyncAPI Diff](https://github.com/asyncapi/diff) — breaking change detection for
  event contracts
- [AsyncAPI CLI](https://www.asyncapi.com/tools/cli) — validation, generation, studio
- [Microcks](https://microcks.io/) — contract testing for async APIs
