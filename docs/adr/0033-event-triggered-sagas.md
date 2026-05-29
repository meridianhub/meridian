---
name: adr-0033-event-triggered-sagas
description: Reactive saga execution via Kafka event channels with CEL-filtered dispatch and chain-depth termination
triggers:
  - Adding event-triggered saga workflows to a tenant manifest
  - Understanding the event:channel trigger syntax in saga definitions
  - Debugging event-driven saga dispatch or CEL filter evaluation
  - Configuring the event-router service for new Kafka channels
  - Implementing chain termination in multi-step saga pipelines
instructions: |
  Use the event: trigger prefix in saga definitions to execute sagas reactively when Kafka events arrive.
  CEL filters on the optional filter field scope which events fire the saga; omit the filter to match all events on the channel.
  Prevent infinite loops by writing filters that exclude the saga's own output events (chain termination pattern).
  The event-router service (formerly utilization-metering-consumer) consumes Kafka topics, evaluates filters, and calls control-plane ExecuteSaga.
  Maximum chain depth defaults to 10; access current depth in CEL via chain_depth.
---

# 33. Event-Triggered Sagas

Date: 2026-03-04

## Status

Accepted

## Context

[ADR-0028](0028-starlark-saga-cel-valuation.md) established Starlark sagas as the orchestration layer and
identified three trigger types: API calls, webhooks, and scheduled jobs. This covered reactive triggers
from external clients and time-based triggers, but left a gap for **platform-internal reactivity**: sagas
that must fire when another service emits a Kafka event.

### The Gap: No Reactive Pipeline Trigger

Without a reactive trigger, tenant configurations must choose between:

1. **Pull-based polling**: A scheduled saga queries for new events. Adds latency, wastes compute during
   quiet periods, and strains upstream services during catch-up.

2. **Hardcoded consumers**: Platform engineers write Go consumers per use case. Eliminates tenant
   self-service and creates an N×M coupling problem (N services × M tenant use cases).

3. **Webhook fan-out**: External systems push events back into the platform. Doubles network traffic and
   requires tenants to operate infrastructure outside Meridian.

### The Business Need

The convergence of finance, energy, and real-world assets requires closed-loop workflows where a position
capture automatically triggers valuation, where a party registration triggers compliance checks, and where
a market data observation triggers a payout distribution. These workflows are tenant-specific business
logic that cannot be hardcoded into platform services.

| Use Case | Triggering Event | Downstream Action |
|----------|-----------------|-------------------|
| Energy billing | kWh position captured | Compute GBP settlement value |
| Cloud billing | GPU_HOUR position captured | Calculate USD charge |
| KYC initiation | Individual party created | Book compliance reserve |
| Payout distribution | Race result becomes official | Distribute winnings to syndicate |
| Cost basis adjustment | Corporate action recorded | Adjust cost basis accounts |

### Why CEL for Filters

Event channels carry heterogeneous events. A single position-keeping channel publishes transactions for
all instruments (GBP, kWh, GPU_HOUR, GOLD, SILVER, PLATINUM, etc.). A saga that only handles kWh
transactions must not fire on GBP transactions.

CEL was already in use for validation expressions ([ADR-0014](0014-financial-instrument-reference-data.md))
and saga preconditions ([ADR-0028](0028-starlark-saga-cel-valuation.md)). It provides:

- Sub-millisecond evaluation (guaranteed bounded execution)
- Compile-time validation at manifest apply (errors caught before production)
- Access to event payload fields, metadata headers, and chain depth

### The Chain Depth Problem

Event-triggered sagas create new positions. New positions emit events. Those events trigger sagas. Without
a termination mechanism, this loop runs indefinitely.

Three approaches were evaluated:

| Approach | Mechanism | Risk |
|----------|-----------|------|
| Filter-only | CEL filter excludes output events | Correct but easily misconfigured |
| Time-to-live | Discard events older than N seconds | Breaks slow-path sagas legitimately |
| Depth counter | Increment header on each hop; drop beyond limit | Bounded by construction; safe backstop |

**Decision**: Use both CEL filters (the correct solution) and a depth counter (the safe backstop).
Filters terminate loops by design; the counter terminates loops when filters are misconfigured.

## Decision Drivers

* **Tenant self-service**: Reactive workflows must be configurable from manifests without platform
  engineering involvement.
* **Zero latency**: React to events as they arrive; polling introduces unnecessary lag for financial data.
* **Safety**: No infinite loops, no unbounded resource consumption, no cross-tenant data leakage.
* **Idempotency**: At-least-once Kafka delivery requires exactly-once saga execution.
* **Consistency**: Same Starlark + CEL toolchain already used for other trigger types.

## Considered Options

1. **Polling schedulers per tenant** — Periodic sagas query for unprocessed events
2. **Per-use-case Go consumers** — Platform engineers write consumers for each reactive pattern
3. **Webhook fan-out via external system** — Events routed through an external broker back to webhooks
4. **Event-triggered sagas with CEL filters** (chosen) — `event:` trigger prefix with optional filter field

## Decision Outcome

Chosen option: **event-triggered sagas with CEL filters**, because it extends the existing Starlark
manifest model to reactive pipelines without introducing new primitives or requiring platform code changes
for each new tenant use case.

### Trigger Format

The `trigger` field in a saga definition accepts the `event:` prefix followed by the Kafka channel name:

```yaml
saga_definitions:
  - name: kwh_valuation
    trigger: "event:position-keeping.transaction-captured.v1"
    filter: "event.instrument_code == 'KWH' && event.direction == 'DEBIT'"
    script: |
      # Starlark saga body...
```

The `filter` field is optional. When omitted, the saga fires on every event on the channel.

### CEL Filter Environment

The `event_filter` CEL environment exposes three variables:

| Variable | Type | Description |
|----------|------|-------------|
| `event` | `dyn` | Event payload fields (deserialized from proto via AsyncAPI mapping) |
| `metadata` | `map(string, string)` | Kafka message headers (`x-tenant-id`, `x-meridian-chain-depth`, etc.) |
| `chain_depth` | `int` | Current saga chain depth (number of event-triggered hops in this chain) |

### Chain Depth Enforcement

Each saga execution adds the `x-meridian-chain-depth` header to outbound Kafka messages. The event-router
reads this header and increments it on each hop. Events arriving with `chain_depth >= max_chain_depth`
(default: 10) are dropped with a warning log.

```
Event arrives (depth=0)
    → Filter matches → Saga executes → New position created
    → New event published (depth=1)
    → Filter matches → Saga executes → ...
    → Event arrives (depth=9)
    → Filter excludes output instrument (chain terminated by design)
    → OR depth >= 10 (chain terminated by safety limit)
```

### Event-Router Service

The event-router service handles the Kafka → CEL → gRPC dispatch pipeline:

1. **Kafka consumer**: Multi-channel consumer subscribing to all registered event channels
2. **Saga registry**: Thread-safe in-memory index mapping channel names to compiled sagas with
   precompiled CEL filter programs. Reloaded atomically when the manifest changes.
3. **CEL evaluation**: For each incoming event, evaluates each registered filter. Non-matching sagas
   are skipped; evaluation errors skip the saga with a warning log.
4. **Idempotency store**: CockroachDB-backed store deduplicates saga dispatches by `(sagaName, correlationID)`.
   At-least-once Kafka delivery cannot cause duplicate saga executions.
5. **gRPC trigger**: Calls control-plane `ExecuteSaga` RPC with the event payload as `input_data`.

### Input Data Structure

The `input_data` dictionary available to the Starlark saga script contains the event proto fields
mapped to Go types, plus standard headers:

```python
input_data = {
    # Standard headers (always present)
    "correlation_id": "uuid-from-message-headers",

    # Event payload fields (from proto via AsyncAPI deserialization)
    "event": {
        "instrument_code": "KWH",
        "direction": "DEBIT",
        "amount_cents": 150000,
        "account_id": "acc-uuid",
        # ... other proto fields
    },

    # Raw metadata headers
    "metadata": {
        "x-meridian-chain-depth": "1",
        "x-tenant-id": "tenant-uuid",
        # ...
    },
}
```

The thin event pattern applies: the saga receives only the fields published by the event producer.
Business data (account type, valuation method, billing account) is resolved by the saga via service
module calls against the entity graph.

### Architecture Overview

```
┌──────────────────────────────────────────────────────────────────────┐
│                          Tenant Manifest                              │
│  saga_definitions:                                                   │
│    - name: kwh_valuation                                             │
│      trigger: "event:position-keeping.transaction-captured.v1"       │
│      filter: "event.instrument_code == 'KWH'"                        │
│      script: <starlark>                                              │
└─────────────────────────────┬────────────────────────────────────────┘
                              │ applied via control-plane
                              ▼
┌──────────────────────────────────────────────────────────────────────┐
│                          Event Router                                 │
│                                                                      │
│  Kafka Consumer                                                      │
│    ↓                                                                 │
│  Saga Registry (channel → [CompiledSaga])                            │
│    ↓                                                                 │
│  CEL Filter Evaluation (< 1ms per saga)                              │
│    ↓                                                                 │
│  Idempotency Store (CockroachDB-backed dedup)                        │
│    ↓                                                                 │
│  gRPC → control-plane.ExecuteSaga                                    │
└─────────────────────────────┬────────────────────────────────────────┘
                              │
                              ▼
┌──────────────────────────────────────────────────────────────────────┐
│                         Control Plane                                 │
│  Executes Starlark saga with input_data from event                   │
│  New positions emitted → new Kafka events → loop or terminate        │
└──────────────────────────────────────────────────────────────────────┘
```

### Positive Consequences

* **Tenant self-service**: New reactive workflows defined entirely in manifests; no platform code changes.
* **Real-time**: Sub-second latency from event arrival to saga execution.
* **Type-safe filters**: CEL filters validated at manifest apply; invalid expressions rejected before
  they can silently fail in production.
* **Bounded execution**: Chain depth counter provides a hard safety limit against runaway loops.
* **Idempotency**: CockroachDB-backed deduplication protects against Kafka at-least-once redelivery.
* **Observability**: Prometheus metrics for events received, sagas triggered, filter latency, chain depth
  exceeded, and trigger failures.
* **Consistent toolchain**: Same Starlark + CEL model as API and scheduled triggers; no new languages.

### Negative Consequences

* **Filter correctness is tenant responsibility**: Misconfigured filters that do not terminate loops rely
  on the depth counter backstop, which drops events rather than surfacing a configuration error.
* **Kafka ordering not guaranteed**: Multiple event-router replicas may process partitions independently;
  sagas must not assume ordering of events within a channel.
* **Registry staleness**: The saga registry is loaded on manifest apply. Sagas added between registry
  reloads will not fire on events arriving during that window.
* **Channel proliferation**: Each new reactive use case requires a Kafka channel; the event-router
  subscribes to all registered channels, increasing broker connections.

## Implementation Details

### Manifest Validation

The manifest applier validates event-triggered sagas at apply time:

1. `trigger` channel must be registered in the AsyncAPI channel registry
2. `filter` CEL expression must compile against the `event_filter` environment
3. `filter` must return a boolean value (validated by type-checking the AST)
4. Chain termination is not automatically verified — operators must ensure filters exclude output events

### Idempotency Key Derivation

The correlation ID for idempotency is extracted in priority order:

1. Event proto `CorrelationID` field (populated during AsyncAPI deserialization)
2. Kafka header `correlation_id`
3. Kafka header `x-correlation-id`
4. Kafka header `X-Correlation-ID`
5. Kafka header `correlationId`
6. Generated UUID (with a warning log — Kafka redelivery may cause duplicate executions)

### Prometheus Metrics

| Metric | Type | Labels | Description |
|--------|------|--------|-------------|
| `meridian_event_router_events_received_total` | Counter | `channel` | Events entering the dispatch handler |
| `meridian_event_router_sagas_triggered_total` | Counter | `saga_name`, `channel` | Successful saga triggers |
| `meridian_event_router_filter_evaluation_duration_seconds` | Histogram | `saga_name` | CEL evaluation latency |
| `meridian_event_router_filter_evaluation_errors_total` | Counter | `saga_name` | CEL evaluation failures |
| `meridian_event_router_chain_depth_exceeded_total` | Counter | — | Events dropped at chain depth limit |
| `meridian_event_router_saga_trigger_failures_total` | Counter | `saga_name`, `channel` | Trigger infrastructure failures |
| `meridian_event_router_duplicate_events_total` | Counter | `saga_name` | Idempotency key collisions |

## Chain Termination Patterns

### Filter-Based Termination (Recommended)

Exclude the saga's own output from re-triggering:

```cel
# Energy valuation: only fire on non-GBP debits (GBP output excluded by filter)
event.instrument_code != 'GBP' && event.direction == 'DEBIT'

# Precious metals: only fire on listed metals (GBP settlement output excluded)
event.instrument_code in ['GOLD', 'SILVER', 'PLATINUM']

# Party events: only fire on new individual parties (no downstream loop possible)
event.party_type == 'INDIVIDUAL'
```

### Depth-Based Termination (Safety Backstop)

Use `chain_depth` in CEL to hard-limit multi-hop chains:

```cel
# Allow up to 3 hops in a chain, then stop
event.instrument_code != 'GBP' && chain_depth < 3
```

## Links

### Reference Implementations

* [`valuation_on_capture.star`](../../services/control-plane/internal/applier/testdata/tenant-saga-examples/valuation_on_capture.star) — Precious metals spot valuation
* [`kyc_on_party.star`](../../services/control-plane/internal/applier/testdata/tenant-saga-examples/kyc_on_party.star) — KYC compliance marker
* [`usage_to_value.star`](../../services/control-plane/internal/applier/testdata/tenant-saga-examples/usage_to_value.star) — Energy cross-instrument valuation
* [`compute_billing.star`](../../services/control-plane/internal/applier/testdata/tenant-saga-examples/compute_billing.star) — Cloud usage billing
* [`race_result_distribution.star`](../../services/control-plane/internal/applier/testdata/tenant-saga-examples/race_result_distribution.star) — Party hierarchy payout

### Related Services

* [Event Router Service](../../services/event-router/README.md) — Kafka consumer and dispatch service
* Event Router Runbook — `docs/runbooks/event-router.md`
* [Event-Triggered Sagas Skill](../../.claude/skills/event-triggered-sagas/SKILL.md) — Configuration guide

### Related ADRs

* [ADR-0028: Starlark Saga Orchestration with CEL Valuation](0028-starlark-saga-cel-valuation.md) — Foundation
* [ADR-0014: Financial Instrument Reference Data](0014-financial-instrument-reference-data.md) — CEL compiler
* [ADR-0004: Event Schema Evolution](0004-event-schema-evolution.md) — Protobuf + Kafka conventions

### Guides

* [Manifest Design Patterns](../guides/manifest-design-patterns.md) — Trigger and filter design
* [Starlark Style Guide](../guides/starlark-style-guide.md) — Starlark syntax conventions

## Notes

### Reconsidering This Decision

Revisit if:

- The saga registry reload window causes observable missed events in production (consider push-based
  registry updates from the control plane)
- Channel proliferation causes Kafka broker connection saturation (consider a multiplexed event bus
  with topic-level routing)
- CEL filter errors become a support burden (consider surfacing filter validation warnings as manifest
  apply responses rather than silent skip-with-log)
