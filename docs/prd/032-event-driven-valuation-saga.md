---
name: prd-032-event-driven-valuation-saga
description: Event-triggered saga execution for automatic cross-instrument valuation when positions are captured
triggers:
  - Adding event-driven saga triggers to manifests
  - Working on valuation pipelines or kWh-to-GBP conversion
  - Building Kafka consumers that trigger saga execution
  - Extending the saga trigger type system beyond api/webhook/scheduled
  - Working on the Outfox energy pilot or HH settlement data
  - Connecting position-keeping events to downstream workflows
instructions: |
  Meridian's manifest currently supports three saga trigger types: api:, webhook:, scheduled:.
  This PRD introduces a fourth: event:<topic>, enabling sagas to execute in response to Kafka
  domain events. The first use case is automatic valuation: when a kWh position is logged,
  the system looks up the account type's valuation configuration and executes a saga that
  converts the position to GBP using the tenant's Starlark valuation methods.

  Key design choices:
  - The event: trigger type is generic infrastructure, not valuation-specific
  - Account type metadata (DefaultSagaPrefix, ValuationMethods) determines whether a
    captured position triggers valuation — accounts without valuation hooks are dropped cheaply
  - The valuation saga is a standard Starlark saga declared in the manifest
  - Idempotency uses correlation_id from the source event to prevent double-booking
  - Event chain termination: GBP positions emit their own events but the GBP account type
    has no valuation hook, so the consumer drops them (one lookup, no saga load)
---

# PRD-032: Event-Driven Valuation Saga

**Author:** Meridian Platform Team
**Status:** Not Started
**Date:** 2026-03-02

---

## 1. Problem Statement

When a kWh position is logged against a customer account in position-keeping, nothing
currently triggers valuation to convert it to a GBP position. The demo works around this by
manually booking both sides (two separate REST deposits). For the pilot, this must be
automatic: HH meter read arrives as kWh, the system values it via the tenant's Starlark
tariff script, and GBP positions appear on both the customer and GSP accounts.

### 1.1 Missing Trigger Type

Meridian's manifest supports three saga trigger types:

| Trigger | Format | Initiation |
|---------|--------|------------|
| `api:` | `api:/v1/path` | Client HTTP/gRPC request |
| `webhook:` | `webhook:name` | External provider callback |
| `scheduled:` | `scheduled:name` | Periodic cron/interval |

There is no way to declare a saga that fires in response to a **domain event**. This is the
fourth and arguably most important trigger type for an event-driven architecture.

### 1.2 Decorative Metadata

Account type definitions already carry valuation-related fields:

- `DefaultSagaPrefix` — saga naming prefix for operations on this account type
- `DefaultConversionMethodID` / `DefaultConversionMethodVersion` — default valuation method
- `ValuationMethods` — array of Starlark valuation method references

These fields are currently decorative. Making them load-bearing — driving automatic saga
execution — is the core of this PRD.

### 1.3 Pilot Requirement

The Outfox reconciliation pilot requires:

- CSV-imported HH reads (48 settlement periods/day, already demonstrated in demo)
- Automatic valuation at the customer's retail tariff (account-type-specific Starlark)
- Automatic GSP wholesale-side booking
- Reconcilable output: positions queryable by MPAN reference, grouped by settlement period

## 2. Design

### 2.1 New Trigger Type: `event:<topic>`

Add a fourth saga trigger prefix to the manifest schema:

```text
event:<topic-pattern>
```

Examples:

```text
event:position-keeping.transaction-captured.v1
event:payment-order.instruction-completed.v1
```

The trigger binds a saga to a Kafka topic. When an event arrives on that topic, the saga
runtime evaluates whether to execute the saga (see 2.2 for filtering).

**Proto change** — update the `SagaDefinition.trigger` validation pattern:

```text
Current: ^(api:|webhook:|scheduled:).+$
New:     ^(api:|webhook:|scheduled:|event:).+$
```

### 2.2 Event Filtering via Account Type Metadata

Not every `transaction-captured` event should trigger a saga. The consumer must filter
cheaply:

```text
Event arrives
  → Extract account_id
  → Look up account → get account_type_code
  → Look up account type definition
  → Check: does this account type define a valuation hook?
    → No hook  → drop event (one gRPC call, no saga load)
    → Has hook → execute saga referenced by DefaultSagaPrefix
```

**The "no hook" path must be cheap.** GBP positions created by the valuation saga will
themselves emit `transaction-captured` events. The consumer picks them up, finds no
valuation hook on the GBP account type, and drops them. This is how the event chain
terminates.

### 2.3 Manifest Declaration

A tenant manifest declares the event-triggered valuation saga alongside the account types
that activate it:

```json
{
  "instruments": [
    { "code": "KWH", "name": "Kilowatt Hour", "type": "INSTRUMENT_TYPE_COMMODITY",
      "dimensions": { "unit": "KWH", "precision": 3 } },
    { "code": "GBP", "name": "British Pound Sterling", "type": "INSTRUMENT_TYPE_FIAT",
      "dimensions": { "unit": "GBP", "precision": 2 } }
  ],
  "accountTypes": [
    {
      "code": "CUSTOMER_ENERGY",
      "name": "Customer Energy Account",
      "normalBalance": "NORMAL_BALANCE_DEBIT",
      "allowedInstruments": ["KWH"],
      "policies": {
        "validation": "amount > 0",
        "valuationSagaPrefix": "meter-read-to-cash",
        "defaultConversionMethod": "retail_tariff_v1"
      }
    },
    {
      "code": "CUSTOMER_BILLING",
      "name": "Customer Billing Account",
      "normalBalance": "NORMAL_BALANCE_DEBIT",
      "allowedInstruments": ["GBP"],
      "policies": {
        "validation": "amount > 0"
      }
    }
  ],
  "sagas": [
    {
      "name": "meter-read-to-cash",
      "trigger": "event:position-keeping.transaction-captured.v1",
      "script": "..."
    }
  ]
}
```

The connection between event and saga is indirect: the `event:` trigger tells the runtime
*which topic to consume*; the account type's `valuationSagaPrefix` tells the consumer
*which saga to invoke for this account*. This allows multiple account types to use different
sagas for the same event topic.

### 2.4 Valuation Saga: meter-read-to-cash

The Starlark saga executed when a kWh position triggers valuation:

```python
def execute(ctx):
    # 1. Idempotency check — has this correlation_id already produced a GBP position?
    existing = position_keeping.query_logs(
        correlation_id=ctx.correlation_id,
        instrument_code="GBP",
    )
    if existing.count > 0:
        return {"status": "ALREADY_VALUED", "correlation_id": ctx.correlation_id}

    # 2. Look up valuation method for this account type
    method = valuation_engine.get_method(
        account_type_code=ctx.account_type_code,
        from_instrument="KWH",
        to_instrument="GBP",
    )

    # 3. Value at customer retail rate
    customer_valuation = valuation_engine.compute(
        method_id=method.id,
        amount=ctx.amount,
        from_instrument="KWH",
        to_instrument="GBP",
        context={"mpan": ctx.reference, "settlement_period": ctx.metadata.settlement_period},
    )

    # 4. Book GBP on customer billing account
    step()
    position_keeping.initiate_log(
        account_id=ctx.customer_billing_account_id,
        instrument_code="GBP",
        direction="DEBIT",
        amount=customer_valuation.amount,
        reference=ctx.reference,
        correlation_id=ctx.correlation_id,
        description="Valued kWH: " + str(ctx.amount) + " KWH @ " + str(customer_valuation.rate),
    )

    # 5. Determine GSP counterparty
    gsp_account = reference_data.resolve_counterparty(
        account_id=ctx.account_id,
        relationship_type="GSP_SUPPLIER",
    )

    # 6. Value at wholesale rate
    wholesale_valuation = valuation_engine.compute(
        method_id=method.wholesale_method_id,
        amount=ctx.amount,
        from_instrument="KWH",
        to_instrument="GBP",
        context={"gsp_group": gsp_account.metadata.gsp_group},
    )

    # 7. Book GBP on GSP account
    step()
    position_keeping.initiate_log(
        account_id=gsp_account.account_id,
        instrument_code="GBP",
        direction="CREDIT",
        amount=wholesale_valuation.amount,
        reference=ctx.reference,
        correlation_id=ctx.correlation_id,
        description="GSP wholesale: " + str(ctx.amount) + " KWH @ " + str(wholesale_valuation.rate),
    )

    return {
        "status": "VALUED",
        "customer_gbp": str(customer_valuation.amount),
        "gsp_gbp": str(wholesale_valuation.amount),
        "margin": str(customer_valuation.amount - wholesale_valuation.amount),
        "correlation_id": ctx.correlation_id,
    }
```

### 2.5 Idempotency

Kafka consumers reprocess events on rebalance/restart. The correlation_id in the source
event is the natural idempotency key:

1. Consumer delivers event to saga runtime
2. Saga queries position-keeping: "has this correlation_id already produced a GBP position?"
3. If yes, return early (no double-booking)
4. If no, proceed with valuation and booking

This is saga-level idempotency, not consumer-level. The consumer itself is at-least-once;
the saga guarantees exactly-once semantics for the business operation.

### 2.6 Event Chain Termination

The GBP positions created by the valuation saga emit their own `transaction-captured`
events. This creates a potential infinite loop:

```text
kWH position → event → valuation saga → GBP position → event → ???
```

Termination is guaranteed by the account type lookup:

1. Consumer receives GBP `transaction-captured` event
2. Looks up the GBP account type (e.g., `CUSTOMER_BILLING`)
3. `CUSTOMER_BILLING` has no `valuationSagaPrefix` defined
4. Consumer drops the event

This is a single gRPC call with no saga loading. The "no hook" path must remain cheap
because it handles the majority of events (every GBP booking, every non-energy account).

### 2.7 Consumer Architecture

**Option A: Dedicated service** — a new `valuation-trigger` service with its own deployment.

**Option B: Saga runtime extension** — add event-trigger handling to the existing saga
runtime (control-plane or a shared saga executor).

**Recommendation: Option B.** The saga runtime already has the Starlark execution
environment, handler registry, and compensation logic. Adding a Kafka consumer group is
incremental. A dedicated service would duplicate the saga execution stack.

The consumer follows the same patterns as existing consumers (audit-worker,
utilization-metering-consumer):

- Consumer group: `valuation-trigger`
- Partition assignment: cooperative-sticky
- Offset commit: after successful saga execution (or idempotent skip)
- Error handling: dead-letter topic after N retries (configurable)

### 2.8 Eventual Consistency

There is a window (seconds) between kWh position appearing and GBP position materializing.
This is acceptable for energy settlement where settlement periods are 30 minutes.
The reconciliation service handles D+1/D+5 corrections downstream.

## 3. What Already Exists

### 3.1 Position-Keeping Event Emission

- Outbox pattern: position log INSERT + event_outbox INSERT in same DB transaction
- Background worker polls outbox, publishes to Kafka
- Topic: `position-keeping.transaction-captured.v1`
- Payload: log_id, account_id, transaction_id, amount, direction, source, description,
  reference, correlation_id, timestamp, version

### 3.2 Valuation Engine

- `shared/pkg/valuation/` and `shared/pkg/valuationfeature/`
- Starlark-sandboxed, <5ms target, 5s timeout, 64MB memory limit
- In-memory cache with 5-minute TTL
- Already used by reconciliation service for variance valuation

### 3.3 Saga Runtime

- Starlark saga orchestration fully implemented (PRD-006, 24/24 tasks)
- Typed service clients auto-generated from handler schemas
- Handlers: `position_keeping.initiate_log`, `position_keeping.update_log`, etc.
- Automatic compensation (if step N fails, steps N-1..1 roll back)

### 3.4 Account Type Metadata

- `DefaultSagaPrefix`, `DefaultConversionMethodID`, `ValuationMethods` fields exist
- Currently populated but not consumed by any runtime logic

### 3.5 Existing Kafka Consumers

- audit-worker: consumes `audit.events.*.v1`, writes to audit_log
- utilization-metering-consumer: consumes `audit.events.*.v1`, transforms to measurements
- Both provide patterns for consumer group management, offset handling, error recovery

## 4. Scope

### 4.1 In Scope

- `event:` trigger type in manifest schema (proto + validator + planner + applier)
- Consumer infrastructure for event-triggered saga execution
- Account type valuation hook evaluation (make decorative fields load-bearing)
- `meter-read-to-cash` reference saga in Starlark
- Idempotency via correlation_id
- Event chain termination via account type lookup
- Account type caching in consumer (with TTL, refreshed per-event is too expensive)
- Manifest validation: ensure event-triggered sagas reference valid topics

### 4.2 Out of Scope

- DCC adapter for live meter data ingestion (pilot uses CSV import)
- Customer acquisition / CSS adapter / DTN flows
- Demand forecasting
- Dynamic per-customer tariffs (second-order feature)
- Workflow management / exception handling UI
- MPAN-to-GSP mapping data model (open question — needs separate design)

## 5. Open Questions

1. **MPAN-to-GSP mapping**: Where does this live? Options: (a) party relationship in
   reference-data, (b) account metadata field, (c) dedicated mapping table. This affects
   step 5 of the saga.

2. **Account type caching**: Should the consumer cache account type lookups with TTL, or
   fetch per event via gRPC? Per-event is safer but adds latency to every event. A 60s TTL
   cache is likely sufficient since account type definitions change infrequently.

3. **Batch import path**: CSV batch import already goes through position-keeping (which
   emits events). Confirm this is the intended flow — each row produces a
   `transaction-captured` event, which triggers individual saga executions. For 48 HH
   periods, that is 48 saga executions per MPAN per day. At pilot scale (small set of
   MPANs) this is fine; at production scale, batch-aware optimizations may be needed.

4. **Dead letter handling**: When a valuation saga fails (method not found, valuation
   engine error, position-keeping unavailable), should the event go to a dead-letter topic
   for manual review, or should the consumer retry with backoff?

## 6. Success Criteria

### Business

- HH meter reads imported via CSV are automatically valued and booked to GBP accounts
  within 10 seconds of position capture
- Both customer retail and GSP wholesale positions appear without manual intervention
- Reconciliation service can compare Meridian GBP positions against Outfox's existing
  system for the pilot MPAN set

### Technical

- Event-triggered sagas are declarable in tenant manifests using the `event:` trigger prefix
- Account types without valuation hooks drop events in <5ms (one lookup, no saga load)
- Idempotent: reprocessing the same event produces no duplicate positions
- Event chain terminates: GBP bookings do not trigger further valuation
- Saga compensation works: if wholesale booking fails, customer booking is reversed

## 7. Related Documents

- [PRD-006: Starlark Saga Orchestration (Core)](006-starlark-saga-orchestration-core.md)
- [PRD-011: Valuation Service](011-valuation-service.md)
- [PRD-030: AsyncAPI Specification](030-asyncapi-specification.md) — event topic documentation
- [ADR-0004: Event Schema Evolution](../adr/0004-event-schema-evolution.md) — topic naming
- [ADR-0026: Canonical Ingestion Contract](../adr/0026-canonical-ingestion-contract.md) — boundary pattern
