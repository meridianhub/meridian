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
  - The existing utilization-metering-consumer is generalized and renamed to event-router —
    a single service that routes ALL platform events to saga handlers via CEL filters
  - The event-router's current hard-coded metering logic becomes a platform Starlark saga,
    making it another rule not an exception — tenant utilization data stays in the tenant's
    own schema, not tenant-zero
  - CEL filters on saga definitions determine applicability — the tenant decides what
    conditions make a saga fire, not the entity type. This handles events from any domain.
  - Idempotency uses correlation_id from the source event to prevent duplicate processing
  - Event chain termination: CEL filters naturally reject downstream events + max chain
    depth safety net via causation_id
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

Account type definitions carry fields that imply reactive behavior:

- `DefaultSagaPrefix` — saga naming prefix for operations on this account type
- `DefaultConversionMethodID` / `DefaultConversionMethodVersion` — default valuation method
- `ValuationMethods` — array of Starlark valuation method references

These fields are populated but not consumed by any runtime logic. With event-triggered
sagas and CEL filters, these fields become queryable context rather than routing
configuration — a saga's CEL filter can reference account type metadata when deciding
whether to fire, and the saga script can read them to determine which valuation method
to use.

### 1.3 The Pattern is Industry-Agnostic

The `event:` trigger enables any reactive workflow a tenant defines in Starlark:

| Industry | Event | Saga | Effect |
|----------|-------|------|--------|
| Energy | kWh position captured | Cross-instrument valuation | GBP positions booked |
| Wealth | Corporate action recorded | Cost basis adjustment | GBP cost basis updated |
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

### 2.2 CEL Filter for Saga Applicability

Not every event on a channel should trigger a saga. Each saga definition includes a
`filter` — a CEL expression evaluated against the event payload. If the filter returns
true, the saga executes. If false, the event is dropped for that saga.

```json
{
  "name": "usage-to-value",
  "trigger": "event:position-keeping.transaction-captured.v1",
  "filter": "event.instrument_code != 'GBP' && event.direction == 'DEBIT'",
  "script": "..."
}
```

This is the same CEL infrastructure already used for account type validation, bucketing,
and eligibility — sub-millisecond evaluation, guaranteed termination, no side effects.

**Routing flow:**

```text
Event arrives on channel
  → Find all saga definitions with event: trigger matching this channel
  → For each matching saga:
    → Evaluate CEL filter against event payload
    → Filter returns false → skip (sub-millisecond, no saga load)
    → Filter returns true  → execute saga via SagaTrigger port
```

**Why CEL instead of account type policies:**

The entity graph is broader than accounts. Events can originate from any service domain:

| Event Source | Example | Routing Logic |
|-------------|---------|---------------|
| Position-keeping | kWh position captured | Filter on instrument_code, account_type |
| Market data | Horse race result recorded | Filter on dataset_code, attributes |
| Party | Organization onboarded | Filter on party_type, relationship_type |
| Payment | Instruction completed | Filter on payment_type, status |
| Reference data | Valuation rule updated | Filter on instrument_code, method |

Hardcoding routing through account type policies would only cover the first case. CEL
filters handle all of them — the tenant decides what conditions make a saga applicable.

**Event chain termination** is also handled by CEL filters. A valuation saga creates GBP
positions, which emit further `transaction-captured` events. The saga's filter
(`event.instrument_code != 'GBP'`) rejects them — no special-case logic needed.

### 2.3 The Entity Graph

Sagas navigate an entity graph stored across reference-data and operational services.
The graph has two axes: **what things are** (reference data definitions) and **who owns
them** (operational relationships).

```text
Reference Data (definitions — the schema)
├── Instruments        (KWH, GBP, GPU_HOUR, TONNE_CO2E)
├── Account Types      (METERED_USAGE, BILLING, CLEARING)
├── Party Types        (PERSON, ORGANIZATION)
├── Valuation Rules    (KWH→GBP, GPU_HOUR→USD)
├── Saga Definitions   (event-triggered workflows — this PRD)
├── Market Data Sets   (observations, rates, race results)
└── Mappings           (field transformations)

Operational Data (instances — the state)
├── Accounts           (customer + internal, typed by account type)
├── Parties            (individuals + organizations)
│   └── Associations   (BENEFICIAL_OWNER, SYNDICATE_PARTICIPANT, ...)
├── Positions          (quantities on accounts)
├── Payments           (instructions between accounts)
└── Reconciliation     (variance tracking)
```

Sagas traverse this graph via existing service modules:

- `reference_data.get_instrument(...)` — look up instrument definitions
- `reference_data.get_account_type(...)` — look up account type and its CEL policies
- `party.list_participants(org_id, relationship_type)` — traverse party hierarchy
- `party.get_structuring_data(party_id, org_id, ...)` — get allocation shares
- `position_keeping.initiate_log(...)` — book positions on accounts
- `valuation_engine.compute(...)` — convert between instruments

The `event:` trigger fires the saga. The CEL filter decides if it's applicable. The
Starlark script navigates the graph to do the work. The platform owns the first two;
the tenant owns the third.

### 2.4 Manifest Declaration

A tenant manifest declares event-triggered sagas with their CEL filters. No changes to
account type definitions are needed — the routing logic lives on the saga, not the entity.

```json
{
  "sagas": [
    {
      "name": "usage-to-value",
      "trigger": "event:position-keeping.transaction-captured.v1",
      "filter": "event.instrument_code != 'GBP' && event.direction == 'DEBIT'",
      "script": "..."
    },
    {
      "name": "race-result-distribution",
      "trigger": "event:market-information.observation-recorded.v1",
      "filter": "event.dataset_code == 'HORSE_RACING' && event.status == 'OFFICIAL'",
      "script": "..."
    },
    {
      "name": "party-onboarding-provisioning",
      "trigger": "event:party.created.v1",
      "filter": "event.party_type == 'ORGANIZATION'",
      "script": "..."
    }
  ]
}
```

Each saga stands alone — `trigger` says what channel, `filter` says when, `script` says
what. No indirect wiring through account type policies or party relationships.

### 2.5 The Event Router (Service Generalization)

The existing `utilization-metering-consumer` is generalized and renamed to
**`event-router`**. The current service already has the right infrastructure — Kafka
consumer, proto deserialization, hexagonal ports, at-least-once semantics with manual
offset commits. What changes is its scope: instead of hard-coded Go metering logic, it
becomes the platform's general-purpose event routing layer.

**Current state (`utilization-metering-consumer`):**

- Consumes audit events from 6 Kafka topics
- Hard-coded Go transformer converts events to utilization measurements
- Records measurements to Position Keeping on a tenant-zero account
- Optionally publishes aggregated metrics to Market Data Service

**Target state (`event-router`):**

- Consumes events from ALL platform Kafka topics (not just audit)
- Evaluates CEL filters from saga trigger definitions to determine routing
- Dispatches matching events to the saga runtime for execution
- Platform metering becomes a Starlark saga alongside tenant sagas (see 2.9)

```text
┌──────────────────────────────────────────────────────────────┐
│                      EVENT ROUTER                            │
│                                                              │
│  For each event:                                             │
│    1. Deserialize proto message                              │
│    2. Find saga definitions with matching event: trigger     │
│    3. Evaluate CEL filter against event payload              │
│    4. For each match → SagaTrigger.TriggerSaga()             │
│                                                              │
│  Routes to:                                                  │
│    • Tenant sagas  (usage-to-value, race-distribution, ...)  │
│    • Platform sagas (utilization-metering — see 2.9)         │
│                                                              │
└──────────────────────────────────────────────────────────────┘
                          ▲
                          │ Kafka consumer
                          │
         ┌────────────────┼────────────────┐
         │                │                │
   ┌─────┴──────┐  ┌─────┴──────┐  ┌─────┴──────┐
   │ position-   │  │ market-     │  │ audit      │
   │ keeping     │  │ information │  │ events     │
   │ events      │  │ events      │  │ (metering) │
   └─────────────┘  └─────────────┘  └─────────────┘
```

The saga runtime provides:

- Starlark execution environment
- Handler registry and typed service clients
- Compensation logic
- The `SagaTrigger` interface (already wired for Stripe webhooks)
- Observability (structured logging, metrics)
- Multi-tenancy (tenant-scoped execution)
- Error handling (retry, compensation, dead-letter)

### 2.6 Idempotency

Event consumers reprocess events on restart/rebalance. The `correlation_id` standard
header (formalized by PRD-030) is the natural idempotency key:

1. Event consumer adapter delivers event to saga runtime via `TriggerSaga()`
2. Saga checks whether this `correlation_id` has already been processed
3. If yes, return early (no duplicate work)
4. If no, proceed with saga execution

This is saga-level idempotency, not consumer-level. The consumer is at-least-once; the
saga provides effectively-once business behavior through idempotency checks. How the saga checks
for prior processing is up to the Starlark script — typically a query against
position-keeping or a dedicated idempotency store.

The `tenant_id` header (also from PRD-030) provides multi-tenant scoping — the consumer
routes events to the correct tenant's saga definitions and CEL filters.

### 2.7 Event Chain Termination

Sagas that create downstream events will potentially re-trigger. This creates a loop risk:

```text
Position A → event → saga → Position B → event → ???
```

Termination is guaranteed by the CEL filter. A valuation saga with filter
`event.instrument_code != 'GBP'` naturally rejects the GBP positions it creates.
No special-case logic — the tenant writes their filter to match only the events they
want. If no saga's filter matches, the event is dropped after CEL evaluation
(sub-millisecond, no saga loading).

**Tenants own the termination contract.** A poorly written filter could create a loop.
The platform should enforce a configurable maximum chain depth (e.g., 5) as a safety net,
tracked via the `causation_id` header from PRD-030.

### 2.8 Eventual Consistency

There is a window (seconds) between the source event and the saga's side effects
materializing. This is inherent to event-driven architectures. Tenants design their
settlement periods and reconciliation cycles around this — the platform does not promise
synchronous execution for event-triggered sagas.

### 2.9 Platform Metering as a Starlark Saga

The current `utilization-metering-consumer` has hard-coded Go logic that transforms
audit events into utilization measurements and records them to a tenant-zero account.
This is the exact pattern that event-triggered sagas solve — it should be a Starlark
saga, not compiled Go. Another rule, not an exception.

**Current architecture (hard-coded):**

```text
Audit event → Go transformer → Position Keeping (tenant-zero account)
```

**Target architecture (Starlark saga):**

```text
Audit event → event-router → CEL filter → platform_metering.star saga
                                            → Position Keeping (tenant's own schema)
```

**Key changes:**

1. **Metering logic in Starlark, not Go.** The transformation from audit event to
   utilization measurement becomes a saga script. This makes the metering logic visible,
   auditable, and configurable per tenant.

2. **Tenant's own schema, not tenant-zero.** Utilization data belongs to the tenant it
   describes. Each tenant's metering positions live in their own position-keeping schema,
   not a shared platform account. This aligns with multi-tenancy principles — tenants
   can query their own utilization data directly.

3. **Configurable per tenant.** Different tenants may want different metering granularity
   or instruments tracked. A wealth management tenant may not need API call counting.
   An energy tenant may want sub-second metering resolution. The Starlark saga makes
   this tenant-configurable via the manifest.

4. **Platform provisions a default metering saga.** On tenant creation, the platform
   includes a default `platform_metering` saga in the tenant's manifest. Tenants can
   modify or disable it. The platform still has visibility via the tenant's own
   position data.

**Example platform metering saga:**

```python
# Trigger: event:audit.operation-recorded.v1
# Filter:  event.tenant_id == tenant.id
#
# Records a utilization measurement in the tenant's own schema
# for each auditable operation (API calls, transactions, storage).

def execute_metering():
    ctx = input_data
    correlation_id = ctx["correlation_id"]

    step(name="check_idempotency")
    existing = position_keeping.query_logs(
        correlation_id=correlation_id,
        instrument_code=ctx["instrument_code"],
    )
    if existing.count > 0:
        return {"status": "ALREADY_METERED"}

    step(name="record_utilization")
    position_keeping.initiate_log(
        account_id=ctx["metering_account_id"],
        instrument_code=ctx["instrument_code"],
        direction="DEBIT",
        amount=Decimal(ctx["quantity"]),
        correlation_id=correlation_id,
        description=ctx["service_name"] + ":" + ctx["operation_type"],
    )

    return {"status": "METERED", "instrument": ctx["instrument_code"]}
```

This saga follows the same patterns as every other tenant saga — idempotency check,
step markers, position booking. The event-router treats it identically to tenant
business sagas. The only difference is that the platform provisions it by default.

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
- Automatic compensation (if step N fails, steps N-1 to 1 roll back)
- `SagaTrigger` interface already used by Stripe webhook adapter in control-plane

### 3.3 Valuation Engine

- `shared/pkg/valuation/` and `shared/pkg/valuationfeature/`
- Starlark-sandboxed, <5ms target, 5s timeout, 64MB memory limit
- In-memory cache with 5-minute TTL
- Available as a service module in Starlark sagas (`valuation_engine.compute(...)`)

### 3.4 CEL Infrastructure

- CEL evaluator already exists (`shared/pkg/saga/cel_evaluator.go`)
- Three CEL environments in use: validation, bucket_key, eligibility
- Account type definitions use CEL for validation_cel, bucketing_cel, eligibility_cel
- Instrument definitions use CEL for validation_expression, fungibility_key_expression
- Sub-millisecond evaluation, guaranteed termination, no side effects

### 3.5 Entity Graph Navigation (Starlark Service Modules)

- `party.list_participants(org_id, relationship_type)` — traverse party hierarchy
- `party.get_structuring_data(party_id, org_id, ...)` — get allocation metadata
- `reference_data.get_instrument(...)` / `reference_data.get_account_type(...)` — lookups
- Results cached in saga LookupCache for deterministic replay

### 3.6 Existing Event Consumer: `utilization-metering-consumer`

The service to be generalized into the event-router (see 2.5):

- Kafka consumer subscribing to 6 audit event topics
- Hexagonal ports: `PositionKeepingClient`, `UtilizationPublisher` (interfaces)
- Transport adapter: `AuditConsumer` wraps `MessageHandler` — domain never sees Kafka
- Handler signature: `func(ctx context.Context, key []byte, msg proto.Message) error`
- Fan-out: primary output (gRPC to PK) + optional secondary (MDS publisher)
- Hard-coded metering logic to be replaced by platform Starlark saga (see 2.9)
- Currently records to tenant-zero account — to be migrated to tenant's own schema

### 3.7 Direct Saga Trigger (Stripe Webhook Consumer)

- `PaymentEventConsumer` receives HTTP webhook, calls `SagaTrigger.TriggerSaga()`
- No message broker involved — event arrives via HTTP, saga executes immediately
- Same `SagaTrigger` port the `event:` trigger would use

## 4. Scope

### 4.1 In Scope

- `event:` trigger type in manifest schema (proto + validator + planner + applier)
- `filter` field on saga definitions — CEL expression for applicability
- New CEL environment (`event_filter`) with event payload variables
- **Rename `utilization-metering-consumer` to `event-router`** — generalize from
  hard-coded metering to CEL-filtered saga dispatch for all platform events
- **Convert platform metering to a Starlark saga** — the hard-coded Go transformation
  logic becomes a `platform_metering.star` saga provisioned by default on tenant creation
- **Tenant-scoped utilization data** — metering positions recorded in the tenant's own
  schema, not tenant-zero
- Event consumer adapter wired to the existing `SagaTrigger` port in the saga runtime
- Idempotency contract (correlation_id as natural key)
- Event chain termination via CEL filters + max chain depth safety net
- Saga definition caching in consumer (with TTL — definitions change infrequently)
- Manifest validation: event-triggered sagas reference channels defined in AsyncAPI specs
  (PRD-030), and filter expressions compile in the `event_filter` CEL environment

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
- MDS aggregation publisher migration (the event-router's optional MDS fan-out can remain
  as a separate concern, migrated in a follow-up)

## 5. Reference: Tenant Manifest Examples

These examples illustrate how tenants in different industries would use the `event:`
trigger. They are **tenant configuration, not platform code** — included here to
validate that the platform infrastructure is sufficiently general.

### 5.1 Energy: Position Valuation

An energy retailer values kWh meter reads at retail and wholesale rates:

```python
def execute(ctx):
    # Idempotency: check both legs are complete (not just one)
    retail_logs = position_keeping.query_logs(
        correlation_id=ctx.correlation_id,
        instrument_code="GBP",
        account_id=ctx.event.metadata.billing_account_id,
    )
    wholesale_logs = position_keeping.query_logs(
        correlation_id=ctx.correlation_id,
        instrument_code="GBP",
        account_id=ctx.event.metadata.counterparty_account_id,
    )
    if retail_logs.count > 0 and wholesale_logs.count > 0:
        return {"status": "ALREADY_PROCESSED"}

    # Look up account type to get valuation method references
    # These are the DefaultConversionMethodID and ValuationMethods fields
    # defined on the account type in reference-data
    step()
    account_type = reference_data.get_account_type(
        code=ctx.event.account_type_code,
    )

    # Value at retail rate using the account type's default conversion method
    step()
    retail = valuation_engine.compute(
        method_id=account_type.default_conversion_method_id,
        amount=ctx.event.amount,
        from_instrument=ctx.event.instrument_code,
        to_instrument="GBP",
    )

    # Book customer billing position
    step()
    position_keeping.initiate_log(
        account_id=ctx.event.metadata.billing_account_id,
        instrument_code="GBP",
        direction="DEBIT",
        amount=retail.amount,
        correlation_id=ctx.correlation_id,
    )

    # Value at wholesale rate (second entry in ValuationMethods array)
    wholesale_method = account_type.valuation_methods[1]
    step()
    wholesale = valuation_engine.compute(
        method_id=wholesale_method.method_id,
        amount=ctx.event.amount,
        from_instrument=ctx.event.instrument_code,
        to_instrument="GBP",
    )

    step()
    position_keeping.initiate_log(
        account_id=ctx.event.metadata.counterparty_account_id,
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
        account_id=ctx.event.metadata.billing_account_id,
    )
    if existing.count > 0:
        return {"status": "ALREADY_BILLED"}

    # Look up account type for its default conversion method
    step()
    account_type = reference_data.get_account_type(
        code=ctx.event.account_type_code,
    )

    step()
    charge = valuation_engine.compute(
        method_id=account_type.default_conversion_method_id,
        amount=ctx.event.amount,
        from_instrument="GPU_HOUR",
        to_instrument="USD",
    )

    step()
    position_keeping.initiate_log(
        account_id=ctx.event.metadata.billing_account_id,
        instrument_code="USD",
        direction="DEBIT",
        amount=charge.amount,
        correlation_id=ctx.correlation_id,
    )

    return {"status": "BILLED", "amount": str(charge.amount)}
```

### 5.3 Betting: Market Event Triggers Party Distribution

A betting platform distributes pot winnings across an organization's syndicate members
when a horse race completes. The event comes from market data, not position-keeping —
the saga navigates the party hierarchy to determine payouts.

```python
def execute(ctx):
    # ctx.event contains the market data observation
    race_id = ctx.event.reference
    results = ctx.event.attributes

    # Find the syndicate organization that placed bets on this race
    step()
    syndicate = reference_data.query(
        entity_type="party",
        filter="attributes.active_race_id == '" + race_id + "'",
    )
    if syndicate.count == 0:
        return {"status": "NO_SYNDICATE", "race_id": race_id}

    # Traverse party hierarchy — get all syndicate participants
    step()
    participants = party.list_participants(
        org_id=syndicate.items[0].party_id,
        relationship_type="SYNDICATE_PARTICIPANT",
    )

    # Calculate pot and distribute by allocation share
    pot = Decimal(results.total_pot)
    for p in participants:
        step()
        structuring = party.get_structuring_data(
            party_id=p.party_id,
            org_id=syndicate.items[0].party_id,
            relationship_type="SYNDICATE_PARTICIPANT",
        )
        payout = pot * Decimal(structuring.allocation_share)

        step()
        position_keeping.initiate_log(
            account_id=p.metadata.payout_account_id,
            instrument_code="GBP",
            direction="CREDIT",
            amount=payout,
            correlation_id=ctx.correlation_id,
            description="Race " + race_id + " payout: " + str(structuring.allocation_share),
        )

    return {"status": "DISTRIBUTED", "race_id": race_id, "participants": len(participants)}
```

This saga demonstrates the full entity graph traversal: market data event triggers
lookup of a party organization, traversal of its syndicate hierarchy, retrieval of each
participant's structuring data (allocation shares), and position booking on each
participant's account.

### 5.4 Wealth: Corporate Action Cost Basis Tracking

A wealth platform tracks portfolio holdings with both market value and cost basis views.
The position is one set of units on a custody account. Market value is a valuation engine
computation (units x latest price). Cost basis is a separate GBP account that adjusts
when purchases or corporate actions occur — even when no cash or units move.

Account structure per client per instrument:

```text
Custody Account (VANGUARD_FTSE_GLOBAL)   ← what you own (units)
Cost Basis Account (GBP)                 ← what it cost (adjusted for phantom events)
Market Value                             ← not an account — valuation engine computation
```

The phantom event problem: an accumulating ETF dividend reinvests automatically. No
cash moves. No units change. But the cost basis increases, affecting CGT on eventual
disposal. Traditional systems miss this. Meridian records it as a position on the cost
basis account.

Manifest declaration:

```json
{
  "sagas": [
    {
      "name": "corporate-action-cost-adjustment",
      "trigger": "event:market-information.corporate-action.v1",
      "filter": "event.action_type == 'ACCUMULATING_DIVIDEND'",
      "script": "corporate_action_cost_adjustment.star"
    },
    {
      "name": "stock-split-adjustment",
      "trigger": "event:market-information.corporate-action.v1",
      "filter": "event.action_type == 'STOCK_SPLIT'",
      "script": "stock_split_adjustment.star"
    }
  ]
}
```

Saga script (`corporate_action_cost_adjustment.star`):

```python
def execute(ctx):
    # Find all custody accounts holding this instrument
    step()
    holdings = position_keeping.query_accounts(
        instrument_code=ctx.event.instrument_code,
    )

    adjustments = []
    for holding in holdings:
        # Get current units on this account
        step()
        position = position_keeping.get_balance(
            account_id=holding.account_id,
        )
        if position.amount == 0:
            continue

        # Cost basis adjustment: units x dividend per unit
        # No cash moves. No units change. But the tax position changed.
        adjustment = position.amount * Decimal(ctx.event.amount_per_unit)

        step()
        position_keeping.initiate_log(
            account_id=holding.metadata.cost_basis_account_id,
            instrument_code="GBP",
            direction="CREDIT",
            amount=adjustment,
            correlation_id=ctx.correlation_id,
            description="Accumulating dividend: " + ctx.event.instrument_code,
            reference=ctx.event.ex_date,
        )
        adjustments.append({"account": holding.account_id, "adjustment": str(adjustment)})

    return {"status": "ADJUSTED", "holdings": len(adjustments)}
```

Resulting cost basis position log (the audit trail):

| Date | Event | Cost Basis | Change |
|------|-------|-----------|--------|
| 2023-01-15 | Purchase | £8,000 | +£8,000 |
| 2023-04-01 | Accumulating dividend | £8,320 | +£320 |
| 2023-07-01 | Accumulating dividend | £8,660 | +£340 |
| 2023-09-20 | Purchase | £9,800 | +£1,140 |
| 2023-10-01 | Accumulating dividend | £10,150 | +£350 |

That table IS the position log on the cost basis account. Not reconstructed — retrieved.

The two truths, from the same instant:

- Market value: 450 units x £27.56 (latest price) = **£12,400**
- Cost basis: sum of cost basis account = **£9,800**
- Unrealised gain: £12,400 - £9,800 = **£2,600**

A market price change affects the first number. A corporate action affects the second.
Neither touches the custody account's unit count. One position, multiple views.

### 5.5 Pilot Context: Outfox Energy

The first deployment of event-triggered sagas is the Outfox reconciliation pilot:

- CSV-imported HH reads (48 settlement periods/day per MPAN)
- Automatic valuation at customer retail tariff
- Automatic GSP wholesale-side booking
- Reconcilable output for dual-publish comparison

This validates the platform infrastructure with real data. The saga script and manifest
configuration are tenant-owned — Meridian provides the `event:` trigger, the CEL filter,
and the saga runtime.

## 6. Open Questions

1. **CEL environment for event filters**: The `event_filter` environment needs access to
   the event payload fields. Should it also have access to reference data (e.g., account
   type lookups) for richer filtering, or should filters be pure event-payload expressions
   to keep evaluation cheap?

2. **Multiple sagas per channel**: If two saga definitions both match the same event
   (same channel, both CEL filters return true), should both execute independently? This
   seems correct (each saga has its own compensation chain) but needs explicit design.

3. **Max chain depth**: What should the default max chain depth be for safety? 5?
   Should it be configurable per tenant or per saga?

4. **Dead letter handling**: When a saga fails after retries, should the event go to a
   dead-letter queue for manual review, or should the consumer retry indefinitely with
   backoff?

5. **Filter compilation at manifest apply**: CEL filters should be compiled and validated
   when the manifest is applied (like Starlark scripts). What variables should the
   `event_filter` environment expose? At minimum: `event.*` fields from the channel's
   proto payload schema.

## 7. Success Criteria

### Platform

- Event-triggered sagas are declarable in tenant manifests using `event:` trigger + CEL
  `filter`
- CEL filters compiled and validated at manifest apply time (same as Starlark scripts)
- Events with no matching saga filter are dropped in <1ms (CEL evaluation only)
- Idempotent: reprocessing the same event produces no duplicate side effects
- Event chains terminate via CEL filters + max chain depth safety net
- Saga compensation works: if a step fails, prior steps roll back
- `utilization-metering-consumer` renamed to `event-router` and generalized to route
  all platform events via CEL-filtered saga dispatch
- Hard-coded metering logic replaced by a platform-provisioned Starlark saga
- Tenant utilization data recorded in the tenant's own schema, not tenant-zero
- Sagas can navigate the full entity graph (accounts, parties, hierarchies) via existing
  service modules

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
