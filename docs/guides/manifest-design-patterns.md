# Manifest Design Patterns

How to design a tenant economy in Meridian. Covers instruments, account
structures, valuation rules, saga triggers, and CEL filters.

**Audience:** Developers configuring tenants, AI assistants guiding tenant
onboarding, and platform engineers extending the reference examples.

**Related:**

- [Starlark Style Guide](starlark-style-guide.md) — Syntax conventions for
  saga scripts
- [Saga Validation Guide](saga-validation.md) — Validation layers and
  troubleshooting
- [PRD-032 Examples](../prd/032-examples/) — Industry-spanning reference
  implementations

---

## Economy Building Blocks

A Meridian tenant economy is defined by a manifest containing five primitives:

| Primitive | Purpose | Example |
|-----------|---------|---------|
| **Instrument** | Unit of value (what you track) | GBP, kWh, GPU_HOUR, TONNE_CO2E |
| **Account type** | Container template (how you track it) | custody_account, cost_basis_account |
| **Valuation rule** | Conversion logic (how you price it) | kWh_to_gbp_retail, gpu_hour_to_usd |
| **Saga definition** | Workflow (what happens when) | usage_to_value, compute_billing |
| **Trigger** | Event routing (when it fires) | event:position-keeping.transaction-captured.v1 |

The design question is always: **what instruments do you need, what accounts
hold them, what events trigger valuations, and what sagas execute the logic?**

---

## Design Patterns

### Pattern 1: Cross-Instrument Valuation

**When to use:** A transaction in one instrument (e.g. kWh, GPU_HOUR) needs to
produce a monetary value in another instrument (e.g. GBP, USD).

**Reference:** [`usage_to_value.star`](../prd/032-examples/usage_to_value.star)

**Account model:**

```text
Customer Account (kWh)     ← source transaction lands here
├── Retail GBP Account     ← saga creates valuation at retail rate
└── Wholesale GBP Account  ← saga creates valuation at wholesale rate
```

**Flow:**

1. Position captured on kWh account (platform event)
2. CEL filter matches: `event.instrument_code != 'GBP' && event.direction == 'DEBIT'`
3. Saga looks up account type metadata for valuation methods
4. Saga calls `valuation_engine.compute()` for each method
5. Saga books GBP positions on target accounts

**Key decisions:**

- **Number of legs:** One valuation method = one leg. Energy often needs two
  (retail + wholesale). Cloud billing typically needs one.
- **Chain termination:** The GBP positions created by the saga also emit events.
  The CEL filter (`instrument_code != 'GBP'`) prevents infinite loops.
- **Idempotency:** Check ALL expected legs exist before proceeding. A single
  `count > 0` check is insufficient when multiple legs are expected.

**Idempotency guard (multi-leg):**

```python
# Check BOTH legs exist, not just one
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

if existing_retail.count > 0 and existing_wholesale.count > 0:
    return {"status": "ALREADY_VALUED"}
```

### Pattern 2: Usage Billing (Single-Leg Valuation)

**When to use:** Simpler variant of cross-instrument valuation with a single
target instrument.

**Reference:** [`compute_billing.star`](../prd/032-examples/compute_billing.star)

**Account model:**

```text
Usage Account (GPU_HOUR)   ← source transaction
└── Billing Account (USD)  ← saga creates charge
```

**This is Pattern 1 with one leg instead of two.** Same flow: look up account
type, get conversion method, compute, book. The simplification means a single
idempotency check suffices.

### Pattern 3: Entity Graph Distribution

**When to use:** An event triggers traversal of a party hierarchy, with
position booking per participant based on structuring data (allocation shares).

**Reference:** [`race_result_distribution.star`](../prd/032-examples/race_result_distribution.star)

**Account model:**

```text
Syndicate Organization
├── Participant A (40% share) → Payout Account (GBP)
├── Participant B (35% share) → Payout Account (GBP)
└── Participant C (25% share) → Payout Account (GBP)
```

**Flow:**

1. Market data event (e.g. race result becomes OFFICIAL)
2. CEL filter matches: `event.dataset_code == 'HORSE_RACING' && event.status == 'OFFICIAL'`
3. Saga queries reference data for the syndicate organization
4. Saga traverses party hierarchy to find participants
5. For each participant: get structuring data (allocation share), compute payout, book position

**Key decisions:**

- **Event source is NOT position-keeping.** This pattern triggers from market
  data, reference data updates, or external webhooks.
- **Dynamic step names:** Use `step(name="book_payout_" + str(i))` when
  iterating over a variable-length collection.
- **Structuring data drives allocation.** The party hierarchy contains the
  business logic (allocation shares), not the saga script.

### Pattern 4: Phantom Events (One Position, Multiple Views)

**When to use:** An economic event changes the value of a holding without moving
cash or units. Cost basis adjustment, mark-to-market, tax events.

**Reference:** [`corporate_action_cost_adjustment.star`](../prd/032-examples/corporate_action_cost_adjustment.star)

**Account model (per client per instrument):**

```text
Holding
├── Custody Account (instrument units)  ← what you own (unchanged by this saga)
├── Cost Basis Account (GBP)           ← what it cost (adjusted by this saga)
└── Market Value                       ← not an account, valuation engine computes at query time
```

**Flow:**

1. Corporate action event (e.g. accumulating ETF dividend)
2. CEL filter matches: `event.action_type == 'ACCUMULATING_DIVIDEND'`
3. Saga queries all custody accounts holding the affected instrument
4. For each holding: get balance (units), compute adjustment (units x dividend per unit)
5. Book GBP adjustment on the cost basis account

**Key decisions:**

- **Cost basis is a separate account.** Not a computed field, not stored as
  metadata — it's a real GBP position with its own audit trail.
- **The position log IS the audit trail.** For tax reporting, you don't
  reconstruct from trade confirmations — you read the position log directly.
- **No cash moves.** The CREDIT on the cost basis account has no corresponding
  DEBIT anywhere. This is a phantom event that changes an accounting view.
- **"Two truths" pattern:** Market value and cost basis are independent views
  of the same holding. They diverge by design.

---

## Trigger and Filter Design

### Choosing a Trigger Type

| Trigger | When to use | Example |
|---------|-------------|---------|
| `api:` | User-initiated actions (deposit, withdrawal) | `api:current-account.withdrawal.v1` |
| `webhook:` | External system integration | `webhook:stripe.payment-intent.v1` |
| `scheduled:` | Periodic jobs (settlement, reconciliation) | `scheduled:daily-settlement.v1` |
| `event:` | Reacting to platform events | `event:position-keeping.transaction-captured.v1` |

**Use `event:` when:**

- A position change should trigger a derived calculation
- A market data observation should trigger downstream actions
- An entity update should propagate through a hierarchy

**Chain termination:** Event-triggered sagas can create new events. Use CEL
filters to prevent infinite loops. The filter must exclude the saga's own
output events from re-triggering it.

### Writing CEL Filters

CEL filters determine which events trigger a saga. They execute in < 1ms and
have access to the event payload fields.

**Good filters are specific:**

```cel
# Specific instrument + direction
event.instrument_code == 'GPU_HOUR' && event.direction == 'DEBIT'

# Exclude own output (chain termination)
event.instrument_code != 'GBP' && event.direction == 'DEBIT'

# Event type + status
event.dataset_code == 'HORSE_RACING' && event.status == 'OFFICIAL'
```

**Avoid overly broad filters:**

```cel
# Too broad — triggers on everything
event.direction == 'DEBIT'

# Missing chain termination — could loop
event.direction == 'DEBIT'  # without excluding output instrument
```

**Filter variables available:**

- `event.*` — Event payload fields (instrument_code, direction, amount, etc.)
- `tenant.*` — Tenant context (tenant_id, configuration)

---

## Account Model Design

### Principle: Accounts Are Views

Each account represents one view of a position. Different views use different
instruments and different account types. A single business entity (customer,
holding, contract) may have multiple accounts.

### When to Use Separate Accounts

| Scenario | Account structure |
|----------|-------------------|
| Track units AND value | Custody account (units) + Value account (currency) |
| Multiple valuations | One source account + N target accounts (one per method) |
| Tax vs market | Cost basis account (GBP) + Market value (computed) |
| Receivable vs settled | Accrued account + Settled account |

### Account Type Metadata

Account types carry metadata that sagas use for business logic:

```yaml
account_types:
  - code: energy_metered
    default_conversion_method_id: kwh_to_gbp_retail
    valuation_methods:
      - kwh_to_gbp_retail
      - kwh_to_gbp_wholesale
```

Sagas look up account type metadata with `reference_data.get_account_type()`
and use it to determine which valuation methods to apply. This makes sagas
data-driven rather than hard-coded.

---

## Starlark Conventions for Tenant Sagas

These conventions supplement the [Starlark Style Guide](starlark-style-guide.md)
with patterns specific to event-triggered tenant sagas.

### Handler Results Are Structs

Service module calls return `starlarkstruct.Struct` values with dot-access
fields — not dicts.

```python
# Access fields with dot notation
result = position_keeping.get_balance(account_id=acc_id)
amount = result.amount           # dot access
instrument = result.instrument_code

# NOT dict access
amount = result["amount"]        # wrong — this fails
```

### Iterating Handler Results

Handlers that return collections expose them through `.items` and `.count`
fields.

```python
result = position_keeping.query_accounts(instrument_code="kWh")
count = result.count        # number of results
items = result.items        # iterable collection

for account in result.items:
    balance = position_keeping.get_balance(account_id=account.account_id)
```

### Dynamic Step Names in Loops

When iterating over a collection, create unique step names using an index:

```python
adjustment_count = 0
for holding in holdings.items:
    step(name="book_adjustment_" + str(adjustment_count))
    position_keeping.initiate_log(
        account_id=holding.cost_basis_account_id,
        # ...
    )
    adjustment_count = adjustment_count + 1
```

**Why not `+=`?** Starlark supports `+=` for integers. Both forms work, but
explicit `= ... + 1` is clearer in saga context where every line matters for
compensation tracking.

### Valuation Method Derivation

Never hard-code valuation method IDs. Look them up from account type metadata:

```python
# Look up account type for its valuation methods
step(name="lookup_account_type")
account_type = reference_data.get_account_type(
    code=ctx["account_type_code"],
)

# Use the method from metadata
step(name="compute_value")
value = valuation_engine.compute(
    method_id=account_type.default_conversion_method_id,
    amount=amount,
    from_instrument="kWh",
    to_instrument="GBP",
)
```

### Idempotency Check First

Always check for existing work before executing saga logic. This prevents
duplicate processing on event replay.

```python
def execute_saga():
    correlation_id = input_data["correlation_id"]

    # Idempotency check — always first
    step(name="check_idempotency")
    existing = position_keeping.query_logs(
        correlation_id=correlation_id,
        instrument_code="GBP",
        account_id=target_account_id,
    )

    if existing.count > 0:
        return {"status": "ALREADY_PROCESSED", "correlation_id": correlation_id}

    # ... proceed with saga logic
```

---

## Validation and Testing

### Syntax Validation

Example saga scripts are validated by Go tests using Starlark's parser with
the same options as the saga runtime:

```go
var starlarkFileOptions = &syntax.FileOptions{
    Set:            true,
    While:          false,    // no while loops
    GlobalReassign: true,
    Recursion:      false,    // no recursion
}

_, err = starlarkFileOptions.Parse(filename, script, 0)
```

This validates syntax without executing the script. For full execution
validation (including handler call correctness), use the
[DryRunValidator](saga-validation.md) with populated `input_data`.

### DryRunValidator Limitations

The `DryRunValidator.Validate()` method executes scripts with an **empty
`input_data` dict**. Scripts that access `input_data["key"]` will fail with
`key "key" not in dict`.

**Workaround for PRD examples:** Use syntax-only validation via
`syntax.Parse`. The syntax test validates that scripts are valid Starlark; the
handler YAML in the test file documents the intended handler interfaces.

**For production sagas:** Use `NewStarlarkSagaRunner` directly with populated
`RunnerInput.Input` map to test full execution paths. See the
[Starlark Style Guide testing section](starlark-style-guide.md#testing-considerations).

### Test Coverage Guard

Ensure all `.star` files in an examples directory are covered by tests:

```go
func TestAllStarFilesHaveTests(t *testing.T) {
    entries, _ := os.ReadDir(dir)
    for _, entry := range entries {
        if filepath.Ext(entry.Name()) == ".star" {
            assert.True(t, testedFiles[entry.Name()],
                "%s exists but is not covered by tests", entry.Name())
        }
    }
}
```

---

## Reference Examples

The [`docs/prd/032-examples/`](../prd/032-examples/) directory contains four
industry-spanning examples demonstrating these patterns:

| Example | Industry | Pattern | Trigger |
|---------|----------|---------|---------|
| `usage_to_value.star` | Energy | Cross-instrument valuation (2 legs) | `event:position-keeping.transaction-captured.v1` |
| `compute_billing.star` | Cloud | Single-leg billing | `event:position-keeping.transaction-captured.v1` |
| `race_result_distribution.star` | Betting | Entity graph distribution | `event:market-information.observation-recorded.v1` |
| `corporate_action_cost_adjustment.star` | Wealth | Phantom events / cost basis | `event:market-information.corporate-action.v1` |

Each example includes a file header documenting its trigger, filter, input
data, and account model. See the
[examples README](../prd/032-examples/README.md) for details.

---

## Designing a New Tenant Economy

When guiding a tenant through economy design, follow this sequence:

1. **Identify instruments.** What units of value does the business track?
   Currency, energy, compute, carbon, loyalty points?

2. **Map account structures.** For each business entity, what views are needed?
   What instruments does each account hold?

3. **Define valuation rules.** Which instruments need conversion? What methods
   (market rate, fixed rate, tiered pricing)?

4. **Choose triggers.** What events should fire sagas? Position changes, market
   data, external webhooks, scheduled jobs?

5. **Write CEL filters.** What conditions narrow each trigger to the right
   events? How is chain termination achieved?

6. **Design saga logic.** What steps does each saga execute? What's the
   idempotency strategy? What's the compensation strategy?

7. **Validate.** Run syntax validation on all `.star` files. Test with
   populated input data. Review complexity metrics.

This sequence maps directly to the manifest structure and can be driven by
conversational AI that asks the right questions at each step.
