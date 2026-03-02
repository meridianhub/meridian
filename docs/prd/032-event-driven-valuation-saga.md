---
name: prd-032-event-triggered-saga-execution
description: Add event: trigger type to manifests so sagas execute in response to domain events
triggers:
  - Adding event-driven saga triggers to manifests
  - Building event consumers that trigger saga execution
  - Extending the saga trigger type system beyond api/webhook/scheduled
  - Connecting position-keeping events to downstream workflows
  - Designing reactive workflows triggered by domain events
instructions: |
  Meridian's manifest currently supports three saga trigger types: api:, webhook:, scheduled:.
  This PRD introduces a fourth: event:<channel>, enabling sagas to execute in response to
  domain events. The event: trigger is industry-agnostic platform infrastructure — the saga
  script determines what happens, not the trigger type.

  Key design choices:
  - The event: trigger type is transport-agnostic (hexagonal architecture) — Kafka is one
    adapter, but the port accepts any proto message from any delivery mechanism
  - No new service required — the existing saga runtime (control-plane) already has the
    SagaTrigger port used by Stripe webhooks; event consumption is another input adapter
  - Account type policies can optionally reference a saga to execute when positions are
    captured — this is how tenants wire reactive workflows without custom consumers
  - Idempotency uses correlation_id from the source event to prevent duplicate processing
  - Event chain termination: downstream positions that lack a saga policy are dropped cheaply
---

# PRD-032: Event-Triggered Saga Execution

**Author:** Meridian Platform Team
**Status:** Not Started
**Date:** 2026-03-02

---

## 1. Problem Statement

Meridian's manifest supports three saga trigger types:

| Trigger | Format | Initiation |
|---------|--------|------------|
| `api:` | `api:/v1/path` | Client HTTP/gRPC request |
| `webhook:` | `webhook:name` | External provider callback |
| `scheduled:` | `scheduled:name` | Periodic cron/interval |

There is no way to declare a saga that fires in response to a **domain event**. This is the
fourth and arguably most important trigger type for an event-driven architecture.

Without it, any reactive workflow — position captured triggers valuation, payment received
triggers settlement, party onboarded triggers account provisioning — requires custom
consumer code outside the manifest. That breaks the core promise: tenants configure
workflows via Starlark sagas in manifests, not by writing Go services.

### 1.1 Relationship to PRD-030 (AsyncAPI Specification)

PRD-030 formalizes the structure of event contracts: channel naming, payload schemas
(derived from proto), and standard headers (`correlation_id`, `causation_id`, `tenant_id`).
The `event:` trigger is a direct consumer of those contracts.

| PRD-030 provides | PRD-032 consumes |
|------------------|------------------|
| AsyncAPI spec per service domain | Channel validation — manifest `event:` triggers reference real channels |
| Standard headers (`correlation_id`) | Idempotency key for saga deduplication |
| Standard headers (`tenant_id`) | Tenant scoping for multi-tenant event routing |
| Typed event publishers (outbox pattern) | Reliable at-least-once event delivery |
| Payload schemas (proto-derived) | Structured `ctx` passed to Starlark saga scripts |

PRD-030's outbox alignment work (WS1-2) is a prerequisite for PRD-032. Event-triggered
sagas depend on reliable event delivery — fire-and-forget writes risk losing the event
that should have triggered a saga.

### 1.2 Decorative Metadata

Account type definitions already carry fields that imply reactive behavior:

- `DefaultSagaPrefix` — saga naming prefix for operations on this account type
- `DefaultConversionMethodID` / `DefaultConversionMethodVersion` — default valuation method
- `ValuationMethods` — array of Starlark valuation method references

These fields are populated but not consumed by any runtime logic. Making them load-bearing
— driving automatic saga execution when positions are captured on accounts of that type —
is part of this PRD.

### 1.3 The Pattern is Industry-Agnostic

The `event:` trigger enables any reactive workflow a tenant defines in Starlark:

| Industry | Event | Saga | Effect |
|----------|-------|------|--------|
| Energy | kWh position captured | Cross-instrument valuation | GBP positions booked |
| Carbon | TONNE_CO2E position captured | Credit verification | Verified status updated |
| Compute | GPU_HOUR position captured | Usage billing | USD charge posted |
| Banking | Payment received | Settlement initiation | Clearing entries posted |

The platform provides the trigger infrastructure. The tenant provides the saga logic.

## 2. Design

### 2.1 New Trigger Type: `event:<channel>`

Add a fourth saga trigger prefix to the manifest schema:

```text
event:<channel-name>
```

Examples:

```text
event:position-keeping.transaction-captured.v1
event:payment-order.instruction-completed.v1
event:party.onboarding-completed.v1
```

The trigger binds a saga to an event channel (as defined by PRD-030's AsyncAPI specs).
When an event arrives on that channel, the saga runtime evaluates whether to execute the
saga (see 2.2 for filtering).

**Proto change** — update the `SagaDefinition.trigger` validation pattern:

```text
Current: ^(api:|webhook:|scheduled:).+$
New:     ^(api:|webhook:|scheduled:|event:).+$
```

### 2.2 Event Routing via Account Type Policies

Not every event on a channel should trigger a saga. The runtime filters using account type
metadata that already exists in reference-data:

```text
Event arrives
  → Extract account_id from event payload
  → Look up account → get account_type_code
  → Look up account type policies
  → Check: does this account type define a saga for this event?
    → No  → drop event (one lookup, no saga load)
    → Yes → execute the referenced saga
```

**The "no match" path must be cheap.** A saga that creates downstream positions will emit
further events. Those events hit account types with no saga policy and get dropped. This is
how event chains terminate — by absence of policy, not by special-case logic.

### 2.3 Manifest Declaration

A tenant manifest declares event-triggered sagas alongside the account types that activate
them. The platform wires the two together at runtime.

```json
{
  "accountTypes": [
    {
      "code": "METERED_USAGE",
      "name": "Metered Usage Account",
      "normalBalance": "NORMAL_BALANCE_DEBIT",
      "allowedInstruments": ["KWH"],
      "policies": {
        "validation": "amount > 0",
        "onPositionCaptured": {
          "saga": "usage-to-value",
          "conversionMethod": "retail_tariff_v1"
        }
      }
    },
    {
      "code": "BILLING",
      "name": "Billing Account",
      "normalBalance": "NORMAL_BALANCE_DEBIT",
      "allowedInstruments": ["GBP"],
      "policies": {
        "validation": "amount > 0"
      }
    }
  ],
  "sagas": [
    {
      "name": "usage-to-value",
      "trigger": "event:position-keeping.transaction-captured.v1",
      "script": "..."
    }
  ]
}
```

The connection is indirect:

- `event:` trigger tells the runtime *which channel to subscribe to*
- Account type `onPositionCaptured.saga` tells the runtime *which saga to invoke for
  positions on this account type*
- Multiple account types can reference different sagas for the same event channel
- Account types without `onPositionCaptured` are ignored (cheap drop)

### 2.4 Transport-Agnostic Architecture (Hexagonal)

Meridian uses hexagonal architecture. The `event:` trigger defines a **port** — the
transport that delivers events is an **adapter**. This follows the same pattern as the
utilization-metering-consumer, where domain logic receives a
`func(ctx, key, msg proto.Message) error` with no transport references in the signature.

```text
┌──────────────────────────────────────────────────────────┐
│                     SAGA RUNTIME                         │
│                                                          │
│  SagaTrigger port:                                       │
│    TriggerSaga(ctx, sagaName, inputData, idempotencyKey) │
│                                                          │
│  Already used by:                                        │
│    • api:    → gRPC/HTTP handler adapter                 │
│    • webhook: → Stripe PaymentEventConsumer adapter      │
│    • scheduled: → Platform scheduler adapter             │
│    • event:  → Event consumer adapter (this PRD)         │
│                                                          │
└──────────────────────────────────────────────────────────┘
                          ▲
                          │ SagaTrigger interface
                          │
         ┌────────────────┼────────────────┐
         │                │                │
   ┌─────┴──────┐  ┌─────┴──────┐  ┌─────┴──────┐
   │   Kafka     │  │   Outbox    │  │  In-Process │
   │   Adapter   │  │   Poller    │  │   (testing) │
   │             │  │   Adapter   │  │   Adapter   │
   └─────────────┘  └─────────────┘  └─────────────┘
```

The event consumer adapter:

1. Receives a proto message from *any* delivery mechanism
2. Extracts `account_id`, looks up account type policy
3. If policy matches, calls `SagaTrigger.TriggerSaga()` — the same port Stripe webhooks
   already use
4. Saga runtime handles execution, compensation, idempotency

**No new service is required.** The saga runtime in control-plane already has:

- Starlark execution environment
- Handler registry and typed service clients
- Compensation logic
- The `SagaTrigger` interface (already wired for Stripe webhooks)

Adding an event consumer adapter is incremental — same pattern as adding the Stripe
webhook adapter. The adapter subscribes to events and calls `TriggerSaga()`. The saga
runtime does the rest.

This also means the event consumer inherits the saga runtime's existing:

- Observability (structured logging, metrics)
- Multi-tenancy (tenant-scoped execution)
- Error handling (retry, compensation, dead-letter)

### 2.5 Idempotency

Event consumers reprocess events on restart/rebalance. The `correlation_id` standard
header (formalized by PRD-030) is the natural idempotency key:

1. Event consumer adapter delivers event to saga runtime via `TriggerSaga()`
2. Saga checks whether this `correlation_id` has already been processed
3. If yes, return early (no duplicate work)
4. If no, proceed with saga execution

This is saga-level idempotency, not consumer-level. The consumer is at-least-once; the
saga guarantees exactly-once semantics for the business operation. How the saga checks
for prior processing is up to the Starlark script — typically a query against
position-keeping or a dedicated idempotency store.

The `tenant_id` header (also from PRD-030) provides multi-tenant scoping — the consumer
routes events to the correct tenant's saga definitions and account type policies.

### 2.6 Event Chain Termination

Sagas that create downstream positions will emit further events. This creates a potential
loop:

```text
Position A → event → saga → Position B → event → ???
```

Termination is guaranteed by the account type policy lookup:

1. Consumer receives event for Position B
2. Looks up Position B's account type
3. Account type has no `onPositionCaptured` policy
4. Consumer drops the event

This is a single lookup with no saga loading. The "no policy" path handles the majority of
events in any deployment.

### 2.7 Eventual Consistency

There is a window (seconds) between the source event and the saga's side effects
materializing. This is inherent to event-driven architectures. Tenants design their
settlement periods and reconciliation cycles around this — the platform does not promise
synchronous execution for event-triggered sagas.

## 3. What Already Exists

### 3.1 Position-Keeping Event Emission

- Outbox pattern: position log INSERT + event_outbox INSERT in same DB transaction
- Background worker polls outbox, publishes to message broker
- Channel: `position-keeping.transaction-captured.v1`
- Payload: log_id, account_id, transaction_id, amount, direction, source, description,
  reference, correlation_id, timestamp, version

### 3.2 Saga Runtime and SagaTrigger Port

- Starlark saga orchestration fully implemented (PRD-006, 24/24 tasks)
- Typed service clients auto-generated from handler schemas
- Handlers: `position_keeping.initiate_log`, `position_keeping.update_log`, etc.
- Automatic compensation (if step N fails, steps N-1..1 roll back)
- `SagaTrigger` interface already used by Stripe webhook adapter in control-plane

### 3.3 Valuation Engine

- `shared/pkg/valuation/` and `shared/pkg/valuationfeature/`
- Starlark-sandboxed, <5ms target, 5s timeout, 64MB memory limit
- In-memory cache with 5-minute TTL
- Available as a service module in Starlark sagas (`valuation_engine.compute(...)`)

### 3.4 Account Type Metadata

- `DefaultSagaPrefix`, `DefaultConversionMethodID`, `ValuationMethods` fields exist
- Currently populated but not consumed by any runtime logic

### 3.5 Existing Event Consumer Patterns

Two patterns already exist in the codebase:

**Hexagonal consumer (utilization-metering-consumer):**
- Domain ports: `PositionKeepingClient`, `UtilizationPublisher` (interfaces)
- Transport adapter: `AuditConsumer` wraps `MessageHandler` — domain never sees Kafka
- Fan-out: primary output (gRPC to PK) + optional secondary (MDS publisher)
- Handler signature: `func(ctx context.Context, key []byte, msg proto.Message) error`

**Direct saga trigger (Stripe webhook consumer):**
- `PaymentEventConsumer` receives HTTP webhook, calls `SagaTrigger.TriggerSaga()`
- No message broker involved — event arrives via HTTP, saga executes immediately
- Same `SagaTrigger` port the `event:` trigger would use

## 4. Scope

### 4.1 In Scope

- `event:` trigger type in manifest schema (proto + validator + planner + applier)
- Event consumer adapter wired to the existing `SagaTrigger` port in the saga runtime
- Account type policy evaluation for saga routing (`onPositionCaptured`)
- Make existing account type metadata fields (`DefaultSagaPrefix`, etc.) load-bearing
- Idempotency contract (correlation_id as natural key)
- Event chain termination via absence of policy
- Account type caching in consumer (with TTL)
- Manifest validation: event-triggered sagas reference channels defined in AsyncAPI specs
  (PRD-030)

### 4.2 Prerequisites

- **PRD-030 WS1-2 (Outbox Alignment)**: event-triggered sagas depend on reliable
  at-least-once delivery. Services using fire-and-forget writes risk losing the event
  that should have triggered a saga.

### 4.3 Out of Scope

- Tenant-specific saga scripts (tenants write these in their manifests)
- Industry-specific data models (MPAN mappings, tariff structures, etc.)
- Batch-aware optimizations (individual event processing is sufficient at pilot scale)
- Custom consumer code for tenants (the whole point is to avoid this)
- Event contract formalization (covered by PRD-030)
- New service deployment — the event consumer adapter runs inside the existing saga runtime

## 5. Reference: Tenant Manifest Examples

These examples illustrate how tenants in different industries would use the `event:`
trigger. They are **tenant configuration, not platform code** — included here to
validate that the platform infrastructure is sufficiently general.

### 5.1 Energy: Position Valuation

An energy retailer values kWh meter reads at retail and wholesale rates:

```python
def execute(ctx):
    # Idempotency: skip if already valued
    existing = position_keeping.query_logs(
        correlation_id=ctx.correlation_id,
        instrument_code="GBP",
    )
    if existing.count > 0:
        return {"status": "ALREADY_PROCESSED"}

    # Value at retail rate
    step()
    retail = valuation_engine.compute(
        method_id=ctx.policy.conversion_method,
        amount=ctx.amount,
        from_instrument=ctx.instrument_code,
        to_instrument="GBP",
    )

    # Book customer billing position
    step()
    position_keeping.initiate_log(
        account_id=ctx.metadata.billing_account_id,
        instrument_code="GBP",
        direction="DEBIT",
        amount=retail.amount,
        correlation_id=ctx.correlation_id,
    )

    # Value at wholesale rate and book counterparty
    step()
    wholesale = valuation_engine.compute(
        method_id=ctx.policy.wholesale_method,
        amount=ctx.amount,
        from_instrument=ctx.instrument_code,
        to_instrument="GBP",
    )

    step()
    position_keeping.initiate_log(
        account_id=ctx.metadata.counterparty_account_id,
        instrument_code="GBP",
        direction="CREDIT",
        amount=wholesale.amount,
        correlation_id=ctx.correlation_id,
    )

    return {"status": "VALUED", "retail": str(retail.amount), "wholesale": str(wholesale.amount)}
```

### 5.2 Compute: Usage Billing

A cloud provider converts GPU-hours to USD charges:

```python
def execute(ctx):
    existing = position_keeping.query_logs(
        correlation_id=ctx.correlation_id,
        instrument_code="USD",
    )
    if existing.count > 0:
        return {"status": "ALREADY_BILLED"}

    step()
    charge = valuation_engine.compute(
        method_id=ctx.policy.conversion_method,
        amount=ctx.amount,
        from_instrument="GPU_HOUR",
        to_instrument="USD",
    )

    step()
    position_keeping.initiate_log(
        account_id=ctx.metadata.billing_account_id,
        instrument_code="USD",
        direction="DEBIT",
        amount=charge.amount,
        correlation_id=ctx.correlation_id,
    )

    return {"status": "BILLED", "amount": str(charge.amount)}
```

### 5.3 Pilot Context: Outfox Energy

The first deployment of event-triggered sagas is the Outfox reconciliation pilot:

- CSV-imported HH reads (48 settlement periods/day per MPAN)
- Automatic valuation at customer retail tariff
- Automatic GSP wholesale-side booking
- Reconcilable output for dual-publish comparison

This validates the platform infrastructure with real data. The saga script and manifest
configuration are tenant-owned — Meridian provides the `event:` trigger, the account type
routing, and the saga runtime.

## 6. Open Questions

1. **Account type policy schema**: The `onPositionCaptured` policy shape proposed in 2.3
   is new. Should it reuse the existing `DefaultSagaPrefix` / `ValuationMethods` fields,
   or is a cleaner schema worth the migration?

2. **Account type caching**: Should the consumer cache account type lookups with TTL, or
   fetch per event via gRPC? Per-event is safer but adds latency. A 60s TTL cache is
   likely sufficient since account type definitions change infrequently.

3. **Multiple sagas per channel**: If two saga definitions declare `event:` triggers on
   the same channel, should both execute? Or should the account type policy disambiguate
   (only one saga per account type per event)?

4. **Dead letter handling**: When a saga fails after retries, should the event go to a
   dead-letter queue for manual review, or should the consumer retry indefinitely with
   backoff?

## 7. Success Criteria

### Platform

- Event-triggered sagas are declarable in tenant manifests using the `event:` trigger
- Account types without saga policies drop events in <5ms (one lookup, no saga load)
- Idempotent: reprocessing the same event produces no duplicate side effects
- Event chain terminates: downstream positions without policies do not trigger further sagas
- Saga compensation works: if a step fails, prior steps roll back
- No new service required — event consumer adapter runs inside existing saga runtime

### First Deployment

- Tenant-configured saga successfully values positions end-to-end
- Consumer handles at-least-once delivery without duplicate bookings
- Event chain terminates correctly (output positions are not re-processed)

## 8. Related Documents

- [PRD-030: AsyncAPI Specification](030-asyncapi-specification.md) — **prerequisite**:
  formalizes event contracts, outbox alignment, standard headers that this PRD consumes
- [PRD-006: Starlark Saga Orchestration (Core)](006-starlark-saga-orchestration-core.md) —
  saga runtime this PRD extends with the `event:` trigger
- [PRD-011: Valuation Service](011-valuation-service.md) — valuation engine available as
  a service module in event-triggered sagas
- [ADR-0004: Event Schema Evolution](../adr/0004-event-schema-evolution.md) — channel
  naming convention and outbox pattern
- [ADR-0026: Canonical Ingestion Contract](../adr/0026-canonical-ingestion-contract.md) —
  boundary pattern for external data
