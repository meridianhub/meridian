# Authoring Patterns

This guide describes how to create and publish Meridian Cookbook business patterns.

A pattern is a reusable bundle of Meridian manifest fragments (instruments, account types, sagas, valuation rules)
that solves a common business problem. Patterns are described by `registry:pattern` entries conforming to the
`registry-item.json` schema.

---

## Pattern Structure

Every pattern lives in its own directory under `cookbook/patterns/<pattern-name>/` and contains:

```text
cookbook/patterns/<pattern-name>/
├── pattern.json              # Registry metadata (required)
├── manifest-fragment.yaml    # Manifest primitives: instruments, account types, etc. (required)
└── <saga-name>.star          # Starlark saga scripts (one per saga declared in manifest)
```

**`pattern.json`** — the registry entry. Describes what the pattern provides, requires, and composes with.
Must conform to `cookbook/schema/registry-item.json`.

**`manifest-fragment.yaml`** — the deployable configuration. Contains the Meridian manifest primitives
(instruments, accountTypes, valuationRules, sagas) that a tenant applies when adopting this pattern.

**`.star` files** — Starlark saga scripts. Required when the pattern declares sagas. Each saga in
`manifest-fragment.yaml` needs a corresponding `.star` file, and all `.star` files must be referenced
in `pattern.json`'s `files[]` array.

---

## The `pattern.json` Schema

All fields come from `cookbook/schema/registry-item.json`. For `registry:pattern` entries,
`files[].path` values are relative to the **cookbook root** (`cookbook/`), for example `patterns/my-pattern/...`.

```json
{
    "$schema": "https://cookbook.meridianhub.org/schema/registry-item.json",
    "name": "my-pattern",
    "type": "registry:pattern",
    "title": "Human-Readable Title",
    "description": "One sentence: what this pattern does and why you'd use it.",
    "registryDependencies": ["base-fiat-gbp"],
    "categories": ["economy", "billing"],
    "meta": {
        "complexity": 3,
        "design_pattern": "cross-instrument-valuation",
        "industries": ["energy", "utilities"],
        "provides": {
            "instruments": ["KWH"],
            "account_types": ["ENERGY_INVENTORY", "SETTLEMENT"],
            "sagas": ["usage_to_value"],
            "valuation_rules": ["kwh_to_gbp_retail"],
            "triggers": ["event:position-keeping.transaction-captured.v1"]
        },
        "requires": {
            "instruments": ["GBP"],
            "market_data": ["KWH_GBP_RETAIL"]
        },
        "composes_with": ["carbon-offset"],
        "conflicts_with": [],
        "extends": []
    },
    "files": [
        {
            "path": "patterns/my-pattern/manifest-fragment.yaml",
            "type": "registry:file",
            "target": "~/manifest-fragment.yaml"
        },
        {
            "path": "patterns/my-pattern/my_saga.star",
            "type": "registry:file",
            "target": "~/sagas/my_saga.star"
        }
    ]
}
```

### Field Reference

| Field | Type | Required | Notes |
|-------|------|----------|-------|
| `name` | string | yes | Kebab-case, matches directory name. Pattern: `^[a-z][a-z0-9-]*$` |
| `type` | string | yes | Always `"registry:pattern"` |
| `title` | string | yes | Human-readable display name |
| `description` | string | yes | One or two sentences, not a bullet list |
| `registryDependencies` | array | yes | Names of patterns this one depends on. Use `[]` for foundation patterns |
| `categories` | array | no | Taxonomy tags for discovery (e.g., `"economy"`, `"billing"`, `"compliance"`) |
| `meta.complexity` | integer | yes | Fibonacci: 1, 2, 3, 5, or 8 |
| `meta.design_pattern` | string\|null | yes | Design pattern name, or `null` for foundation patterns |
| `meta.industries` | array | yes | Industry sectors: `"all"` for universal, or specific sectors |
| `meta.provides` | object | yes | What this pattern contributes to the manifest |
| `meta.requires` | object | yes | External dependencies needed before this pattern works |
| `meta.composes_with` | array | yes | Patterns that pair well with this one |
| `meta.conflicts_with` | array | yes | Patterns that cannot be combined with this one |
| `meta.extends` | array | yes | Patterns this one builds on top of |
| `files` | array | yes | All files this pattern installs |

### Provides vs Requires vs RegistryDependencies

These three fields express different kinds of dependency:

- **`registryDependencies`**: Other registry patterns that must be installed first. The current pattern's
  manifest fragment may reference instruments or account types from these patterns.
  Example: `energy-settlement` depends on `base-fiat-gbp` because its account types allow GBP.

- **`meta.requires.instruments`**: Instruments that must already exist in the tenant's manifest but are
  not provided by a listed registry dependency. Use this when the instrument comes from a pattern
  outside the registry (e.g., a tenant-specific currency).

- **`meta.requires.market_data`**: Market data dataset codes that the pattern's valuation rules reference.
  Example: `energy-settlement` requires `KWH_GBP_RETAIL` because its SPOT_RATE valuation rule reads it.

- **`meta.provides`**: What this pattern contributes. The contents of `provides` should match exactly
  what is in `manifest-fragment.yaml`. Tests enforce this: `provides.instruments` must match the
  `instruments` list in the manifest fragment.

---

## Complexity Scoring

The `meta.complexity` field uses the Fibonacci scale (1, 2, 3, 5, 8):

| Score | What it means | Example |
|-------|--------------|---------|
| 1 | Single instrument definition, no sagas, no account types | `base-fiat-gbp`, `base-fiat-usd` |
| 2 | One account type and/or one simple saga with a single step | A simple webhook receiver |
| 3 | Multiple account types, one or two sagas, standard trigger pattern | `energy-settlement`, `kyc-compliance` |
| 5 | Multiple sagas, cross-instrument valuation, multiple triggers | `saas-billing`, `carbon-offset` |
| 8 | Gateway integration, multi-step saga with compensation, external provider | `payment-gateway-stripe` |

Score based on uncertainty and scope, not line count. A 50-line saga with a well-understood pattern
is complexity 3. An 8-line saga that integrates with an external provider is complexity 5.

---

## Design Pattern References

The `meta.design_pattern` field names the design pattern this entry exemplifies. Valid names come from
[`docs/guides/manifest-design-patterns.md`](../../docs/guides/manifest-design-patterns.md):

| Value | Pattern | When to use |
|-------|---------|-------------|
| `null` | No pattern (foundation) | Base currency instruments |
| `cross-instrument-valuation` | Pattern 1 | kWh → GBP, GPU_HOUR → USD (multi-leg) |
| `compute-metering` | Pattern 2 | Usage billing with single target instrument |
| `credit-retirement-lifecycle` | Pattern 3 | Carbon credits, irreversible retirement |
| `financial-gateway` | Custom | Stripe payment collection and payout via Financial Gateway |
| `operational-gateway` | Custom | Generic external REST/gRPC calls, non-payment webhooks |
| `compliance-marker` | Custom | Zero-amount positions as compliance audit trail |

Set to `null` for foundation patterns (`base-fiat-*`). Set to the closest named pattern otherwise.
If your pattern doesn't fit an existing name, use a descriptive kebab-case string.

---

## CEL Filter Chain Termination

When a saga uses the `event:` trigger type, it reacts to platform events. If the saga produces new
positions (e.g., booking a GBP charge after metering kWh), those positions also emit events. Without
a termination condition, the saga re-triggers on its own output.

**Rule:** Every `event:`-triggered saga that creates positions must have a CEL filter that excludes
the instruments it writes.

**Example from `energy-settlement`:**

The `usage_to_value` saga triggers on `event:position-keeping.transaction-captured.v1` and books GBP
positions. Its filter is:

```cel
event.instrument_code != 'GBP' && event.direction == 'DEBIT'
```

The `!= 'GBP'` clause ensures the saga ignores the GBP positions it creates. Without this, every
GBP booking would re-trigger the saga.

**Example from `saas-billing`:**

The `compute_billing` saga triggers on GPU_HOUR DEBITs and creates USD charges:

```cel
event.instrument_code == 'GPU_HOUR' && event.direction == 'DEBIT'
```

An `==` filter on the source instrument implicitly terminates the chain: the saga only fires on
GPU_HOUR, and it creates USD — so its output never matches the filter.

Both approaches are valid. Use `==` (allowlist) when your pattern handles exactly one source instrument.
Use `!=` (denylist) when the pattern should handle any non-settlement instrument.

---

## Gateway Selection

Meridian provides two gateways for external integrations. Choosing the wrong one is the most common
mistake when authoring payment-adjacent patterns.

### Financial Gateway

Use `financial_gateway` in Starlark sagas when the integration involves **payment collection or
payout** with a supported payment provider (Stripe, Stripe Connect).

The Financial Gateway provides:

- `financial_gateway.dispatch_payment()` — charge a customer's payment method
- `financial_gateway.dispatch_refund()` — issue a refund or payout to a connected account
- `financial_gateway.cancel_payment()` — cancel an in-flight payment (compensation step)
- Built-in idempotency, retry logic, and payment lifecycle management
- Automatic routing via the `paymentRails` configuration in `manifest-fragment.yaml`

Configure the provider in `manifest-fragment.yaml` under `paymentRails`:

```yaml
paymentRails:
  - provider: stripe
    mode: CONNECT_MODE_STANDARD
    accountId: "acct_REPLACE_WITH_REAL_ID"
    webhookEndpointSecret: "sm://stripe/webhook_secret"
    payoutSchedule: PAYOUT_SCHEDULE_DAILY
    supportedMethods: [card]
```

**When to use the Financial Gateway:**

- Charging a customer's card for a service
- Issuing a payout or refund to a connected account
- Any saga where money moves through Stripe

**Example:** `payment-gateway-stripe` uses `financial_gateway.dispatch_payment()` and
`financial_gateway.cancel_payment()` for Stripe payment collection with full compensation support.

### Operational Gateway

Use the Operational Gateway when a saga needs to make an **outbound call to a generic external
service** — REST endpoints, gRPC services, or MQTT/AMQP brokers. The Operational Gateway is for
outbound dispatch only; inbound data from external systems arrives via the `webhook:` trigger type
on sagas (no gateway involved).

The Operational Gateway is configured via `operationalGateway.providerConnections` in the manifest
and dispatched through the gateway instruction API. It explicitly rejects `payment.*` instruction
types.

**When to use the Operational Gateway:**

- Calling an external REST or gRPC API from a saga
- Dispatching outbound messages to MQTT/AMQP brokers

**For inbound webhooks** (e.g., IoT meter events, market data feeds): use the `webhook:` saga
trigger type directly. Meridian delivers inbound webhook payloads to the matching saga trigger
without involving the Operational Gateway. The `saas-billing` pattern is an example: its
`record_gpu_usage` saga uses `webhook:gpu_meter_event` to receive meter events and records usage
positions via `position_keeping.initiate_log`, with no outbound dispatch at all.

### Decision Tree

```text
Does the saga need to move money through Stripe?
├── Yes → financial_gateway.dispatch_payment() or dispatch_refund()
│         Configure paymentRails in manifest-fragment.yaml
│         design_pattern: "financial-gateway"
└── No → Is the saga receiving data from an external system?
         ├── Yes, inbound webhook → webhook: trigger on the saga
         │   Meridian routes the webhook payload directly to the saga.
         │   No gateway dispatch. Saga records positions via position_keeping.
         └── No → Does the saga need to call an external system outbound?
                  ├── Yes → operationalGateway.providerConnections in the manifest
                  │         design_pattern: "operational-gateway"
                  └── No → No gateway needed
```

### Common Mistake

**Do not use the Operational Gateway for Stripe payments.** The Operational Gateway does not have
Stripe credentials, does not manage payment state, and rejects `payment.*` instruction types at
runtime. Attempting to dispatch a Stripe payment through it will fail.

The `webhook:` trigger type is used for inbound webhook delivery (e.g., `stripe.payment_intent.succeeded`).
Meridian routes inbound Stripe webhooks to saga triggers based on the `webhookEndpointSecret` in
`paymentRails`. This is separate from the Financial Gateway, which handles outbound payment dispatch.
A pattern can use both: `stripe_payment_via_gateway.star` dispatches outbound via Financial Gateway,
while `stripe_payment_received.star` handles the inbound Stripe webhook confirmation.

---

## Step-by-Step: Creating a New Pattern

### 1. Create the directory

```bash
mkdir -p cookbook/patterns/my-pattern
```

### 2. Write `manifest-fragment.yaml`

Declare the manifest primitives this pattern adds to a tenant economy:

```yaml
instruments:
  - code: MY_ASSET
    name: My Asset
    type: INSTRUMENT_TYPE_COMMODITY
    dimensions:
      unit: units
      precision: 3

accountTypes:
  - code: MY_ACCOUNT
    name: My Account
    normalBalance: NORMAL_BALANCE_DEBIT
    allowedInstruments: [MY_ASSET]
    policies:
      validation: "amount > 0"
      bucketing: ""
```

Refer to existing fragments for valid `type` values (`INSTRUMENT_TYPE_FIAT`, `INSTRUMENT_TYPE_COMMODITY`,
`INSTRUMENT_TYPE_VOUCHER`) and `normalBalance` values (`NORMAL_BALANCE_DEBIT`, `NORMAL_BALANCE_CREDIT`).

### 3. Write any `.star` saga files

If the pattern declares sagas, write a `.star` file for each. See the
[Starlark Style Guide](../../docs/guides/starlark-style-guide.md) for syntax conventions.

Minimum structure:

```python
# Saga: my_saga
# Trigger: event:position-keeping.transaction-captured.v1
# Filter:  event.instrument_code == 'MY_ASSET' && event.direction == 'DEBIT'

my_saga_saga = saga(name="my_saga")

def execute_my_saga():
    ctx = input_data
    correlation_id = ctx["correlation_id"]

    # Idempotency check first
    step(name="check_idempotency")
    existing = position_keeping.query_logs(correlation_id=correlation_id)
    if existing.count > 0:
        return {"status": "ALREADY_PROCESSED", "correlation_id": correlation_id}

    # ... saga logic

    return {"status": "PROCESSED", "correlation_id": correlation_id}

output = execute_my_saga()
```

### 4. Write `pattern.json`

```json
{
    "$schema": "https://cookbook.meridianhub.org/schema/registry-item.json",
    "name": "my-pattern",
    "type": "registry:pattern",
    "title": "My Pattern",
    "description": "One sentence description.",
    "registryDependencies": ["base-fiat-gbp"],
    "categories": ["economy"],
    "meta": {
        "complexity": 3,
        "design_pattern": "cross-instrument-valuation",
        "industries": ["your-sector"],
        "provides": {
            "instruments": ["MY_ASSET"],
            "account_types": ["MY_ACCOUNT"],
            "sagas": ["my_saga"],
            "valuation_rules": [],
            "triggers": ["event:position-keeping.transaction-captured.v1"]
        },
        "requires": {
            "instruments": ["GBP"],
            "market_data": []
        },
        "composes_with": [],
        "conflicts_with": [],
        "extends": []
    },
    "files": [
        {
            "path": "patterns/my-pattern/manifest-fragment.yaml",
            "type": "registry:file",
            "target": "~/manifest-fragment.yaml"
        },
        {
            "path": "patterns/my-pattern/my_saga.star",
            "type": "registry:file",
            "target": "~/sagas/my_saga.star"
        }
    ]
}
```

### 5. Register in `registry.json`

Add an entry to `cookbook/registry.json`:

```json
{
    "name": "my-pattern",
    "type": "registry:pattern"
}
```

### 6. Run the tests

```bash
cd cookbook && go test ./...
```

Tests enforce: schema validation, `provides.instruments` matches manifest, `registryDependencies` exist
in registry, all `files[]` paths exist on disk, no `.star` syntax errors, and no orphan files.

---

## Example: Simple Pattern (Energy Settlement)

`energy-settlement` converts metered kWh into GBP using market rates. It requires `base-fiat-gbp`
because its account types hold GBP, which must already exist.

**`pattern.json` key decisions:**

- `complexity: 3` — two account types, one saga, one valuation rule pair, standard trigger
- `design_pattern: "cross-instrument-valuation"` — two settlement legs (retail + wholesale)
- `registryDependencies: ["base-fiat-gbp"]` — uses GBP instrument from that pattern
- `requires.market_data: ["KWH_GBP_RETAIL", "KWH_GBP_WHOLESALE"]` — SPOT_RATE valuation needs live rates
- `composes_with: ["carbon-offset", "time-of-use-pricing"]` — these patterns add value alongside this one
- `conflicts_with: ["flat-rate-billing"]` — flat-rate billing contradicts usage-based settlement

**`manifest-fragment.yaml` structure:** Declares KWH instrument, three account types (ENERGY_INVENTORY,
SETTLEMENT, REVENUE), and two valuation rules for retail and wholesale conversion.

**`usage_to_value.star`:** Triggered by `event:position-keeping.transaction-captured.v1`. The CEL
filter `event.instrument_code != 'GBP' && event.direction == 'DEBIT'` prevents the GBP positions
the saga creates from re-triggering it. The saga performs dual idempotency checks (one per leg)
before calling `valuation_engine.compute()` for each rate, then books both GBP positions.

---

## Example: Complex Pattern (SaaS Billing)

`saas-billing` meters GPU hours, API calls, and storage, then converts each to USD charges. It has
three sagas for different trigger points (webhook, event, scheduled).

**`pattern.json` key decisions:**

- `complexity: 5` — four instruments, four account types, three sagas, four valuation rules, three triggers
- `registryDependencies: ["base-fiat-usd"]` — all billing targets USD
- `extends: ["base-fiat-usd"]` — declares it builds on the USD foundation pattern
- `provides.triggers` lists all three trigger types: `webhook:`, `event:`, and `scheduled:`

**Three sagas, three trigger types:**

| Saga | Trigger | What it does |
|------|---------|-------------|
| `record_gpu_usage` | `webhook:gpu_meter_event` | Captures raw GPU_HOUR usage from meter events |
| `compute_billing` | `event:position-keeping.transaction-captured.v1` with GPU_HOUR filter | Converts GPU_HOUR to USD charge |
| `generate_monthly_invoice` | `scheduled:monthly_billing` | Aggregates month's usage into invoice |

The `compute_billing` saga's filter `event.instrument_code == 'GPU_HOUR' && event.direction == 'DEBIT'`
is specific to one instrument — it implicitly terminates any chain because the USD positions it creates
do not match the GPU_HOUR filter.

---

## Testing Requirements

Every pattern needs to pass the suite in `cookbook/validation_test.go` and `cookbook/patterns/patterns_test.go`.

**What the tests check:**

1. **Schema validation** — `pattern.json` conforms to `registry-item.json` (all required fields,
   `complexity` is a valid Fibonacci value, `name` matches the kebab-case pattern).

2. **Manifest consistency** — `meta.provides.instruments` lists exactly the instruments defined in
   `manifest-fragment.yaml`. If the manifest declares KWH, `provides.instruments` must include `"KWH"`.

3. **Dependency existence** — every name in `registryDependencies` and `meta.composes_with` must exist
   in `registry.json`. Add your pattern to the registry before adding cross-references to it.

4. **File existence** — every path in `files[]` must exist on disk relative to the cookbook root (`cookbook/`).

5. **Starlark syntax** — all `.star` files parse without errors using the saga runtime's file options
   (no `while` loops, no recursion).

6. **No orphan files** — every `.json`, `.yaml`, and `.star` file in `cookbook/patterns/` (except
   `pattern.json` and `manifest-fragment.yaml`) must be listed in a `files[]` array.

Run with: `cd cookbook && go test ./...`
