---
name: event-triggered-sagas
description: Configure sagas that fire reactively when Kafka events arrive using event: trigger prefix and CEL filters
triggers:
  - Adding a saga that reacts to position captures, party events, or market data observations
  - Writing CEL filters to scope which events trigger a saga
  - Preventing infinite saga chains via chain termination filters
  - Debugging why an event-triggered saga is not firing or firing too often
  - Configuring the event-router service for a new Kafka channel
instructions: |
  Use the event: prefix in the trigger field of a saga definition to fire reactively on Kafka events.
  The optional filter field accepts a CEL expression that must return bool; omit to match all events on the channel.
  Prevent infinite loops by excluding the saga's own output events from the filter (e.g., instrument_code != 'GBP' when the saga creates GBP positions).
  The CEL filter has access to event (dyn), metadata (map<string,string>), and chain_depth (int).
  Reference ADR-0033 and the manifest-design-patterns guide for detailed patterns.
---

# Event-Triggered Sagas

Configure sagas that execute reactively when Kafka events arrive on a channel.

**Related:**

- **[ADR-0033: Event-Triggered Sagas](../adr/0033-event-triggered-sagas.md)** - Architecture decision and design rationale
- **[Manifest Design Patterns](../guides/manifest-design-patterns.md)** - Trigger and filter design
- **[Starlark Style Guide](../guides/starlark-style-guide.md)** - Saga script conventions
- **[Event Router Runbook](../runbooks/event-router.md)** - Operational procedures for the dispatch service

---

## Quick Start

### Minimal Event-Triggered Saga

```yaml
saga_definitions:
  - name: my_reactive_saga
    trigger: "event:position-keeping.transaction-captured.v1"
    filter: "event.instrument_code == 'KWH' && event.direction == 'DEBIT'"
    script: |
      my_saga = saga(name="my_reactive_saga")

      def execute():
          ctx = input_data
          correlation_id = ctx["correlation_id"]
          account_id = ctx["event"]["account_id"]
          amount = Decimal(ctx["event"]["instrument_amount"]["amount"])

          step(name="process")
          position_keeping.initiate_log(
              account_id=account_id,
              instrument_code="GBP",
              direction="DEBIT",
              amount=amount,
              correlation_id=correlation_id,
          )

          return {"status": "COMPLETED"}

      output = execute()
```

### Trigger Format

```text
event:<channel-name>
```

| Part | Example | Description |
|------|---------|-------------|
| `event:` | `event:` | Fixed prefix for event-triggered sagas |
| channel name | `position-keeping.transaction-captured.v1` | Kafka channel from the AsyncAPI registry |

### Available Channels

Common channels used in event-triggered sagas:

| Channel | Events | Typical Use |
|---------|--------|-------------|
| `position-keeping.transaction-captured.v1` | Position captured | Valuation, billing, cost basis |
| `market-information.observation-recorded.v1` | Market data | Payout distribution, price updates |
| `party.created.v1` | Party registered | KYC initiation (aspirational) |

---

## CEL Filter Syntax

The `filter` field is a CEL expression evaluated for each event arriving on the channel. Only events where
the expression evaluates to `true` trigger the saga.

### Available Variables

| Variable | Type | Example Access |
|----------|------|----------------|
| `event` | `dyn` (dynamic map) | `event.instrument_code`, `event.direction` |
| `metadata` | `map(string, string)` | `metadata["x-tenant-id"]` |
| `chain_depth` | `int` | `chain_depth < 3` |

### Common Filter Patterns

**Single instrument:**

```cel
event.instrument_code == 'KWH' && event.direction == 'DEBIT'
```

**Multiple instruments (CEL `in` operator):**

```cel
event.instrument_code in ['GOLD', 'SILVER', 'PLATINUM']
```

**Exclude own output (chain termination):**

```cel
# Fires on any non-GBP debit; GBP positions created by this saga are excluded
event.instrument_code != 'GBP' && event.direction == 'DEBIT'
```

**Market data events:**

```cel
event.dataset_code == 'HORSE_RACING' && event.quality == 'ACTUAL'
```

**Party events:**

```cel
event.party_type == 'INDIVIDUAL'
```

**Depth-limited chains:**

```cel
event.instrument_code != 'GBP' && chain_depth < 3
```

### Filter Omitted (Match All)

When `filter` is absent, the saga fires on every event on the channel:

```yaml
saga_definitions:
  - name: log_all_transactions
    trigger: "event:position-keeping.transaction-captured.v1"
    # No filter — matches every transaction
    script: |
      ...
```

---

## Input Data Structure

The Starlark `input_data` dictionary contains event payload fields plus standard headers:

```python
input_data = {
    # Standard header (always present)
    "correlation_id": "550e8400-e29b-41d4-a716-446655440000",

    # Kafka metadata headers
    "metadata": {
        "x-meridian-chain-depth": "1",
        "x-tenant-id": "tenant-uuid",
        "x-meridian-correlation-id": "correlation-uuid",
    },

    # Event payload fields (from proto via AsyncAPI deserialization)
    "event": {
        "instrument_code": "KWH",
        "direction": "DEBIT",
        "amount_cents": 150000,
        "account_id": "acc-uuid",
        "log_id": "log-uuid",
        "transaction_id": "txn-uuid",
        "instrument_amount": {
            "amount": "1500.00",
            "instrument_code": "KWH",
        },
    },
}
```

### Accessing Input Data

```python
def execute():
    ctx = input_data

    # Standard headers
    correlation_id = ctx["correlation_id"]

    # Event payload fields
    account_id   = ctx["event"]["account_id"]
    instrument   = ctx["event"]["instrument_code"]
    direction    = ctx["event"]["direction"]
    amount       = Decimal(ctx["event"]["instrument_amount"]["amount"])

    # Chain depth (also available in CEL filter)
    chain_depth  = int(ctx["metadata"].get("x-meridian-chain-depth", "0"))
```

### Thin Event Pattern

The event carries only the fields published by the producer. Business data (account type, billing account,
valuation method) is resolved by the saga via service module calls:

```python
def execute():
    ctx = input_data
    account_id = ctx["event"]["account_id"]

    # Resolve business data from entity graph
    step(name="lookup_account")
    account = reference_data.get_account(id=account_id)
    billing_account_id = account.metadata["billing_account_id"]
    account_type = reference_data.get_account_type(code=account.account_type_code)
```

---

## Chain Depth

Event-triggered sagas create positions. New positions emit events. Those events can trigger more sagas.
The chain depth counter prevents infinite loops.

### How It Works

1. Initial event arrives with no chain depth header (depth = 0)
2. Saga executes and creates a new position
3. New position event is published with `x-meridian-chain-depth: 1`
4. Event arrives at event-router with depth = 1
5. CEL filter evaluates `chain_depth` = 1
6. If depth reaches `max_chain_depth` (default: 10), event is dropped with a warning log

### Correct Termination: Filter-Based

The preferred approach is to write CEL filters that exclude the saga's own output:

```cel
# Energy billing: fires on kWh debits, not on the GBP positions this saga creates
event.instrument_code == 'KWH' && event.direction == 'DEBIT'
```

### Fallback Termination: Depth-Based

Use `chain_depth` as a backstop when filter-based termination is insufficient:

```cel
event.instrument_code == 'KWH' && event.direction == 'DEBIT' && chain_depth < 5
```

### Checking Chain Depth in Starlark

```python
def execute():
    ctx = input_data
    depth = int(ctx["metadata"].get("x-meridian-chain-depth", "0"))

    # Defensive check inside the saga (belt-and-suspenders)
    if depth >= 5:
        return {"status": "CHAIN_DEPTH_LIMIT_REACHED", "depth": depth}
```

---

## Idempotency

Kafka guarantees at-least-once delivery. The same event may arrive more than once. Write sagas to be
idempotent by checking whether work has already been done before proceeding.

### Standard Idempotency Check

```python
def execute():
    ctx = input_data
    correlation_id = ctx["correlation_id"]
    settlement_account_id = "..."  # resolved from entity graph

    # Check before doing work
    step(name="check_idempotency")
    existing = position_keeping.query_logs(
        correlation_id=correlation_id,
        instrument_code="GBP",
        account_id=settlement_account_id,
    )

    if existing.count > 0:
        return {"status": "ALREADY_PROCESSED", "correlation_id": correlation_id}

    # Proceed with saga...
```

### Multi-Leg Idempotency

When a saga creates multiple positions, check that ALL expected positions exist before skipping:

```python
step(name="check_retail")
existing_retail = position_keeping.query_logs(
    correlation_id=correlation_id,
    instrument_code="GBP",
    account_id=retail_account_id,
)

step(name="check_wholesale")
existing_wholesale = position_keeping.query_logs(
    correlation_id=correlation_id,
    instrument_code="GBP",
    account_id=wholesale_account_id,
)

# Both must exist — checking only one is insufficient
if existing_retail.count > 0 and existing_wholesale.count > 0:
    return {"status": "ALREADY_VALUED"}
```

The event-router also deduplicates at the dispatch level using a CockroachDB-backed idempotency store
keyed on `(sagaName, correlationID)`.

---

## Examples

### Precious Metals: Spot Rate Settlement

**File:** [`valuation_on_capture.star`](../../services/control-plane/internal/applier/testdata/tenant-saga-examples/valuation_on_capture.star)

```yaml
trigger: "event:position-keeping.transaction-captured.v1"
filter:  "event.instrument_code in ['GOLD', 'SILVER', 'PLATINUM']"
```

Pattern: Resolve account → check idempotency → look up account type → compute valuation → book GBP settlement.

### Cloud Billing: Usage to Value

**File:** [`compute_billing.star`](../../services/control-plane/internal/applier/testdata/tenant-saga-examples/compute_billing.star)

```yaml
trigger: "event:position-keeping.transaction-captured.v1"
filter:  "event.instrument_code == 'GPU_HOUR' && event.direction == 'DEBIT'"
```

Pattern: Single-leg valuation — GPU_HOUR position → USD charge.

### Compliance: KYC Initiation

**File:** [`kyc_on_party.star`](../../services/control-plane/internal/applier/testdata/tenant-saga-examples/kyc_on_party.star)

```yaml
trigger: "event:party.created.v1"
filter:  "event.party_type == 'INDIVIDUAL'"
```

Pattern: Resolve party → check idempotency → find compliance account by jurisdiction → book zero-amount marker position.

---

## Troubleshooting

### Saga Not Firing

**Check 1:** Is the channel name correct?

```bash
# Confirm the channel exists in the AsyncAPI registry
# The trigger value must match exactly (case-sensitive)
trigger: "event:position-keeping.transaction-captured.v1"
```

**Check 2:** Does the CEL filter match? Test with `meridian_cel_validate`:

```bash
# Validate CEL expression compiles
mcp__meridian__meridian_cel_validate(
  expression: "event.instrument_code == 'KWH' && event.direction == 'DEBIT'",
  environment: "eligibility"
)
```

**Check 3:** Check event-router logs for filter evaluation:

```bash
kubectl logs -n production deployment/event-router | grep "CEL filter did not match"
kubectl logs -n production deployment/event-router | grep "CEL filter evaluation error"
```

**Check 4:** Check Prometheus metrics:

```promql
# Events received on the channel
meridian_event_router_events_received_total{channel="position-keeping.transaction-captured.v1"}

# Sagas triggered
meridian_event_router_sagas_triggered_total{saga_name="my_saga"}

# Filter evaluation errors
meridian_event_router_filter_evaluation_errors_total{saga_name="my_saga"}
```

### Saga Firing Too Often (Loop)

**Symptom:** `meridian_event_router_chain_depth_exceeded_total` is non-zero, or sagas run repeatedly.

**Fix 1:** Add chain termination to the filter:

```cel
# Before (loops)
event.direction == 'DEBIT'

# After (terminates)
event.instrument_code == 'KWH' && event.direction == 'DEBIT'
#                               ^ saga creates GBP positions, not KWH
```

**Fix 2:** Add depth limit to the filter:

```cel
event.instrument_code == 'KWH' && event.direction == 'DEBIT' && chain_depth < 3
```

**Fix 3:** Add idempotency check inside the saga to prevent re-processing.

### Duplicate Executions

**Symptom:** `meridian_event_router_duplicate_events_total` is non-zero.

This is expected behavior. The event-router's idempotency store deduplicates by `(sagaName, correlationID)`.
Duplicates are silently skipped and recorded in the metric. Verify the saga also performs an internal
idempotency check for defense-in-depth.

### Chain Depth Exceeded

**Symptom:** `meridian_event_router_chain_depth_exceeded_total` is incrementing.

Events are being dropped because the chain depth reached the maximum (default: 10). The filter is not
terminating the chain correctly. See **Saga Firing Too Often** above.

---

## Syntax Checklist

Before applying a manifest with event-triggered sagas:

- [ ] Trigger uses `event:` prefix followed by valid channel name
- [ ] CEL filter compiles without errors (test with `meridian_cel_validate`)
- [ ] CEL filter returns `bool` (not `int`, not `string`)
- [ ] Filter excludes output events (chain termination is correct)
- [ ] Saga has an idempotency check before creating positions
- [ ] Multi-leg sagas check ALL legs, not just one
- [ ] Input data fields accessed via `ctx["event"]["field"]` not `ctx["field"]`
- [ ] Decimal amounts wrapped with `Decimal(...)` before arithmetic

---

## Further Reading

- **[ADR-0033: Event-Triggered Sagas](../adr/0033-event-triggered-sagas.md)** - Design decisions and consequences
- **[Manifest Design Patterns](../guides/manifest-design-patterns.md)** - Pattern catalog with examples
- **[Starlark Style Guide](../guides/starlark-style-guide.md)** - Comprehensive syntax conventions
- **[Event Router Runbook](../runbooks/event-router.md)** - Operational procedures
- **[Example Scripts](../../services/control-plane/internal/applier/testdata/tenant-saga-examples/)** - Reference implementations
