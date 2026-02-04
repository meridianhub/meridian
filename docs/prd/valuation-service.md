---
name: prd-account-scoped-valuation
description: Account-Scoped Valuation Engine - Embedded library pattern for multi-asset value transformation
triggers:
  - Converting non-monetary assets to monetary value for settlement
  - Tenant-specific pricing strategies without code deployment
  - Bi-temporal valuation replay for audit and reconciliation
  - Multi-currency and multi-commodity conversion with full audit trail
instructions: |
  Account Services (CurrentAccount, InternalBankAccount) own valuation capability via shared library.
  Each account defines how it accepts value through strategy assignments stored in its schema.
  The shared/pkg/valuation library executes CEL/Starlark strategies loaded from Reference Data.

  CRITICAL: Valuation happens ATOMICALLY within transactional operations (InitiateLien, ExecuteDeposit).
  Use InitiateLien for withdrawals (creates price lock). Use ExecuteDeposit for deposits (atomic valuation).
  Only use GetValuedAmount for non-binding inquiries (mobile apps, dashboards).

  Price Lock: Liens store valued_amount at creation time. ExecuteLien uses locked value, not recalculated.
  Concurrency Safety: Valuation queries projected balance (current + active liens) to prevent tiered pricing drift.
---

# PRD: Account-Scoped Valuation Engine

**Status:** Draft - Revised Architecture
**Version:** 2.6 (Ghost Pricing Prevention & Lien Immutability)
**Task Master Tag:** `valuation-engine`
**Story Points:** 75 (11 streams)
**Core ADR:** [ADR-0028: Starlark Saga Orchestration with CEL Valuation](../adr/0028-starlark-saga-cel-valuation.md)

**Version History:**

- v2.0: Embedded library architecture (vs standalone service)
- v2.1: Gemini safety enhancements (passport pattern, event-driven cache)
- v2.2: Atomic valuation with price lock (TOCTOU elimination)
- v2.3: Security & resilience (read-only VM, graceful stale, bucket filtering, quality tracking)
- v2.4: Reservation Ledger pattern, calculation path audit, canonical bucketing library
- v2.5: Idempotency guards, basis drift detection, architecture enforcement, size limits
- v2.6: Ghost Pricing prevention (shared valuation logic), lien immutability constraint

## 1. Executive Summary

The Account-Scoped Valuation Engine enables multi-asset ledgers by making **accounts responsible for defining
how they accept value**. Instead of a centralized pricing service, valuation logic is embedded within
Account Services (CurrentAccount, InternalBankAccount) via a shared library.

### The "Probe Pattern"

```text
Saga asks Account: "What is 100 kWh worth to you?"
Account responds: "£35.00, and here's why (ValuationBasis)"
```

**Key Innovation:** This PRD codifies a shift from "Price is a number" to
**"Value is a Function of an Account."** We move the **responsibility of value** to the **Account**,
while a **shared valuation library** provides the **computational integrity**.

### Architecture at a Glance

```text
shared/pkg/valuation/          # Shared library (CEL + Starlark runtime)
├── engine.go                  # Core valuation execution
├── builtins.go               # market_data, cel_eval functions
└── cache.go                  # L1 in-memory cache

services/current-account/      # Implements GetValuedAmount RPC
services/internal-bank-account/ # Implements GetValuedAmount RPC
```

**Why Embedded Library > Standalone Service:**

- **Performance**: Eliminates 1 network hop (3 hops vs 4)
- **Domain Modeling**: Valuation is Account's capability, not external service
- **Operational Simplicity**: No additional microservice to deploy/monitor
- **Follows Existing Patterns**: Matches shared/pkg/saga library approach

### Atomic Valuation with Price Lock (v2.2 Enhancement)

**Critical Innovation:** Valuation happens **atomically within `InitiateLien` and `ExecuteDeposit`** operations,
not as a separate inquiry step.

**Why This Matters:**

State-dependent pricing (tiered rates, volume discounts) is vulnerable to the "Tiered Valuation Drift" race
condition:

```text
BAD: Two sagas query valuation separately → both see same balance → both get cheap rate
GOOD: Each lien queries projected balance → second sees first's reservation → correct tier
```

**The Price Lock Guarantee:**

When `InitiateLien` creates a reservation, it stores BOTH:

- `reserved_quantity` (100 kWh)
- `valued_amount` (£35.00 at T0)
- `valuation_basis` (audit trail)

Later, when `ExecuteLien` is called, the system uses the **locked value**, not a recalculated value. This
protects both customer (price won't increase) and merchant (discounts can't be gamed).

**Two-Mode Operation:**

1. **Transactional** (`InitiateLien`, `ExecuteDeposit`): Atomic valuation with price lock
2. **Inquiry** (`GetValuedAmount`): Non-binding estimate for UX (mobile apps, dashboards)

**Complexity Impact:** +17 points (46 → 63 points) buys elimination of TOCTOU race conditions and guaranteed
price integrity under concurrent load.

## 2. The Problem Statement

In a multi-asset ledger, the "Conversion Rate" is not a global constant.

| Scenario | Input | Destination Account | Valuation Logic |
|----------|-------|---------------------|-----------------|
| **Retail Energy** | 100 kWh | Consumer Current Account | Flat rate £0.35/kWh |
| **Wholesale Energy** | 100 kWh | DNO Internal Account | Spot Price (EPEX) * GSP |
| **Loyalty Reward** | 100 kWh | Marketing Expense Account | 1 Point per 10 kWh |
| **Foreign Exchange** | $100 USD | GBP Current Account | Market Mid-Rate + 2% Spread |

### Current Gaps

1. **Logic Hardcoding:** Changing a tariff requires code deployment.
2. **Context Loss:** We can't easily track *why* a specific rate was applied to a specific meter read.
3. **Account Heterogeneity:** Different accounts need different formulas (fixed vs. spot pricing).
4. **Audit Trail:** No clear provenance for how values were computed historically.

## 3. The "Account-as-Authority" Solution

We implement an **Account Responsibility Pattern**:

1. **Shared Library**: `shared/pkg/valuation` provides CEL/Starlark execution engine
2. **Account Ownership**: Account Services implement `GetValuedAmount` RPC
3. **Strategy Assignment**: Accounts store `valuation_strategy_id` + parameters in their schema
4. **In-Process Execution**: Valuation happens within Account Service process boundary

### 3.1 Data Model: The Strategy Assignment

Accounts store a reference to their valuation strategy:

```sql
-- Added to CurrentAccount and InternalBankAccount schemas
CREATE TABLE valuation_assignments (
    account_id UUID NOT NULL,
    instrument_code VARCHAR(32) NOT NULL, -- e.g., 'KWH', 'USD', 'TONNE_CO2E'

    -- Reference to the strategy in Reference Data service
    strategy_id UUID NOT NULL,

    -- Account-specific context parameters
    -- e.g., {"gsp": "P", "tier": "Gold", "markup": "0.02"}
    parameters JSONB NOT NULL DEFAULT '{}',

    -- Lifecycle
    active BOOLEAN NOT NULL DEFAULT true,

    -- Bi-temporal tracking
    valid_from TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    valid_to TIMESTAMPTZ,

    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),

    PRIMARY KEY (account_id, instrument_code),
    FOREIGN KEY (account_id) REFERENCES accounts(id) ON DELETE CASCADE
);

CREATE INDEX idx_valuation_assignments_strategy
    ON valuation_assignments(strategy_id)
    WHERE active = true;

CREATE INDEX idx_valuation_assignments_bitemporal
    ON valuation_assignments(account_id, valid_from, valid_to);
```

### 3.2 Valuation Strategy Definition

Strategies are stored in the Reference Data service (per-tenant schema):

```sql
-- Lives in Reference Data service (tenant-scoped via PostgreSQL schemas)
CREATE TABLE valuation_strategies (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),

    -- Identification
    name VARCHAR(64) NOT NULL,           -- "retail_energy_v1", "fx_gbp_usd"
    version INTEGER NOT NULL DEFAULT 1,

    -- Input/Output dimensions
    input_instrument VARCHAR(32) NOT NULL,  -- "KWH"
    output_instrument VARCHAR(32) NOT NULL, -- "GBP"

    -- Logic (Starlark script or CEL expression)
    logic_type VARCHAR(16) NOT NULL,     -- "STARLARK", "CEL"
    logic_script TEXT NOT NULL,
    logic_hash VARCHAR(64) NOT NULL,     -- SHA-256 for cache invalidation

    -- Lifecycle
    status VARCHAR(16) NOT NULL DEFAULT 'DRAFT',  -- DRAFT, ACTIVE, DEPRECATED

    -- Metadata
    description TEXT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    activated_at TIMESTAMPTZ,
    deprecated_at TIMESTAMPTZ,

    -- Bi-temporal for replay
    valid_from TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    valid_to TIMESTAMPTZ,

    UNIQUE(name, version),
    CHECK (status IN ('DRAFT', 'ACTIVE', 'DEPRECATED')),
    CHECK (logic_type IN ('STARLARK', 'CEL')),
    CHECK (logic_script <> '')
);

CREATE INDEX idx_valuation_strategies_lookup
    ON valuation_strategies(input_instrument, output_instrument, status);

CREATE INDEX idx_valuation_strategies_bitemporal
    ON valuation_strategies(name, valid_from, valid_to);
```

#### Platform Default Inheritance (The "SYSTEM Strategy" Pattern)

**Requirement:** If a tenant does not define a valuation strategy for a given instrument, they SHOULD
automatically inherit the `SYSTEM` version of the strategy. This ensures "Motive" or "UN WFP" scenarios
work out-of-the-box without requiring each tenant to manually configure energy strategies.

**Implementation:**

The `ResolveValuationStrategy` function follows a lookup hierarchy:

```go
func (s *Service) ResolveValuationStrategy(
    ctx context.Context,
    tenantID string,
    inputInstrument string,
    outputInstrument string,
) (*Strategy, error) {
    // 1. Check tenant-specific strategy (tenant schema)
    strategy, err := s.repo.FindStrategy(ctx, tenantID, inputInstrument, outputInstrument)
    if err == nil && strategy != nil {
        return strategy, nil
    }

    // 2. Fall back to SYSTEM strategy (platform default)
    systemStrategy, err := s.repo.FindStrategy(ctx, "SYSTEM", inputInstrument, outputInstrument)
    if err == nil && systemStrategy != nil {
        s.logger.Info("using SYSTEM default strategy",
            "tenant_id", tenantID,
            "input", inputInstrument,
            "output", outputInstrument,
            "strategy_id", systemStrategy.ID,
        )
        return systemStrategy, nil
    }

    // 3. No strategy found
    return nil, fmt.Errorf("no valuation strategy for %s → %s", inputInstrument, outputInstrument)
}
```

**Seeded SYSTEM Strategies:**

| Strategy Name | Input → Output | Description |
|---------------|----------------|-------------|
| `SYSTEM_IDENTITY_USD` | USD → USD | 1:1 identity |
| `SYSTEM_IDENTITY_GBP` | GBP → GBP | 1:1 identity |
| `SYSTEM_IDENTITY_EUR` | EUR → EUR | 1:1 identity |
| `SYSTEM_RETAIL_ENERGY` | KWH → GBP | Default retail tariff (£0.35/kWh) |
| `SYSTEM_CARBON_CREDIT` | TONNE_CO2E → GBP | Default carbon price (£75/tonne) |

**Why This Matters:**

- **Motive scenario:** A mobility-as-a-service provider can onboard without configuring energy valuation
- **UN WFP scenario:** Humanitarian vouchers work immediately using SYSTEM strategies
- **Developer experience:** New tenants can start testing without manual strategy configuration

## 4. Functional Requirements

### FR-1: GetValuedAmount RPC (Inquiry-Only, Non-Binding)

**Requirement:** Account Services MUST implement the `GetValuedAmount` RPC as a **read-only inquiry** for
non-transactional valuation queries.

**Semantics:** This RPC is **NON-BINDING**. It does not create liens, does not reserve capacity, and does not
guarantee the returned price will be honored in subsequent transactions. It is intended for:

- Mobile app UX ("What would 100 kWh cost right now?")
- Dashboard displays ("Current rate for my account")
- Saga planning (estimate before reservation)

**For transactional flows** (actual withdrawals/deposits), valuation MUST happen atomically within
`InitiateLien` or `ExecuteDeposit` (see FR-8).

#### Ghost Pricing Prevention (CRITICAL)

**Requirement:** `GetValuedAmount` (inquiry) and `InitiateLien` (transactional) MUST share the **exact same
private valuation function** within the Account Service.

**Risk:** If the two code paths diverge, the mobile app could show one price while the transaction executes
at another—destroying customer trust and potentially violating consumer protection regulations.

**Implementation:**

```go
// services/current-account/internal/service/valuation.go

// valuateInternal is the SINGLE SOURCE OF TRUTH for all valuation
// Both GetValuedAmount and InitiateLien MUST use this function
func (s *Service) valuateInternal(
    ctx context.Context,
    accountID uuid.UUID,
    input *quantity.InstrumentAmount,
    knowledgeAt time.Time,
    projectedBalance *decimal.Decimal,  // nil for inquiry, populated for transactional
) (*ValuationResult, error) {
    // ... shared valuation logic
}

// GetValuedAmount uses valuateInternal with projectedBalance=nil (inquiry mode)
func (s *Service) GetValuedAmount(ctx context.Context, req *GetValuedAmountRequest) (*GetValuedAmountResponse, error) {
    result, err := s.valuateInternal(ctx, req.AccountId, req.Input, req.KnowledgeAt, nil)
    // ...
}

// InitiateLien uses valuateInternal with actual projectedBalance (transactional mode)
func (s *Service) InitiateLien(ctx context.Context, req *InitiateLienRequest) (*InitiateLienResponse, error) {
    projectedBalance, _ := s.positionClient.GetProjectedBalance(ctx, ...)
    result, err := s.valuateInternal(ctx, req.AccountId, req.Amount, time.Now(), &projectedBalance)
    // ...
}
```

**Test Requirement:** Integration tests MUST verify that `GetValuedAmount` and `InitiateLien` return
identical valuations when given the same inputs and projected balance.

**Design Note (Naming):**

BIAN uses "Probe" terminology for non-binding inquiries. Alternative naming: `EvaluateAmount` emphasizes
computation/simulation semantics vs `Get...` which implies simple field retrieval. Current name
`GetValuedAmount` is acceptable but may be revisited in implementation for stronger BIAN alignment.

```protobuf
service CurrentAccountService {
  // Inquiry-only valuation (non-binding)
  rpc GetValuedAmount(GetValuedAmountRequest) returns (GetValuedAmountResponse);
}

message GetValuedAmountRequest {
  string account_id = 1;
  meridian.quantity.v1.InstrumentAmount input = 2;
  google.protobuf.Timestamp knowledge_at = 3;
}

message GetValuedAmountResponse {
  meridian.quantity.v1.InstrumentAmount output = 1;
  ValuationBasis basis = 2;
  string execution_time_ms = 3;
  bool cache_hit = 4;

  // WARNING: This value is informational only. Actual transaction may differ.
  bool is_estimate = 5;  // Always true for this RPC
}

message ValuationBasis {
  string strategy_id = 1;
  string strategy_version = 2;
  map<string, string> applied_rates = 3;
  repeated string observation_ids = 4;  // Links to MarketInformation
  google.protobuf.Timestamp computed_at = 5;
  google.protobuf.Timestamp knowledge_at = 6;
  google.protobuf.Struct account_parameters = 7;

  // Quality level of market data used (per ADR-018 Settlement & Reconciliation)
  repeated MarketDataQuality market_data_qualities = 8;

  // Calculation path breadcrumbs (for regulatory audit of complex strategies)
  repeated string calculation_path = 9;  // e.g., ["tier_2_applied", "markup_standard", "weekend_discount"]

  // Degraded mode flag (set if fallback rates used due to service unavailability)
  bool degraded_mode = 10;
}

message MarketDataQuality {
  string observation_id = 1;
  string source_trust_level = 2;  // "ESTIMATE", "COEFFICIENT", "ACTUAL", "REVISED"
  string instrument_code = 3;     // e.g., "EPEX_SPOT"
  string value = 4;               // The rate/price used
}
```

### FR-2: Shared Valuation Library

**Requirement:** A reusable Go library MUST provide CEL/Starlark execution for valuation logic.

**Package:** `shared/pkg/valuation`

**Core Interface:**

```go
package valuation

type Engine interface {
    // Valuate executes a strategy to convert input to output
    Valuate(ctx context.Context, req Request) (*Response, error)
}

type Request struct {
    Input       *quantity.InstrumentAmount
    StrategyID  uuid.UUID
    Parameters  map[string]interface{}
    KnowledgeAt time.Time
}

type Response struct {
    Output *quantity.InstrumentAmount
    Basis  *Basis
}

type Basis struct {
    StrategyID      uuid.UUID
    StrategyVersion string
    AppliedRates    map[string]decimal.Decimal
    ObservationIDs  []string
    ComputedAt      time.Time
    KnowledgeAt     time.Time
    Parameters      map[string]interface{}
}
```

**Performance Target:** < 5ms per valuation (in-process execution, excluding market data lookups).

### FR-3: Hierarchical Logic Execution

The engine executes logic in three tiers:

1. **Starlark (The Procedure):** Aggregates data, handles rounding logic and branching.
2. **CEL (The Policy):** Performs the high-speed numeric multiplication (~100ns).
3. **Market Data (The Fact):** Injects the bi-temporal rates (e.g., FX mid-rate).

**Example Execution Flow:**

```python
# Starlark strategy loaded from Reference Data
# SECURITY: Sandboxed execution - no filesystem, no network, no system calls
# LIMITS: 5s timeout, 64MB memory, CEL expressions limited to 10,000 cost units
def valuate_energy(input_quantity, params, knowledge_at):
    # 1. Validate required parameters
    if "gsp_coefficient" not in params:
        return {"error": "MISSING_PARAM", "message": "gsp_coefficient required"}

    gsp_coefficient = params["gsp_coefficient"]

    # 2. Fetch market data with error handling
    spot_result = market_data.get_price("EPEX_SPOT", knowledge_at)
    if spot_result.error:
        # Return error - saga will handle (retry or use fallback strategy)
        return {"error": "MARKET_DATA_UNAVAILABLE", "message": spot_result.error}

    spot_price = spot_result.value

    # 3. Execute CEL calculation (cost: 2 units for spot * coeff * markup)
    rate = cel_eval("spot * coeff * markup", {
        "spot": spot_price,
        "coeff": gsp_coefficient,
        "markup": 1.02  # 2% markup
    })

    # 4. Apply to quantity
    output_amount = input_quantity.amount * rate

    return {
        "amount": output_amount,
        "instrument": "GBP",
        "basis": {
            "spot_price": spot_price,
            "gsp_coefficient": gsp_coefficient,
            "final_rate": rate
        }
    }
```

#### CEL Cost Limits

To prevent denial-of-service from runaway expressions, CEL evaluations enforce strict limits:

| Limit | Value | Behavior When Exceeded |
|-------|-------|------------------------|
| **Maximum cost** | 10,000 units | `COST_LIMIT_EXCEEDED` error |
| **Execution timeout** | 100ms | `TIMEOUT_EXCEEDED` error |

**Cost Accounting Rules:**

| Operation | Cost |
|-----------|------|
| Arithmetic (+, -, *, /, %) | 1 unit |
| Comparisons (<, >, ==, !=) | 1 unit |
| Logical operators (&&, \|\|, !) | 1 unit |
| Built-in function calls | 10 units |
| List/map element access | 1 unit per element |
| String operations | 1 unit per character (max 1000) |

**Example:** A strategy performing `spot * coeff * markup` costs 2 units. A strategy iterating over
500 market data points with 3 operations each costs ~1,500 units (within limit).

**Error Response:**

```json
{
  "error": "COST_LIMIT_EXCEEDED",
  "message": "CEL expression exceeded 10,000 cost units (actual: 12,345)",
  "strategy_id": "wholesale-energy-v2"
}
```

**Saga Response:** On cost/timeout exceeded, saga should retry with simpler strategy or fail
gracefully with compensation.

#### Starlark Security Sandbox

Starlark strategies execute in a restricted sandbox:

- **No filesystem access:** `open()`, `read()`, `write()` are undefined
- **No network access:** `http`, `socket`, `requests` are undefined
- **No system calls:** `os`, `subprocess`, `exec` are undefined
- **Execution timeout:** 5 seconds maximum
- **Memory limit:** 64MB per execution

### FR-4: Dimension Guard

**Requirement:** The system MUST prevent "Dimensional Leaks."

**Check:** If an account only accepts `Monetary` value, the valuation engine must verify the
`strategy_id` results in a `Quantity[Monetary]` output.

**Implementation:** Pre-execution validation checks `input_instrument` and `output_instrument`
against strategy definition.

**Conservation of Dimension Enforcement** (per ADR-0028):

- Strategies must declare `ProducesInstrument` metadata
- Runtime validates output matches declaration
- Compile-time checks prevent dimension mixing

#### FR-4.1: Output Instrument Validation (The "Chemical Signature")

**Requirement:** The `GetValuedAmountResponse` MUST return the complete **InstrumentAmount** with full asset identity.

**Rationale:** A USD account must never confuse the caller by returning GBP. The response must include:

- `InstrumentCode` (e.g., "USD", "GBP", "KWH")
- `Version` (for instruments with evolving definitions)
- `Attributes` (for fungibility metadata, e.g., "vintage", "source")

**Validation:** The Valuation Engine MUST verify that the instrument returned by the Starlark/CEL strategy
matches the `output_instrument` defined in the `valuation_assignment`.

**Enforcement Point:** This check happens at **activation time** (when a strategy is assigned to an account),
preventing invalid configurations before they reach production.

**Assertion in Saga:** The calling saga can assert the output instrument matches expectations:

```go
resp, _ := currentAccount.GetValuedAmount(ctx, kwhInput)

if resp.Output.InstrumentCode != expectedInstrument {
    return fmt.Errorf("VALUATION_MISMATCH: expected %s but got %s",
        expectedInstrument, resp.Output.InstrumentCode)
}
```

#### Runtime Output Instrument Validation (The "Chemical Signature" Check)

**Requirement:** The `ResolveValuationStrategy` call MUST return the `OutputInstrumentCode` as a type-hint.
The Valuation Engine MUST then wrap the Starlark/CEL execution with a **post-execution type check**:

```text
Expected: GBP (from strategy definition)
Starlark returned: USD (from script execution)
Result: Hard Error (VALUATION_OUTPUT_MISMATCH)
```

**Why This Matters:**

A strategy bug could accidentally turn an Energy account into a foreign currency account. This runtime
validation prevents dimension leakage that escapes the activation-time checks.

**Implementation:**

```go
func (e *Engine) Valuate(ctx context.Context, req Request) (*Response, error) {
    // 1. Load strategy (includes expected output_instrument)
    strategy, err := e.refDataClient.GetStrategy(ctx, req.StrategyID)
    if err != nil {
        return nil, fmt.Errorf("load strategy: %w", err)
    }

    // 2. Execute Starlark/CEL
    result, err := e.executeScript(ctx, strategy.LogicScript, req)
    if err != nil {
        return nil, fmt.Errorf("execute script: %w", err)
    }

    // 3. CRITICAL: Validate output instrument matches declaration
    if result.Output.InstrumentCode != strategy.OutputInstrument {
        return nil, &ValuationOutputMismatchError{
            Expected: strategy.OutputInstrument,
            Actual:   result.Output.InstrumentCode,
            Strategy: strategy.ID,
        }
    }

    return result, nil
}

// ValuationOutputMismatchError is a hard failure - strategy bug detected
type ValuationOutputMismatchError struct {
    Expected string
    Actual   string
    Strategy uuid.UUID
}

func (e *ValuationOutputMismatchError) Error() string {
    return fmt.Sprintf("VALUATION_OUTPUT_MISMATCH: strategy %s declared output %s but returned %s",
        e.Strategy, e.Expected, e.Actual)
}
```

**When This Fires:**

- Strategy declares `output_instrument: GBP` but returns `USD` → Hard error
- Strategy declares `output_instrument: GBP` but returns `KWH` → Hard error (energy can't become monetary)
- Strategy declares `output_instrument: GBP` and returns `GBP` → Success

**Rationale:** Activation-time validation catches configuration errors. Runtime validation catches
script bugs. Both layers are required for a robust dimension guard.

### FR-5: Valuation Basis (The "Receipt")

**Requirement:** Every valuation result MUST include a **Basis**.

**Audit Trail:** Lists every `MarketPriceObservation.ID` and `Rate` used in the calculation.

**Integrity:** Per ADR-017, the `observation_ids` and rates used in the calculation are stored in the
`PositionEntry.attributes` JSONB field in Position Keeping. This ensures that even if the Market Information
service purges old data after 7 years, the **Basis** stored in the ledger acts as a permanent snapshot
of the evidence used for that valuation.

**Auditability:** Seven years from now, an auditor can examine a single `PositionEntry` and see the complete
"Receipt" for the valuation without calling any external services.

**Quality Level Tracking (Settlement & Reconciliation):**

Per ADR-018, the `ValuationBasis` MUST include the `SourceTrustLevel` of each market data observation used:

- `ESTIMATE` (Quality 1): Forecast or projection
- `COEFFICIENT` (Quality 2): Model-derived value
- `ACTUAL` (Quality 3): Metered or observed value
- `REVISED` (Quality 4): Corrected after audit

**Why This Matters:**

If a valuation was performed using an `ESTIMATE` (Quality 1) at T0, but later an `ACTUAL` (Quality 3)
arrives at T1, the `ValuationBasis` in the ledger provides **proof of why the original (now "wrong") amount
was booked**. This is essential for:

- Settlement processes (explaining why provisional amounts differ from final)
- Reconciliation (tracking estimate-to-actual adjustments)
- Regulatory audit (demonstrating best available information at transaction time)

**Example `ValuationBasis` with Quality Levels:**

```json
{
  "strategy_id": "wholesale-spot-v2",
  "applied_rates": {"EPEX_SPOT": "0.456"},
  "market_data_qualities": [
    {
      "observation_id": "obs_abc123",
      "source_trust_level": "ESTIMATE",
      "instrument_code": "EPEX_SPOT",
      "value": "0.456"
    }
  ]
}
```

Later, when `ACTUAL` arrives, the reconciliation process sees: "Original booking used ESTIMATE quality,
revision is justified."

#### Calculation Path (Audit Trail for Complex Strategies)

**Requirement:** For complex strategies with branching logic (tiered pricing, time-of-use, conditional markups),
the `ValuationBasis` MUST include a **calculation path** - a breadcrumb trail of which decision branches
were taken during execution.

**Purpose:** For M+14 regulatory settlement audits, auditors need to understand which parts of a complex
strategy were executed without re-running the entire logic.

**Implementation:** Starlark strategies call `record_path("tier_2_applied")` at key decision points:

```python
def valuate_tiered(input_quantity, params, knowledge_at):
    projected = position_keeping.get_projected_balance(...)

    if projected.amount < 500.0:
        record_path("tier_1_applied")  # Breadcrumb
        rate = 0.20
    else:
        record_path("tier_2_applied")  # Breadcrumb
        rate = 0.35

    if params.get("customer_tier") == "premium":
        record_path("premium_discount_applied")  # Breadcrumb
        rate = rate * 0.95

    return calculate_value(input_quantity, rate)
```

**Result in ValuationBasis:**

```json
{
  "calculation_path": ["tier_2_applied", "premium_discount_applied"],
  "applied_rates": {"base_rate": "0.35", "discount_multiplier": "0.95"}
}
```

**Audit Value:** Auditor sees: "This transaction used tier 2 pricing with premium discount. No weekend
or time-of-use adjustments applied."

**Size Limit (CRITICAL):**

The `calculation_path` array MUST be limited to **20 entries maximum**. This prevents a complex
Starlark loop from accidentally creating a 10MB JSON blob inside a ledger entry.

**Implementation:**

```go
const MaxCalculationPathEntries = 20

func (ctx *valuationContext) RecordPath(step string) {
    if len(ctx.calculationPath) >= MaxCalculationPathEntries {
        // Set truncation flag for tenant visibility
        ctx.pathTruncated = true
        ctx.truncatedSteps = append(ctx.truncatedSteps, step)

        // Log warning for operations team
        ctx.logger.Warn("calculation_path limit reached, truncating",
            "step", step,
            "limit", MaxCalculationPathEntries,
        )
        return
    }
    ctx.calculationPath = append(ctx.calculationPath, step)
}
```

**Tenant-Visible Warning (Saga Debugger Integration):**

When calculation path is truncated, the `ValuationBasis` MUST include a warning visible in the
Saga Debugger UI so tenants know their audit trail is incomplete:

```go
type ValuationBasis struct {
    // ... existing fields ...
    CalculationPath []string `json:"calculation_path"`

    // NEW: Truncation warning for tenant visibility
    Warnings []ValuationWarning `json:"warnings,omitempty"`
}

type ValuationWarning struct {
    Code    string `json:"code"`     // "CALCULATION_PATH_TRUNCATED"
    Message string `json:"message"`  // Human-readable
    Details any    `json:"details"`  // Additional context
}

// In engine.go, after valuation completes:
if ctx.pathTruncated {
    basis.Warnings = append(basis.Warnings, ValuationWarning{
        Code:    "CALCULATION_PATH_TRUNCATED",
        Message: fmt.Sprintf("Audit trail truncated at %d entries. %d steps omitted.",
            MaxCalculationPathEntries, len(ctx.truncatedSteps)),
        Details: map[string]any{
            "truncated_steps": ctx.truncatedSteps,
            "limit":           MaxCalculationPathEntries,
        },
    })
}
```

**Saga Debugger Display:**

```text
⚠️ CALCULATION_PATH_TRUNCATED
   Audit trail truncated at 20 entries. 3 steps omitted.
   Omitted: ["tier_check_21", "tier_check_22", "final_markup"]
```

**Rationale:** BIAN compliance requires audit trails, but audit trails must be bounded to prevent
storage bloat. 20 entries is sufficient for typical tiered pricing (5-7 tiers) with multiple
conditional branches. Tenants with complex strategies need visibility into truncation.

### FR-6: Caching Strategy

**L1 Cache (In-Memory within Account Service):**

- Compiled CEL expressions
- Recently used valuation strategies
- TTL: 5 minutes (baseline)
- Invalidated on `logic_hash` change

**Key format:** `strategy:{strategy_id}:{logic_hash}`

**Event-Driven Invalidation (Train Track Precision):**

To achieve faster consistency than the 5-minute TTL, the system SHOULD implement event-driven invalidation:

1. When Reference Data updates a `valuation_strategy`, it publishes a `strategy.updated` Kafka event
2. Account Services subscribe to this topic and invalidate their L1 cache immediately
3. New transactions use the updated strategy within milliseconds, not minutes

**Implementation:** This is a P2 enhancement (Stream 8). The 5-minute TTL provides acceptable baseline behavior.

**Graceful Stale Policy (Resilience):**

If Reference Data or Market Information is unavailable and the L1 cache is cold, `InitiateLien` would fail,
blocking the entire saga.

**Mitigation:** The cache SHOULD implement a **"Graceful Stale"** policy:

**For Reference Data (strategies):**

- If Reference Data backend is down, continue using expired strategies for up to **1 hour**
- Log high-priority warning: "Using stale strategy due to Reference Data unavailability"
- Metrics: `valuation_stale_cache_hits_total`

**For Market Information (rates):**

- If Market Information backend is down, the `market_data.get_price()` builtin SHOULD:
  1. Return last known good value from L1 cache (if available)
  2. OR use tenant-configured default rate (fallback)
  3. Log high-priority warning: "Using fallback rate due to Market Information unavailability"
  4. Set `degraded_mode: true` in ValuationBasis
  5. Metrics: `valuation_degraded_mode_total`

**Rationale:** In a ledger, "Calculated with slightly old formula" is preferable to "System Down."
The grace period allows operations teams to restore services without blocking transactions.

**Risk Acceptance:** Degraded mode means the valuation may be less accurate, but the transaction proceeds.
The `degraded_mode` flag in the basis allows downstream reconciliation to identify and adjust these
transactions when services are restored.

#### Degraded Mode MUST Propagate to Ledger (CRITICAL)

The `degraded_mode` flag MUST propagate all the way to the `PositionEntry` in the ledger. Auditors need
to be able to query degraded transactions:

```sql
-- Find all position entries booked with degraded valuation
SELECT *
FROM position_entries
WHERE attributes->'valuation_basis'->>'degraded_mode' = 'true';
```

**Implementation in Position Keeping:**

```go
func (s *Service) Post(ctx context.Context, req *PostRequest) (*PostResponse, error) {
    entry := &PositionEntry{
        AccountID:      req.AccountId,
        Amount:         req.Amount,
        InstrumentCode: req.InstrumentCode,
        // Preserve degraded_mode in attributes JSONB
        Attributes: map[string]interface{}{
            "valuation_basis": req.Basis,  // Includes degraded_mode flag
        },
    }
    // ...
}
```

**Reconciliation Workflow:**

When services are restored after an outage:

1. Query: `SELECT * FROM position_entries WHERE attributes->'valuation_basis'->>'degraded_mode' = 'true'`
2. For each degraded entry, recalculate valuation with restored rates
3. If difference > threshold, create adjustment entry
4. Clear degraded flag (or add `revalued_at` timestamp)

**Why no L2 Redis cache:**

- Bi-temporal queries (`knowledge_at`) make cache hit rate near 0%
- Account Service already has in-memory cache
- Operational complexity not justified

### FR-7: Conservation of Dimension (Recursion Prevention)

**Requirement:** The Valuation Engine MUST NOT trigger write operations back to Position Keeping for the same
asset type being valued.

**Risk:** Without this constraint, a malicious or buggy strategy could create an "Infinite Asset Inflation" loop:

```text
BAD: Valuation triggers position log → Position log triggers valuation → Loop
```

**Enforcement:**

1. **Read-Only Contract:** The `GetValuedAmount` RPC is a **stateless inquiry** (pure function).
   It performs NO writes to Position Keeping, NO writes to Account state.
2. **Domain Boundary:** Valuation is "Math-as-a-Service" - it calculates, it does not transact.
3. **Saga Responsibility:** Only the Saga Orchestrator can write to Position Keeping, and only AFTER
   receiving the valuation response.
4. **Separate Builtin Registry (CRITICAL):** The `shared/pkg/valuation` library MUST use a **different
   Starlark builtin registry** than `shared/pkg/saga`. Specifically, it MUST EXCLUDE all write-capable
   handlers:
   - `position_keeping.initiate_log` (blocked)
   - `financial_accounting.post_entries` (blocked)
   - `current_account.execute_debit` (blocked)
   - Any other state-mutating operations

**Implementation Requirement:**

```go
// shared/pkg/valuation/starlark_runtime.go
func newValuationBuiltins() starlark.StringDict {
    return starlark.StringDict{
        "market_data":    builtinMarketData,     // ✅ Read-only
        "cel_eval":       builtinCelEval,        // ✅ Pure computation
        "quantity":       builtinQuantity,       // ✅ Math operations
        // ❌ NO position_keeping, NO financial_accounting
    }
}
```

#### Architectural Enforcement (CRITICAL)

To prevent a future developer from "helpfully" adding a write handler to the valuation library, the
separation MUST be enforced at the **Go module level**, not just by convention.

##### Option A: Internal Package Isolation (Recommended)

```text
shared/pkg/valuation/
├── internal/
│   └── builtins/          # Cannot import anything outside shared/pkg/valuation
│       ├── market_data.go # ✅ Allowed: Reference Data client
│       ├── cel_eval.go    # ✅ Allowed: CEL runtime
│       └── quantity.go    # ✅ Allowed: Math operations
│       # ❌ FORBIDDEN: Cannot import positionkeepingclient
│       # ❌ FORBIDDEN: Cannot import financialaccountingclient
└── engine.go
```

The `internal/builtins` package is physically prevented from importing write-capable clients by Go's
visibility rules. Any attempt to add `import "meridian/services/position-keeping/client"` will fail
the build.

##### Option B: Build Tags

```go
//go:build valuation_readonly

package builtins

// This file only compiles with valuation_readonly tag
// Production builds enforce this tag, preventing write handlers
```

**Verification:** CI pipeline MUST include a test that attempts to import write clients from the
valuation package and verifies the build fails:

```bash
# ci/verify-valuation-isolation.sh
if go build -tags=test_write_import ./shared/pkg/valuation/...; then
    echo "ERROR: Valuation package should not compile with write imports"
    exit 1
fi
```

**Verification:** Stream 2 MUST include a unit test:

```go
func TestValuationCannotWriteToPositionKeeping(t *testing.T) {
    strategy := `
        def valuate(input, params, knowledge_at):
            # Attempt to write (should fail at VM level)
            position_keeping.initiate_log(...)
            return {"amount": 100}
    `
    engine := NewEngine(...)
    _, err := engine.Valuate(ctx, Request{Strategy: strategy})

    // Expect: name 'position_keeping' is not defined
    assert.ErrorContains(t, err, "name 'position_keeping' is not defined")
}
```

**Architectural Safety:** By making valuation a read-only operation with VM-level enforcement, we prevent:

- Recursive valuation loops
- Non-deterministic execution (valuation affecting its own inputs)
- Unauthorized state mutations
- Malicious or buggy strategies triggering writes

### FR-8: Atomic Valuation in Lien Initiation (Price Lock)

**Requirement:** Account Services MUST perform valuation **atomically** within `InitiateLien` and `ExecuteDeposit`
operations to prevent race conditions in state-dependent pricing.

#### The "Tiered Valuation Drift" Problem

For state-dependent strategies (tiered pricing, volume discounts, time-of-use), querying valuation separately
from reservation creates a TOCTOU (Time-of-Check to Time-of-Use) race condition:

```text
TIME: T0         T1           T2
      ↓          ↓            ↓
Saga A: GetValuedAmount(300 kWh) → £60 (tier 1)
                InitiateLien(300 kWh)
Saga B:          GetValuedAmount(300 kWh) → £60 (WRONG - should be tier 2)
                                  InitiateLien(300 kWh)

Result: Both charged at introductory rate when only first 300 should be.
```

#### The Solution: Valuation-in-Lien

**Transactional Operations** (withdrawals, deposits) MUST calculate valuation atomically:

1. **`InitiateLien`** (withdrawals):
   - Input: `InstrumentAmount` (any asset class)
   - Queries Projected Balance (Current + Active Liens) from Position Keeping
   - Executes valuation strategy using projected state
   - Creates lien storing BOTH `reserved_quantity` AND `valued_amount`
   - Returns lien with **price lock**

2. **`ExecuteDeposit`** (inbound assets):
   - Input: `InstrumentAmount`
   - Queries Projected Balance
   - Executes valuation strategy
   - Posts to Position Keeping with valuation basis

**Updated `InitiateLien` Proto:**

```protobuf
message InitiateLienRequest {
  string account_id = 1;
  meridian.quantity.v1.InstrumentAmount amount = 2;  // Any asset class
  string payment_order_reference = 3;
  meridian.common.v1.IdempotencyKey idempotency_key = 4;
  google.protobuf.Timestamp knowledge_at = 5;
}

message InitiateLienResponse {
  string lien_id = 1;

  // The "Price Lock" - guaranteed value at lien creation time
  meridian.quantity.v1.InstrumentAmount valued_amount = 2;  // e.g., £35.00
  ValuationBasis basis = 3;

  meridian.common.v1.MoneyAmount new_available_balance = 4;
}
```

**Price Lock Guarantee:**

The `valued_amount` in the lien is **immutable**. When `ExecuteLien` is called later, the system uses the
locked value, NOT a recalculated value. This protects both:

- **Customer**: Price won't increase between reservation and execution
- **Merchant**: Tiered discounts can't be gamed by concurrent reservations

#### Lien Immutability Enforcement (Database Level)

**Requirement:** The `liens` table MUST enforce immutability of `valued_amount` once the lien is in
`ACTIVE` status. This prevents both application bugs and direct database manipulation.

##### Implementation Option A: Trigger-Based Enforcement

```sql
-- CockroachDB trigger to prevent valued_amount modification on ACTIVE liens
CREATE FUNCTION prevent_valued_amount_update() RETURNS TRIGGER AS $$
BEGIN
    IF OLD.status = 'ACTIVE' AND NEW.valued_amount IS DISTINCT FROM OLD.valued_amount THEN
        RAISE EXCEPTION 'Cannot modify valued_amount on ACTIVE lien: %', OLD.id;
    END IF;
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

CREATE TRIGGER lien_valued_amount_immutable
    BEFORE UPDATE ON liens
    FOR EACH ROW
    EXECUTE FUNCTION prevent_valued_amount_update();
```

##### Implementation Option B: Application-Level Enforcement

If CockroachDB trigger support is limited, enforce via repository layer:

```go
func (r *LienRepository) Update(ctx context.Context, lien *Lien) error {
    existing, err := r.FindByID(ctx, lien.ID)
    if err != nil {
        return err
    }

    // CRITICAL: Prevent valued_amount modification on ACTIVE liens
    if existing.Status == StatusActive && !existing.ValuedAmount.Equal(lien.ValuedAmount) {
        return fmt.Errorf("LIEN_IMMUTABILITY_VIOLATION: cannot modify valued_amount on ACTIVE lien %s", lien.ID)
    }

    return r.db.Save(lien)
}
```

**Audit Requirement:** Any attempt to modify `valued_amount` on an ACTIVE lien MUST be logged as a
security event for SOC review.

**CRITICAL Constraint:** The `ExecuteLien` RPC MUST be updated to **forbid amount overrides**. The handler
MUST strictly use the `valued_amount` stored in the database lien record created by `InitiateLien`. No
parameters in the `ExecuteLienRequest` may override this value.

**Implementation Check:**

```go
func (s *Service) ExecuteLien(ctx context.Context, req *ExecuteLienRequest) (*ExecuteLienResponse, error) {
    // Load lien from database
    lien, err := s.repo.FindLienByID(ctx, req.LienId)

    // ❌ FORBIDDEN: Allow override
    // if req.OverrideAmount != nil {
    //     amount = req.OverrideAmount
    // }

    // ✅ REQUIRED: Use stored valued_amount
    valuedAmount := lien.ValuedAmount  // Price lock from InitiateLien

    // Post to Position Keeping with locked value
    return s.positionClient.Post(ctx, valuedAmount, lien.Basis)
}
```

**Rationale:** Allowing overrides would defeat the price lock guarantee and reintroduce TOCTOU vulnerabilities.

#### Basis Drift Detection

**Requirement:** If `ExecuteLien` is called and the `knowledge_at` time on the original basis is older than
a configurable threshold (default: 30 days), the system SHOULD emit a `VALUATION_STALE` warning event for
reconciliation.

**Implementation:**

```go
const BasisDriftThreshold = 30 * 24 * time.Hour  // 30 days

func (s *Service) ExecuteLien(ctx context.Context, req *ExecuteLienRequest) (*ExecuteLienResponse, error) {
    lien, err := s.repo.FindLienByID(ctx, req.LienId)
    // ...

    // Check for basis drift
    basisAge := time.Since(lien.Basis.KnowledgeAt)
    if basisAge > BasisDriftThreshold {
        s.events.Publish(ctx, &events.ValuationStaleEvent{
            LienID:      lien.ID,
            BasisAge:    basisAge,
            KnowledgeAt: lien.Basis.KnowledgeAt,
            Threshold:   BasisDriftThreshold,
        })
        s.logger.Warn("executing lien with stale valuation basis",
            "lien_id", lien.ID,
            "basis_age_days", int(basisAge.Hours()/24),
            "threshold_days", 30,
        )
    }

    // Continue with execution using locked value
    valuedAmount := lien.ValuedAmount
    return s.positionClient.Post(ctx, valuedAmount, lien.Basis)
}
```

**Reconciliation Integration:** The `VALUATION_STALE` event allows downstream reconciliation processes
to flag these transactions for review. The lien still executes (business continuity), but auditors are
alerted to potential pricing drift.

#### Idempotency

Retrying `InitiateLien` with same `idempotency_key` returns the existing lien with its original
`valued_amount`. No recalculation.

### FR-9: Projected Balance for State-Dependent Valuation

**Requirement:** Position Keeping MUST provide a `ProjectedBalance` query that includes pending reservations
(active liens).

**Formula:**

```go
ProjectedBalance = CurrentBalance + Sum(ActiveReservations)
```

#### The Architectural Challenge

**Problem:** Liens are stored in **CurrentAccount service**, but `ProjectedBalance` queries happen in
**Position Keeping service**. How does Position Keeping see active liens without:

1. Cross-service JOINs (performance nightmare)
2. Calling back to CurrentAccount (circular dependency)
3. Polling CurrentAccount for lien state (latency, consistency issues)

#### Solution: Reservation Ledger

When `InitiateLien` is called in CurrentAccount, it MUST **synchronously** call a new Position Keeping RPC:

```protobuf
service PositionKeepingService {
  // Record a reservation (called by Account Services during InitiateLien)
  rpc RecordReservation(RecordReservationRequest) returns (RecordReservationResponse);

  // Release a reservation (called during ExecuteLien or TerminateLien)
  rpc ReleaseReservation(ReleaseReservationRequest) returns (ReleaseReservationResponse);

  // Query projected balance (includes reservations)
  rpc GetProjectedBalance(GetProjectedBalanceRequest) returns (GetProjectedBalanceResponse);
}

message RecordReservationRequest {
  string account_id = 1;
  string lien_id = 2;  // Links back to CurrentAccount lien (IDEMPOTENCY KEY)
  meridian.quantity.v1.InstrumentAmount reserved_amount = 3;
  string bucket_id = 4;
  google.protobuf.Timestamp knowledge_at = 5;
}

message ReleaseReservationRequest {
  string lien_id = 1;
  string reason = 2;  // "EXECUTED" or "TERMINATED"
}
```

#### Idempotency Requirement (CRITICAL)

The `RecordReservation` RPC MUST be **idempotent based on `lien_id`**. If `InitiateLien` retries (network
timeout, saga replay), Position Keeping must recognize that the reservation for that `lien_id` already
exists and return success without double-counting.

**Implementation:**

```go
func (s *Service) RecordReservation(
    ctx context.Context,
    req *RecordReservationRequest,
) (*RecordReservationResponse, error) {
    // Check for existing reservation (idempotent check)
    existing, err := s.repo.FindReservationByLienID(ctx, req.LienId)
    if err == nil && existing != nil {
        // Reservation already exists - return success (idempotent)
        return &RecordReservationResponse{
            ReservationId: existing.ID,
            AlreadyExists: true,  // Caller knows this was a retry
        }, nil
    }
    if err != nil && !errors.Is(err, sql.ErrNoRows) {
        return nil, fmt.Errorf("check existing reservation: %w", err)
    }

    // Create new reservation
    reservation := &Reservation{
        LienID:         req.LienId,
        AccountID:      req.AccountId,
        InstrumentCode: req.ReservedAmount.InstrumentCode,
        BucketID:       req.BucketId,
        ReservedAmount: req.ReservedAmount.Amount,
        Status:         "ACTIVE",
    }
    if err := s.repo.CreateReservation(ctx, reservation); err != nil {
        return nil, fmt.Errorf("create reservation: %w", err)
    }

    return &RecordReservationResponse{ReservationId: reservation.ID}, nil
}
```

**Position Keeping Schema Enhancement:**

```sql
CREATE TABLE reservations (
    lien_id UUID PRIMARY KEY,           -- Links to CurrentAccount liens table
    account_id UUID NOT NULL,
    instrument_code VARCHAR(32) NOT NULL,
    bucket_id VARCHAR(64),              -- For fungibility-aware filtering
    reserved_amount DECIMAL NOT NULL,
    status VARCHAR(16) NOT NULL,        -- 'ACTIVE', 'EXECUTED', 'TERMINATED'
    created_at TIMESTAMPTZ NOT NULL,
    executed_at TIMESTAMPTZ,
    terminated_at TIMESTAMPTZ,

    INDEX idx_reservations_active (account_id, instrument_code, status, bucket_id)
);
```

**CurrentAccount Flow Enhancement:**

```go
func (s *Service) InitiateLien(ctx context.Context, req *InitiateLienRequest) (*InitiateLienResponse, error) {
    // 1. Query Projected Balance from Position Keeping (includes active reservations)
    projectedBalance, err := s.positionClient.GetProjectedBalance(ctx, ...)

    // 2. Execute valuation using projected balance
    result, err := s.valuationEngine.Valuate(ctx, ...)

    // 3. Create lien in CurrentAccount database
    lien := s.repo.CreateLien(ctx, Lien{
        ReservedQuantity: req.Amount,
        ValuedAmount:     result.Output,
        Basis:            result.Basis,
    })

    // 4. SYNCHRONOUSLY record reservation in Position Keeping
    _, err = s.positionClient.RecordReservation(ctx, &positionkeepingv1.RecordReservationRequest{
        AccountId:      req.AccountId,
        LienId:         lien.ID,
        ReservedAmount: req.Amount,
        BucketId:       calculateBucketID(req.Amount),
    })
    if err != nil {
        // CRITICAL: Rollback lien if reservation fails
        if delErr := s.repo.DeleteLien(ctx, lien.ID); delErr != nil {
            // Orphaned lien - requires manual cleanup
            s.logger.Error("CRITICAL: Failed to rollback lien after RecordReservation failure",
                "lien_id", lien.ID,
                "original_error", err,
                "rollback_error", delErr,
            )
            s.metrics.Inc("liens.orphaned.total")
            // Write to dead-letter queue for async cleanup
            s.dlq.Publish(ctx, OrphanedLienEvent{
                LienID:        lien.ID,
                AccountID:     req.AccountId,
                OriginalError: err.Error(),
                RollbackError: delErr.Error(),
            })
        }
        return nil, fmt.Errorf("failed to record reservation: %w", err)
    }

    return &InitiateLienResponse{LienId: lien.ID, ValuedAmount: result.Output}, nil
}
```

**Why This Works:**

- **No cross-service JOINs:** Position Keeping owns reservation data
- **No circular dependencies:** One-way call from CurrentAccount → PositionKeeping
- **High performance:** `GetProjectedBalance` is a simple local query
- **Strong consistency:** Synchronous `RecordReservation` ensures atomicity

**Use Case:**

When executing a valuation strategy with state-dependent logic (tiered pricing), the Valuation Engine queries
`ProjectedBalance` instead of `CurrentBalance` to see capacity already spoken for by concurrent transactions.

**Example:**

```python
# Starlark strategy using projected balance
def valuate_tiered(input_quantity, params, knowledge_at):
    # Get projected balance (includes other active liens)
    projected = position_keeping.get_projected_balance(
        account_id=params["account_id"],
        instrument="KWH",
        knowledge_at=knowledge_at
    )

    # Calculate how much of "cheap" tier remains
    cheap_threshold = 500.0
    cheap_available = max(0, cheap_threshold - projected.amount)

    # Price accordingly
    if input_quantity.amount <= cheap_available:
        rate = 0.20  # All in cheap tier
    else:
        # Split across tiers
        cheap_portion = cheap_available * 0.20
        expensive_portion = (input_quantity.amount - cheap_available) * 0.35
        rate = (cheap_portion + expensive_portion) / input_quantity.amount

    return calculate_value(input_quantity, rate)
```

**Position Keeping API:**

```protobuf
message GetProjectedBalanceRequest {
  string account_id = 1;
  string instrument_code = 2;
  google.protobuf.Timestamp knowledge_at = 3;

  // CRITICAL: Bucket filtering for fungibility-aware tiering
  // If strategy uses tiered pricing for source:solar vs source:grid separately,
  // the projection must only include liens/balance for the same bucket
  string bucket_id = 4;  // Optional: filters by instrument attributes
}

message GetProjectedBalanceResponse {
  meridian.quantity.v1.InstrumentAmount current_balance = 1;
  meridian.quantity.v1.InstrumentAmount active_liens_total = 2;
  meridian.quantity.v1.InstrumentAmount projected_balance = 3;  // current + liens

  // Echo back the bucket filter used (for debugging)
  string bucket_id = 4;
}
```

**Bucket Filtering Requirement (CRITICAL):**

When querying `ProjectedBalance`, the system MUST support filtering by `bucket_id`. A `bucket_id` represents
a specific fungibility partition of an instrument.

**Example:**

For tiered pricing on `KWH` with attribute `source: solar` vs `source: grid`:

- Tier strategy for `source:solar` queries: `GetProjectedBalance(instrument=KWH, bucket_id=kwh_solar)`
- Tier strategy for `source:grid` queries: `GetProjectedBalance(instrument=KWH, bucket_id=kwh_grid)`

Without bucket filtering, liens for `source:grid` would incorrectly affect the tier calculation for
`source:solar`, causing cross-contamination of tier thresholds.

**Implementation:** Stream 10 (Position Keeping) MUST implement bucket-aware aggregation using the
`reservations` table:

```sql
-- GetProjectedBalance implementation with Reservation Ledger
WITH current AS (
  SELECT COALESCE(SUM(amount), 0) AS balance
  FROM position_entries
  WHERE account_id = $1
    AND instrument_code = $2
    AND (bucket_id = $3 OR $3 IS NULL)
    AND status IN ('POSTED', 'PENDING')
),
active_reservations AS (
  SELECT COALESCE(SUM(reserved_amount), 0) AS reserved
  FROM reservations
  WHERE account_id = $1
    AND instrument_code = $2
    AND (bucket_id = $3 OR $3 IS NULL)
    AND status = 'ACTIVE'
)
SELECT
  current.balance AS current_balance,
  active_reservations.reserved AS active_liens_total,
  (current.balance + active_reservations.reserved) AS projected_balance
FROM current, active_reservations;
```

**Key Points:**

1. **Local query:** No cross-service calls, no distributed JOINs
2. **Bucket-aware:** Filters both position_entries and reservations by bucket_id
3. **Performance:** Indexed on `(account_id, instrument_code, status, bucket_id)`
4. **O(1) complexity:** Sum aggregations with proper indexes

**Concurrency Safety:**

By using Projected Balance with bucket filtering, the second concurrent lien sees the first lien's
reservation (within the same bucket) and calculates the correct tier pricing. This eliminates the
"Tiered Valuation Drift" bug while preserving fungibility boundaries.

#### Canonical Bucket ID Calculation (CRITICAL)

**Requirement:** The `bucket_id` calculation MUST be identical across all services to prevent "Bucket Drift."

**Solution:** Create a shared library `shared/pkg/bucketing`:

```go
package bucketing

// CalculateBucketID generates a canonical bucket key from an InstrumentAmount.
// Used by: CurrentAccount, InternalBankAccount, PositionKeeping, ReferenceData, Valuation
func CalculateBucketID(amount *quantity.InstrumentAmount) string {
    if amount == nil || len(amount.Attributes) == 0 {
        return ""  // No bucketing for simple instruments
    }

    // Sort attributes for deterministic key generation
    keys := make([]string, 0, len(amount.Attributes))
    for k := range amount.Attributes {
        keys = append(keys, k)
    }
    sort.Strings(keys)

    // Build canonical key: instrument_attr1=val1_attr2=val2
    var parts []string
    parts = append(parts, strings.ToLower(amount.InstrumentCode))
    for _, k := range keys {
        parts = append(parts, fmt.Sprintf("%s=%s", k, amount.Attributes[k]))
    }
    return strings.Join(parts, "_")
}

// Example:
// InstrumentAmount{Code: "KWH", Attributes: {"source": "solar", "vintage": "2024"}}
// → "kwh_source=solar_vintage=2024"
```

**Enforcement:** All services importing the valuation library MUST use `bucketing.CalculateBucketID()`.
Direct bucket_id string construction is FORBIDDEN to prevent drift.

## 5. Technical Architecture

### 5.1 The Transactional Workflow (Atomic Valuation-in-Lien)

**This is the PRIMARY flow for actual transactions (withdrawals, deposits).** Valuation happens atomically
within lien/deposit operations to prevent race conditions.

```mermaid
sequenceDiagram
    participant Saga as Withdrawal Saga
    participant Account as Current Account Service
    participant Lib as Valuation Library (in-process)
    participant PosKeep as Position Keeping
    participant Ref as Reference Data
    participant MIM as Market Information

    Saga->>Account: InitiateLien(account_id, 100 kWh)
    Account->>Account: Load account.valuation_strategy_id from DB

    Note over Account,PosKeep: Get Projected Balance (includes active liens)
    Account->>PosKeep: GetProjectedBalance(account_id, KWH)
    PosKeep-->>Account: current=400 kWh, liens=0, projected=400

    Note over Account,Lib: Execute valuation with projected state
    Account->>Lib: engine.Valuate(strategy_id, projected=400, input=100)

    Lib->>Ref: GetStrategy(strategy_id, knowledge_at)
    Ref-->>Lib: Starlark script (cached 5min)

    Lib->>MIM: GetRate("EPEX_SPOT", knowledge_at)
    MIM-->>Lib: £0.456

    Lib->>Lib: Execute Starlark with tier logic<br/>(remaining cheap tier: 100 kWh)
    Lib-->>Account: Output: £35.00 + Basis

    Note over Account: Create Lien with PRICE LOCK
    Account->>Account: Store lien (100 kWh = £35.00, basis)

    Account-->>Saga: InitiateLienResponse(lien_id, valued_amount=£35.00)

    Note over Saga: Later in saga flow
    Saga->>Account: ExecuteLien(lien_id)
    Account->>PosKeep: Post(100 kWh, £35.00, basis)
```

**Network hop analysis:**

1. Saga → Account: `InitiateLien` request
2. Account → Position Keeping: `GetProjectedBalance` (for tiered pricing)
3. Account → Reference Data: `GetStrategy` (cached 5min)
4. Account → Market Information: `GetRate` (cached)

**Total: 4 network calls** (one more than inquiry-only, but eliminates TOCTOU race)

**Key Difference from Inquiry Flow:**

- **Inquiry (`GetValuedAmount`)**: Returns estimate, no state change, can drift
- **Transactional (`InitiateLien`)**: Creates price lock, queries projected balance, atomic

### 5.1.1 The Inquiry Workflow (Non-Binding, for UX)

For **non-transactional** queries (mobile app, dashboard), use the inquiry RPC:

```mermaid
sequenceDiagram
    participant App as Mobile App
    participant Account as Current Account Service
    participant Lib as Valuation Library (in-process)
    participant Ref as Reference Data
    participant MIM as Market Information

    App->>Account: GetValuedAmount(account_id, 100 kWh)
    Account->>Account: Load account.valuation_strategy_id

    Account->>Lib: engine.Valuate(strategy_id, params)
    Lib->>Ref: GetStrategy(strategy_id)
    Ref-->>Lib: Starlark script (cached)
    Lib->>MIM: GetRate("EPEX_SPOT")
    MIM-->>Lib: £0.456
    Lib-->>Account: Output: £35.00 + Basis

    Account-->>App: GetValuedAmountResponse(is_estimate=true)

    Note over App: Display "~£35.00" (informational only)
```

**WARNING:** This value is non-binding. Actual transaction may differ if:

- Balance changes (tier transitions)
- Market rates update
- Concurrent transactions reserve capacity

### 5.2 Library Structure

```text
shared/pkg/valuation/
├── engine.go                  # Core valuation engine
│   └── type Engine struct
│   └── func (e *Engine) Valuate(ctx, req) (*Response, error)
├── builtins.go               # Starlark builtins
│   └── market_data.get_price()
│   └── cel_eval()
│   └── quantity operations
├── cache.go                  # L1 in-memory cache
│   └── Strategy cache (5min TTL)
│   └── CEL expression cache
├── cel_runtime.go            # CEL compiler wrapper
│   └── Security constraints
│   └── Cost limits
├── starlark_runtime.go       # Starlark VM wrapper
│   └── Deterministic execution
│   └── Timeout controls
└── types.go                  # Request/Response types
    └── type Request, Response, Basis
```

### 5.3 The "Passport Pattern" - Audit Integrity Across Flows

The **Basis** (the valuation receipt) must be persisted to ensure the ledger is auditable. This creates a
**three-layer persistence model**, with different behavior for inquiry vs transactional flows:

#### Layer 1: Account Service

**Inquiry Flow (`GetValuedAmount`)**: Stateless, NO writes. Pure "Math-as-a-Service."

- **Why:** 100,000 inquiries/hour shouldn't write to Account database
- **Performance:** <5ms p99 by eliminating DB contention

**Transactional Flow (`InitiateLien`, `ExecuteDeposit`)**: WRITES lien/deposit record with valuation.

- **Why:** Price lock requires persistence
- **What's Stored:**
  - `reserved_quantity` / `deposited_quantity`
  - `valued_amount` (price lock)
  - `valuation_basis` (audit trail)
- **Performance:** Acceptable overhead (<10ms) for transactional guarantees

#### Layer 2: Saga Orchestrator (Checkpoint Persistence)

The Saga DOES persist the result. Per ADR-028 (Durable Execution Engine), the saga saves the response of every
step into its `saga_step_results` table.

**Audit Value:** If a pod dies and the saga replays, it doesn't re-calculate the value; it pulls the "frozen"
result from the last checkpoint. This guarantees **deterministic replay** - the same saga execution always sees
the same valuation, even if market rates have changed since.

**Example:**

```json
{
  "step_id": "valuate_energy",
  "result": {
    "output": {"amount": "35.00", "instrument": "GBP"},
    "basis": {
      "strategy_id": "wholesale-spot-v2",
      "rates": {"EPEX_SPOT": "0.456"},
      "observation_ids": ["obs_abc123"],
      "knowledge_at": "2025-01-15T14:30:00Z"
    }
  }
}
```

#### Layer 3: Position Keeping (Permanent Audit)

When the transaction finally hits **Position Keeping**, the `ValuationBasis` is stored in the `attributes` JSONB
of the `PositionEntry`.

**Audit Value:** Seven years from now, an auditor can examine a single row in the ledger and see the complete
"Receipt" for the valuation without calling any external services. Even if:

- The Market Information service has purged old rate data
- The Reference Data service has deprecated the strategy
- The Account Service has been decommissioned

**Example `PositionEntry.attributes`:**

```json
{
  "valuation_basis": {
    "strategy_id": "wholesale-spot-v2",
    "strategy_version": "1.2.0",
    "applied_rates": {"EPEX_SPOT": "0.456", "gsp_coefficient": "1.05"},
    "observation_ids": ["obs_abc123"],
    "computed_at": "2025-01-15T14:30:15Z",
    "knowledge_at": "2025-01-15T14:30:00Z",
    "account_parameters": {"gsp": "P", "tier": "Gold"}
  }
}
```

#### The "Passport Analogy"

The `ValuationBasis` travels through the system like a passport:

1. **Issued** by the Account Service (valuation calculation)
2. **Stamped** by the Saga Orchestrator (checkpoint persistence)
3. **Archived** by Position Keeping (permanent ledger entry)

At each layer, the Basis provides **proof of origin** and **proof of calculation** for regulatory audit.

### 5.4 Account Service Integration

```go
// services/current-account/internal/service/valuation.go
package service

import "meridian/shared/pkg/valuation"

func (s *Service) GetValuedAmount(
    ctx context.Context,
    req *currentaccountv1.GetValuedAmountRequest,
) (*currentaccountv1.GetValuedAmountResponse, error) {

    // 1. Load account to get strategy assignment
    account, err := s.repo.FindByID(ctx, req.AccountId)
    if err != nil {
        return nil, fmt.Errorf("load account: %w", err)
    }

    // 2. Resolve strategy assignment for input instrument
    assignment, err := s.getValuationAssignment(
        ctx,
        account.ID,
        req.Input.InstrumentCode,
        req.KnowledgeAt,
    )
    if err != nil {
        return nil, fmt.Errorf("resolve assignment: %w", err)
    }

    // 3. Use shared valuation library (in-process)
    result, err := s.valuationEngine.Valuate(ctx, valuation.Request{
        Input:       req.Input,
        StrategyID:  assignment.StrategyID,
        Parameters:  assignment.Parameters,
        KnowledgeAt: req.KnowledgeAt.AsTime(),
    })
    if err != nil {
        return nil, fmt.Errorf("execute valuation: %w", err)
    }

    // 4. Return valued amount with audit basis
    return &currentaccountv1.GetValuedAmountResponse{
        Output:          result.Output,
        Basis:           toProtoBasis(result.Basis),
        ExecutionTimeMs: fmt.Sprintf("%.2f", result.ExecutionTime.Milliseconds()),
        CacheHit:        result.CacheHit,
    }, nil
}
```

### 5.5 Dependency Injection

```go
// services/current-account/cmd/current-account-service/main.go
func main() {
    // Existing clients
    positionClient := positionkeepingclient.New(...)
    finAcctClient := financialaccountingclient.New(...)

    // NEW - Add clients for valuation
    refDataClient := referencedataclient.New(...)
    marketInfoClient := marketinformationclient.New(...)

    // Create valuation engine with dependencies
    valuationEngine := valuation.NewEngine(valuation.Config{
        RefDataClient:    refDataClient,     // For strategy lookups
        MarketInfoClient: marketInfoClient,  // For rate lookups
        CacheSize:        1000,              // L1 cache entries
        CacheTTL:         5 * time.Minute,
        Logger:           logger,
    })

    // Inject into service
    svc := service.New(service.Config{
        Repository:       repo,
        ValuationEngine:  valuationEngine,  // NEW
        PositionClient:   positionClient,
        FinAcctClient:    finAcctClient,
    })
}
```

## 6. Implementation Streams

### Stream 1: Account Strategy Assignments (P0, 5 points)

**Tasks:**

1. Add `valuation_assignments` table to Current Account service
2. Add `valuation_assignments` table to Internal Bank Account service
3. Implement CRUD operations for assignments
4. Add bi-temporal query support
5. Update Tenant Provisioning to seed default strategies (e.g., `USD_IDENTITY`)
6. Migration scripts for existing accounts

**Success Criteria:**

- All existing accounts have at least one valuation assignment (identity strategy)
- Bi-temporal queries work correctly with `knowledge_at`
- Assignments can be updated without service restart

### Stream 2: Valuation Engine Library (P0, 12 points)

**Tasks:**

1. Create `shared/pkg/valuation` package structure
2. Implement CEL compiler wrapper with security constraints
3. Implement Starlark VM wrapper with timeouts
4. Register built-in functions (market_data, cel_eval, quantity operations)
5. **CRITICAL:** Implement separate read-only builtin registry (exclude position_keeping, financial_accounting)
6. **CRITICAL:** Use `internal/builtins` package isolation to enforce read-only at Go module level
7. Implement L1 in-memory cache with graceful stale policy (1-hour grace if backend down)
8. Implement `record_path()` builtin with 20-entry size limit
9. Implement output instrument validation (runtime type check)
10. Add comprehensive unit tests
11. **CRITICAL:** Add security verification test (strategy cannot call write handlers)
12. **CRITICAL:** Add CI verification script that fails if write imports are added
13. Add benchmarks (target: <5ms in-process execution)
14. Document library usage patterns

**Success Criteria:**

- Can compile and execute CEL expressions
- Can execute Starlark scripts with all builtins
- Expression cost limits prevent infinite loops
- Benchmark shows <5ms execution time for typical strategies
- Cache hit rate >80% after warmup (for same strategy_id)
- **Security test passes:** Strategy attempting `position_keeping.initiate_log` fails with
  `name 'position_keeping' is not defined`
- **Architecture test passes:** Build fails if valuation package imports write-capable clients
- Graceful stale cache activates when Reference Data unavailable (logs warning, continues)
- Calculation path limited to 20 entries (logs warning on truncation)
- Output instrument mismatch returns hard error (VALUATION_OUTPUT_MISMATCH)

### Stream 3: Reference Data Strategy Storage (P0, 6 points)

**Tasks:**

1. Add `valuation_strategies` table to Reference Data service
2. Implement `GetStrategy` RPC with bi-temporal support
3. Implement `ResolveValuationStrategy` RPC with **Platform Default Inheritance**
4. Add strategy validation (syntax check, instrument compatibility)
5. Add `output_instrument` to strategy response (for runtime type validation)
6. Seed SYSTEM identity strategies for major currencies (USD, EUR, GBP, NZD, AUD)
7. Seed SYSTEM energy strategy (KWH → GBP, default £0.35/kWh)
8. Seed SYSTEM carbon strategy (TONNE_CO2E → GBP, default £75/tonne)
9. Add integration tests for tenant → SYSTEM fallback

**Success Criteria:**

- Strategies can be stored and retrieved via gRPC
- Bi-temporal queries return correct strategy versions
- Cache invalidation works on strategy updates
- Identity strategies are available for all fiat currencies
- **Platform Default Inheritance:** Tenant without KWH strategy gets SYSTEM_RETAIL_ENERGY
- **Output instrument returned:** GetStrategy response includes `output_instrument` for runtime validation
- New tenant can valuate KWH without any configuration (uses SYSTEM defaults)

### Stream 4: Current Account Integration (P1, 10 points)

**Tasks:**

1. Add `GetValuedAmount` RPC to Current Account proto (inquiry-only)
2. Update `InitiateLien` to accept `InstrumentAmount` (any asset class)
3. Update `InitiateLien` response to include `valued_amount` and `basis`
4. Update `ExecuteDeposit` to perform atomic valuation
5. Wire up valuation library in service initialization
6. Add Position Keeping client for projected balance AND reservation RPCs
7. Implement valuation in `InitiateLien` handler (price lock)
8. **CRITICAL:** Call `position_keeping.RecordReservation()` after creating lien
9. **CRITICAL:** Call `position_keeping.ReleaseReservation()` in ExecuteLien/TerminateLien
10. Implement valuation in `ExecuteDeposit` handler
11. Implement `GetValuedAmount` handler (inquiry-only, non-binding)
12. Add Market Information client dependency with graceful degradation
13. Add `record_path()` builtin for calculation path audit trail
14. Add observability (metrics, logging, tracing, degraded_mode counter)
15. Integration tests with mock strategies
16. Concurrency tests (verify tiered pricing with parallel liens)
17. Rollback test: If RecordReservation fails, lien must be deleted

**Success Criteria:**

- `InitiateLien` accepts non-monetary assets (kWh, TONNE_CO2E)
- Lien stores price lock (valued_amount) alongside reserved_quantity
- **RecordReservation called synchronously** after lien creation
- **ReleaseReservation called** during ExecuteLien/TerminateLien
- Concurrent liens on tiered pricing account get correct rates (no drift)
- `GetValuedAmount` returns `is_estimate=true` (non-binding)
- Valuation basis includes calculation_path and degraded_mode flag
- **Basis drift detection:** ExecuteLien emits VALUATION_STALE event if basis > 30 days old
- **Output instrument validation:** Valuation fails with VALUATION_OUTPUT_MISMATCH if script returns wrong instrument
- Graceful degradation when Market Information unavailable
- Metrics show execution time and cache hit rate

### Stream 5: Internal Bank Account Integration (P1, 10 points)

**Tasks:**

1. Add `GetValuedAmount` RPC to Internal Bank Account proto (inquiry-only)
2. Update `InitiateLien` to accept `InstrumentAmount` (any asset class)
3. Update `InitiateLien` response to include `valued_amount` and `basis`
4. Update `ExecuteDeposit` to perform atomic valuation
5. Wire up valuation library (same as Current Account)
6. Add Position Keeping client for projected balance AND reservation RPCs
7. Implement valuation in `InitiateLien` handler (price lock)
8. **CRITICAL:** Call `position_keeping.RecordReservation()` after creating lien
9. **CRITICAL:** Call `position_keeping.ReleaseReservation()` in ExecuteLien/TerminateLien
10. Implement valuation in `ExecuteDeposit` handler
11. Implement `GetValuedAmount` handler (inquiry-only, non-binding)
12. Add Market Information client dependency with graceful degradation
13. Add `record_path()` builtin for calculation path audit trail
14. Integration tests
15. Concurrency tests (verify tiered pricing with parallel liens)
16. Rollback test: If RecordReservation fails, lien must be deleted

**Success Criteria:**

- Internal accounts can value assets using same strategies
- Wholesale energy strategy works (spot price × GSP coefficient)
- **RecordReservation called synchronously** after lien creation
- **ReleaseReservation called** during ExecuteLien/TerminateLien
- `InitiateLien` stores price lock for regulatory accounts
- Concurrent liens handle tiered pricing correctly
- Valuation basis includes calculation_path for regulatory audit
- Audit trail is complete for regulatory accounts

### Stream 6: Saga Integration (P1, 8 points)

**Tasks:**

1. Update withdrawal saga to use `InitiateLien` for non-monetary assets (no separate valuation call)
2. Update saga to extract `valued_amount` from `InitiateLienResponse`
3. Update saga to pass valuation basis to Position Keeping
4. Update deposit saga to use atomic valuation in `ExecuteDeposit`
5. Update Position Keeping to store valuation basis in `PositionEntry.attributes` JSONB
6. Add valuation basis to audit logs
7. Update saga replay logic to use checkpointed valuations (deterministic replay)
8. Integration tests for end-to-end flows with tiered pricing
9. Chaos tests (pod kills during valuation - verify deterministic replay)
10. Update operator runbooks

**Success Criteria:**

- Withdrawal sagas use `InitiateLien` (valuation atomic, no separate call)
- Deposit sagas use atomic valuation in `ExecuteDeposit`
- Position entries include valuation basis in attributes JSONB
- Can replay historical valuations using `knowledge_at` (saga checkpoints)
- Saga replay is deterministic (same valued_amount from checkpoint)
- Audit logs show full valuation provenance
- No TOCTOU race conditions under concurrent load

### Stream 7: Energy/Commodity Strategies (P2, 8 points)

**Tasks:**

1. Design wholesale energy strategy (Spot Price × GSP Coefficient)
2. Implement retail energy strategy (Fixed Tariff)
3. Add time-of-use (TOU) tariff support
4. Add carbon credit valuation strategy
5. Add GPU-hour valuation strategy (AI compute)
6. Comprehensive integration tests for each asset type
7. Document strategy development guide

**Success Criteria:**

- Can value 100 kWh using wholesale spot price + GSP coefficient
- Can value 100 kWh using retail fixed tariff
- TOU tariff applies different rates based on time bands
- All asset types (energy, carbon, compute) have working strategies

### Stream 8: Performance Optimization (P2, 3 points)

**Tasks:**

1. Profile valuation execution paths
2. Optimize cache key generation
3. Add connection pooling for Reference Data client
4. Tune cache sizes and TTLs
5. Implement event-driven cache invalidation (Kafka subscriber for `strategy.updated` events)
6. Load testing (target: 500 valuations/second per Account Service instance)

**Success Criteria:**

- p99 latency < 8ms under normal load
- Cache hit rate >80% after 5-minute warmup
- Event-driven invalidation reduces staleness to <100ms (from 5min TTL baseline)
- No memory leaks in long-running tests
- Graceful degradation if Market Information is slow

### Stream 9: Lien Schema Enhancement (P0, 3 points)

**Tasks:**

1. Add `valued_amount` field to `liens` table (InstrumentAmount)
2. Add `valuation_basis` field to `liens` table (JSONB)
3. Update `InitiateLien` to store valuation in lien record
4. Update `ExecuteLien` to use stored valued_amount (not recalculate)
5. Add `idempotency_check` to return existing lien with original valuation
6. Migration scripts for existing liens (backfill with identity valuation)

**Success Criteria:**

- Liens store both reserved_quantity and valued_amount
- `ExecuteLien` uses price lock from lien creation time
- Idempotent retries return same valuation (no recalculation)
- Lien basis stored in JSONB includes observation_ids, rates, strategy version

### Stream 10: Position Keeping Reservation Ledger & Projected Balance (P0, 8 points)

**Architectural Note:** This stream implements the Reservation Ledger pattern to avoid cross-service
dependencies. Position Keeping owns reservation data, enabling local-only projected balance queries.

**Tasks:**

1. Add `reservations` table to Position Keeping schema (see FR-9 for schema)
2. Add `RecordReservation` RPC (called by Account Services during InitiateLien)
3. **CRITICAL:** Implement `RecordReservation` idempotency based on `lien_id` (see FR-9)
4. Add `ReleaseReservation` RPC (called during ExecuteLien/TerminateLien)
5. Add `GetProjectedBalance` RPC with bucket filtering
6. Implement projected balance query using local reservations table:
   - `SELECT SUM(amount) FROM position_entries WHERE ... AND bucket_id = ?`
   - `SELECT SUM(reserved_amount) FROM reservations WHERE status = 'ACTIVE' AND bucket_id = ?`
7. Add indexes: `(account_id, instrument_code, status, bucket_id)` on both tables
8. **CRITICAL:** Use `shared/pkg/bucketing.CalculateBucketID()` for bucket calculation
9. Add caching (5-minute TTL, invalidated on writes)
10. Add integration tests: RecordReservation → GetProjectedBalance → ReleaseReservation lifecycle
11. **CRITICAL:** Add idempotency test: duplicate RecordReservation returns success without double-counting
12. Add integration tests with concurrent reservations on same bucket
13. Add integration tests with concurrent reservations on different buckets (verify isolation)
14. Performance testing (must complete <10ms for projected balance queries)

**Success Criteria:**

- `RecordReservation` creates reservation record in Position Keeping
- **RecordReservation is idempotent:** Retrying with same `lien_id` returns success, no double-count
- `ReleaseReservation` marks reservation as EXECUTED or TERMINATED
- `GetProjectedBalance` returns current balance + active reservations (local query only)
- **No cross-service JOINs** - Position Keeping doesn't call back to Account Services
- Bucket filtering works: reservations for `source:solar` don't affect `source:grid` tiers
- Second concurrent reservation sees first reservation (within same bucket)
- Performance: <10ms p99 for projected balance queries
- Concurrency test: 10 parallel reservations on tiered account all get correct rates
- Isolation test: Solar reservations don't contaminate grid tier calculations
- Idempotency test: 3 retries of same RecordReservation don't affect ProjectedBalance

### Stream 11: Shared Bucketing Library (P0, 2 points)

**Tasks:**

1. Create `shared/pkg/bucketing` package
2. Implement `CalculateBucketID(InstrumentAmount) string` with canonical key generation
3. Add comprehensive unit tests for edge cases (nil, empty attributes, special characters)
4. Document bucket key format and usage patterns
5. Audit all services to ensure consistent bucket_id usage

**Success Criteria:**

- All services use `bucketing.CalculateBucketID()` for bucket calculation
- No direct string construction of bucket_id anywhere in codebase
- Same InstrumentAmount produces identical bucket_id across all services
- Unit tests cover: nil input, empty attributes, multi-attribute sorting, special characters

## 7. Testing Strategy

### Unit Tests

- CEL expression compilation and evaluation
- Starlark script parsing and execution
- Cache hit/miss logic (L1 only)
- Strategy validation
- Dimension Guard enforcement

### Integration Tests

- End-to-end valuation with real Reference Data and Market Information
- Bi-temporal valuation replay
- Cache invalidation on strategy updates
- Multiple concurrent valuations (inquiry RPC)
- **Atomic valuation in liens** - verify price lock persisted
- **Tiered pricing with concurrent liens** - verify no drift (TOCTOU prevention)
- **Saga deterministic replay** - verify checkpointed valuation used on replay
- **Projected balance queries** - verify second lien sees first lien's reservation

### Performance Tests

- Single valuation latency: <8ms (p99)
- Throughput: >500/sec per Account Service instance
- Cache hit rate: >80% after warmup
- Memory usage under sustained load

### Golden File Tests

- Regression detection for strategy outputs
- Store expected results for known inputs
- Validate outputs match across versions

## 8. Success Metrics

1. **Zero Hardcoded Rates:** All conversion math moves to Starlark/CEL by end of Stream 7.
2. **Replay Parity:** Replaying a valuation from 1 year ago using `knowledge_at` produces the exact
   same result (±1 basis point).
3. **Performance:** 95th percentile valuation latency < 8ms under normal load.
4. **Cache Efficiency:** Cache hit rate >80% after 5-minute warmup period.
5. **Audit Compliance:** 100% of position entries include valuation basis with full provenance.
6. **Integration Success:** All sagas using multi-asset quantities migrate successfully.

## 9. Deployment Considerations

### Rollout Strategy

1. **Phase 1 (Week 1-2):** Deploy shared library with identity strategies only
   (Stream 1-3)
2. **Phase 2 (Week 3-4):** Enable Account Service integration
   (Stream 4-5)
3. **Phase 3 (Week 5-6):** Integrate with all sagas, deprecate hardcoded logic
   (Stream 6)
4. **Phase 4 (Week 7+):** Add energy/commodity strategies
   (Stream 7)

### Strategy Versioning and Migration

**Version Resolution:**

Account assignments reference strategies by `strategy_id`. When multiple versions exist, the system
resolves to the **latest non-deprecated version** unless the assignment explicitly pins a version.

| Scenario | Behavior |
|----------|----------|
| New strategy version deployed | Existing assignments auto-upgrade to latest |
| Strategy deprecated (`deprecated_at < now`) | New valuations REJECTED, bi-temporal replay allowed |
| Assignment pins specific version | Version locked until explicit migration |

**Migration Path:**

1. **Phase 1-2:** All assignments use unpinned references (auto-upgrade)
2. **Phase 3:** Explicit migration for any pinned assignments via `task-master` task
3. **Post-Phase 3:** Deprecate old versions, assignments auto-resolve to latest

**What Happens When `logic_hash` Changes:**

When a strategy's logic is updated (new `logic_hash`), the change creates a new version. Existing
assignments using unpinned references will use the new logic on next valuation. Assignments pinned
to the old version continue using old logic until explicitly migrated.

**Operational Notifications:**

- `strategy.version.created` event → Slack alert to ops channel
- `strategy.deprecated` event → P3 alert, 7-day countdown to removal
- `strategy.removed` event → P2 alert if any assignments still reference it

**Deprecated Strategy Behavior:**

```go
func (s *Service) GetStrategy(ctx context.Context, id uuid.UUID) (*Strategy, error) {
    strategy, err := s.repo.FindByID(ctx, id)
    if err != nil {
        return nil, err
    }

    // Reject new valuations using deprecated strategies
    if strategy.DeprecatedAt != nil && strategy.DeprecatedAt.Before(time.Now()) {
        return nil, &StrategyDeprecatedError{
            StrategyID:   id,
            DeprecatedAt: *strategy.DeprecatedAt,
            Message:      "Strategy deprecated, migrate to latest version",
        }
    }

    return strategy, nil
}
```

**Bi-temporal Replay Exception:**

For audit and replay purposes, deprecated strategies remain queryable via `GetStrategy` with
`knowledge_at` parameter set to a time before `deprecated_at`. This allows historical valuations
to be reproduced exactly.

### Monitoring and Alerting

**Metrics:**

- `valuation.requests.total` (counter by account_service, status)
- `valuation.duration_ms` (histogram by strategy_id)
- `valuation.cache_hit_rate` (gauge by account_service)
- `valuation.strategy_errors` (counter by strategy_id, error_type)
- `valuation.market_data_lookups` (counter by observation_type)

**Alerts:**

- P1: Account service unavailable (no successful GetValuedAmount in 5 minutes)
- P2: Valuation latency p99 > 15ms (performance degradation)
- P2: Cache hit rate < 50% (cache inefficiency)
- P3: Strategy execution errors > 1% of requests (buggy strategy)

### Disaster Recovery

#### Scenario: Reference Data Service Down

- **Mitigation:** L1 cache keeps serving recently used strategies (5min TTL)
- **Fallback:** Use identity transformation if strategy unavailable
- **Alert:** P2 escalation, automatic retry with exponential backoff

#### Scenario: Market Information Service Down

- **Mitigation:** Return last-known-good rate from Market Information cache
- **Fallback:** Use default rates from account parameters
- **Alert:** P2 escalation, flag valuations as "degraded mode" in basis

#### Scenario: Buggy Strategy Deployed

- **Mitigation:** Account Service catches execution errors, returns error response
- **Fallback:** Saga retries or uses fallback strategy
- **Alert:** P2 escalation, notify strategy author

## 10. Open Questions

1. **Cross-Currency Triangulation:** How do we handle currency pairs without direct market data
   (e.g., NZD/AUD via USD)?
   - **Proposed Answer:** Starlark script can compose multiple market data lookups:
     `nzd_to_usd * usd_to_aud`

2. **Regulatory Compliance:** Do valuation strategies need regulatory approval before activation?
   - **Action:** Consult with compliance team on approval workflow requirements

3. **Historical Market Data Retention:** How long do we keep market data for replay?
   - **Proposed Answer:** 7 years (regulatory standard for financial records)

4. **Multi-Tenant Strategy Sharing:** Should strategies be tenant-scoped or globally shared?
   - **Proposed Answer:** Strategies stored per-tenant (PostgreSQL schema-per-tenant),
     marketplace allows copying

## 11. Related Work

- [ADR-0028: Starlark Saga Orchestration with CEL Valuation](../adr/0028-starlark-saga-cel-valuation.md)
- [ADR-0013: Generic Asset Quantity Types](../adr/0013-generic-asset-quantity-types.md)
- [ADR-0014: Financial Instrument Reference Data](../adr/0014-financial-instrument-reference-data.md)
- [PRD: Universal Asset System](universal-asset-system.md)
- [PRD: Market Information Management](market-information-management.md)
- [PRD: Starlark Saga Orchestration Core](starlark-saga-orchestration-core.md)

## 12. Comparison to Standalone Service Approach

### Why Embedded Library > Standalone Service

| Aspect | Standalone Service | Embedded Library (This PRD) |
|--------|-------------------|----------------------------|
| **Network Hops** | 4 (Saga→Account→Valuation→RefData/MIM) | 3 (Saga→Account→RefData/MIM) |
| **Latency** | 12-17ms (estimated) | 5-8ms (measured goal) |
| **Story Points** | 68 points | 75 points (+10% for TOCTOU safety) |
| **Operational Complexity** | New microservice to deploy/monitor | Reuses existing services |
| **Domain Modeling** | External service | Account capability |
| **Dependency Graph** | Saga → Account → Valuation → ... | Saga → Account → ... |
| **Cache Strategy** | L1 + L2 Redis (complex) | L1 only (simple) |
| **Failure Modes** | Valuation service down = all accounts blocked | Account service down = only that account blocked |

**Story Point Justification:** The embedded approach is 75 points vs 68 for standalone (+10%). The
additional complexity buys: (1) Atomic valuation with price lock eliminating TOCTOU race conditions
(v2.2), (2) Reservation Ledger pattern for projected balance without cross-service JOINs (v2.4),
(3) Graceful degradation and resilience features (v2.3), (4) Architecture guards and idempotency (v2.5).
These are not optional features—they're required for correctness under concurrent load.

**Key insight:** Valuation is a **behavior** of the Account aggregate, not a separate Bounded Context.

### Architecture Decision Rationale

The embedded library approach follows the same pattern as:

- `shared/pkg/saga` - Starlark runtime used by multiple services
- `shared/platform/database` - Database utilities
- `shared/platform/observability` - Metrics/tracing

**Account Services are the natural home for valuation because:**

1. They own the `valuation_assignments` data
2. They know which dimension they accept (enforcement point)
3. They have context about the account (parameters, tier, etc.)
4. They're already in the critical path for deposits/withdrawals

## 13. Appendix: Example Strategies

### A. Identity Strategy (Fiat Currency)

```python
# strategy: identity_usd
# input: USD
# output: USD

def valuate(input_quantity, params, knowledge_at):
    """Identity transformation - 1:1 conversion."""
    return {
        "amount": input_quantity.amount,
        "instrument": input_quantity.instrument,
        "basis": {
            "strategy": "identity",
            "rate": "1.0"
        }
    }
```

### B. Retail Energy Strategy

```python
# strategy: retail_energy_v1
# input: KWH
# output: GBP

def valuate(input_quantity, params, knowledge_at):
    """Fixed tariff pricing for retail energy."""
    # Get tariff from account parameters
    tariff_rate = params.get("tariff_rate", 0.35)  # Default £0.35/kWh

    # Apply tariff
    output_amount = input_quantity.amount * tariff_rate

    return {
        "amount": output_amount,
        "instrument": "GBP",
        "basis": {
            "strategy": "retail_energy_v1",
            "tariff_rate": str(tariff_rate),
            "input_kwh": str(input_quantity.amount)
        }
    }
```

### C. Wholesale Energy Strategy (Complex)

```python
# strategy: wholesale_energy_v1
# input: KWH
# output: GBP

def valuate(input_quantity, params, knowledge_at):
    """Spot price + GSP coefficient for wholesale energy."""

    # 1. Get spot price from market data
    spot_price = market_data.get_price(
        instrument="EPEX_SPOT_GBP",
        knowledge_at=knowledge_at
    )

    # 2. Get GSP coefficient from account parameters
    gsp_zone = params.get("gsp_zone", "_P")
    gsp_coefficient = params.get("gsp_coefficient", 1.0)

    # 3. Apply markup (if any)
    markup = params.get("markup", 1.0)

    # 4. Calculate using CEL for precision
    final_rate = cel_eval("spot * coeff * markup", {
        "spot": spot_price.value,
        "coeff": gsp_coefficient,
        "markup": markup
    })

    # 5. Apply to quantity
    output_amount = input_quantity.amount * final_rate

    return {
        "amount": output_amount,
        "instrument": "GBP",
        "basis": {
            "strategy": "wholesale_energy_v1",
            "spot_price": str(spot_price.value),
            "spot_observation_id": spot_price.observation_id,
            "gsp_zone": gsp_zone,
            "gsp_coefficient": str(gsp_coefficient),
            "markup": str(markup),
            "final_rate": str(final_rate)
        }
    }
```

---

**Architectural Philosophy:** This PRD implements the "Probe Pattern" - accounts are queried for value,
not commanded to use a centralized pricing service. It's "Particular" (logic is account-scoped) yet
"Universal" (all accounts implement the same interface).

**Next Steps:** Parse PRD into Task Master (`valuation-engine` tag) and begin with Stream 1
(Account Strategy Assignments) as foundation for all subsequent work.
