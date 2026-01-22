# PRD: Starlark Saga Orchestration

**Status:** Draft
**Version:** 1.4
**Author:** Architecture Team
**ADR Reference:** [ADR-028](../adr/0028-starlark-saga-cel-valuation.md)

## 1. Executive Summary

The Starlark Saga Orchestration system migrates hardcoded Go saga logic to
runtime-configurable workflow definitions. Saga definitions are stored in the
Reference Data service (alongside instrument definitions), cached in Redis for
performance, and executed by a shared runtime.

### The Problem Statement

Current saga orchestration is hardcoded in Go, creating operational bottlenecks:

| Pain Point | Business Impact |
|------------|-----------------|
| **1:1 position-to-posting coupling** | All tenants get identical ledger posting patterns |
| **Custom workflows require code changes** | 2-4 week lead time for tenant-specific logic |
| **Platform becomes bottleneck** | Engineering backlog blocks tenant operations |
| **No self-service** | Tenants cannot define operational workflows (vouchers, credits, adjustments) |

### The Solution

Saga definitions written in **Starlark** (a safe subset of Python):

- **Tenant-configurable**: Each tenant can override platform defaults
- **Hot-reloadable**: No deployment required for workflow changes
- **Auditable**: Versioned definitions with full history
- **Safe**: Guaranteed termination, sandboxed execution, deterministic replay

**Key insight**: Starlark provides Python syntax familiarity while guaranteeing
the safety properties required for financial workflows.

---

## 2. BIAN Alignment

This capability extends multiple BIAN service domains by externalizing their orchestration logic:

| Service Domain | Current Implementation | With Starlark |
|----------------|----------------------|---------------|
| Payment Order | `payment_orchestrator.go` | `payment_execution.star` |
| Current Account | `withdrawal_orchestrator.go`, `deposit_orchestrator.go` | `withdrawal.star`, `deposit.star` |
| Internal Bank Account | Clearing operations | `clearing_settlement.star` |
| *NEW* Settlement | N/A | `energy_settlement.star`, `asset_settlement.star` |

The saga definitions become **Administrative Plan Records** - auditable configuration that governs workflow execution.

---

## 3. Functional Requirements

### FR-1: Saga Definition Storage

- **Requirement**: Saga definitions MUST be stored in Reference Data service
  with lifecycle management (DRAFT → ACTIVE → DEPRECATED)
- **Pattern**: Follow `InstrumentDefinition` model from ADR-014
- **Constraint**: ACTIVE definitions are immutable; create new version to change

### FR-2: Starlark Runtime Execution

- **Requirement**: The system MUST execute Starlark scripts with guaranteed termination
- **Language**: Starlark (deterministic subset of Python - no while loops, no I/O, no imports)
- **Builtins**: Platform provides `cel_eval()`, `posting()`, `resolve_account()`, step handlers

### FR-3: CEL Integration for Calculations

- **Requirement**: Starlark scripts MUST call CEL expressions for financial calculations
- **Rationale**: CEL provides ~100ns evaluation; Starlark handles orchestration flow
- **Constraint**: Valuation math MUST NOT be implemented directly in Starlark

### FR-4: Tenant Default with Override

- **Requirement**: Platform provides default saga definitions; tenants MAY override
- **Resolution order**: Tenant-specific → Platform default
- **Isolation**: Tenant overrides stored in tenant schema partition

### FR-5: Redis Caching

- **Requirement**: Compiled saga definitions MUST be cached in Redis
- **TTL**: Configurable per-definition, default 1 hour
- **Invalidation**: On definition update, cache key invalidated
- **Fallback**: On cache miss, load from database and populate cache

### FR-6: Step Handler Registry

- **Requirement**: Starlark can only invoke registered step handlers
- **Security**: Handlers are platform-controlled Go functions
- **Extensibility**: New handlers require platform deployment

### FR-7: Simulation Mode

- **Requirement**: DRAFT sagas MUST support execution against historical data
- **Implementation**: Run saga with `knowledge_at` parameter for bi-temporal replay
- **Output**: Simulated positions, postings, and P&L without affecting live data

### FR-8: Execution Audit Trail

- **Requirement**: Every saga execution MUST produce an execution record
- **Contents**: Saga version, input parameters, step results, duration, outcome
- **Retention**: Per tenant retention policy

### FR-9: Reference Validation

- **Requirement**: Saga definitions MUST be validated for reference integrity at multiple lifecycle phases
- **DRAFT phase**: Warn on missing references, allow save
- **ACTIVATION phase**: Error on missing/deprecated references, block activation
- **RUNTIME phase**: Fail fast with actionable error if reference no longer valid

### FR-10: Deprecation Impact Analysis

- **Requirement**: When deprecating instruments, accounts, or sagas, the system MUST report dependent sagas
- **Scope**: Identify all ACTIVE sagas that reference the item being deprecated
- **Action**: Require explicit acknowledgment or block deprecation until dependents updated

### FR-11: Party-Level Data Isolation

- **Requirement**: Saga execution MUST be scoped to the party hierarchy from Party Service
- **Individual party**: Access only own positions, accounts, and data
- **Organization party**: Access own + descendant parties (enables aggregate views)
- **Enforcement**: Runtime resolves party tree; injects immutable `ctx.party_scope`
- **No bypass**: Saga authors cannot access parties outside their scope
- **Audit**: All executions logged with `party_id` for compliance

### FR-12: Saga Composition

- **Requirement**: Sagas MAY invoke other sagas as steps via `invoke_saga()` builtin
- **Compensation**: Child saga compensation cascades automatically on parent failure
- **Scope inheritance**: Child saga inherits parent's party scope (cannot escalate)
- **Circular detection**: Runtime MUST detect and reject circular saga references

### FR-13: Durable Execution via Replay

- **Requirement**: Saga execution MUST survive pod restarts without data loss
- **Checkpointing**: Before executing any side-effect-producing step, persist `SagaInstance` state
- **Recovery**: On pod restart, orphaned sagas MUST be detected and resumed
- **Replay**: Starlark script re-executes from start; completed steps return cached results
- **Idempotency keys**: Format is `saga_{instance_id}_step_{index}` (See Section 5.9 for details)
- **Idempotency**: Step handlers MUST be idempotent (use idempotency keys)

### FR-14: Strict Determinism

- **Requirement**: The Starlark runtime environment MUST be strictly deterministic
- **No time access**: Runtime MUST NOT provide `time.now()` or similar functions
- **Injected time**: All time-related logic MUST use `ctx.effective_at` or `ctx.knowledge_at`
  - `effective_at`: When the business event occurred (transaction date)
  - `knowledge_at`: When we learned about it (for bi-temporal replay)
- **No randomness**: Runtime MUST NOT provide random number generation
- **Handler purity**: Step handlers MUST return results derived solely from inputs and `knowledge_at`

### FR-15: Step Handler Output Contracts

- **Requirement**: Step handlers MUST return Starlark-compatible `Dict` or `Struct` types
- **Schema definition**: Each handler declares its output schema (keys and types)
- **Validation**: Reference validation SHOULD check that scripts access valid output keys
- **Documentation**: Handler schemas are auto-documented for saga authors

### FR-16: Simulation Mode Boundary

- **Requirement**: Step handlers MUST check `ctx.is_simulation` flag
- **Side-effect handlers**: When `is_simulation=true`, handlers for Financial
  Accounting, Payment Gateway, etc. MUST return mock success without executing
  real transactions
- **Read handlers**: May execute normally (data access is safe)
- **Audit**: Simulation executions are logged but marked as non-production

### FR-17: Causation ID Propagation

- **Requirement**: Runtime MUST automatically inject `causation_id` into all step handler calls
- **Compensation linking**: Compensation steps receive the parent step's `causation_id`
- **Audit trail**: All "Do" and "Undo" actions are linked via causation chain
- **Traceability**: Given any entry, trace back to the saga step that created it

### FR-18: Side-Effect Idempotency Enforcement

- **Requirement**: Step Handlers for external integrations MUST explicitly
  declare their idempotency capability
- **Declaration**: Handlers marked `idempotency: "EXTERNAL_SUPPORTED"` or
  `idempotency: "EXTERNAL_NOT_SUPPORTED"`
- **Fail-fast**: If an external service does not support idempotency, the
  Runtime MUST fail-fast during ACTIVATION if that handler is used in a saga
  that lacks a "Pre-Step Check" pattern
- **Pre-Step Check pattern**: Query external system state before executing
  (e.g., check if payment already exists before creating)
- **Standard library**: Runtime provides `verify_external_state(gateway, check_func)`
  builtin for Pre-Step Check enforcement
- **Linter enforcement**: Logic/Physics Linter (SAGA-048) MUST warn if
  `EXTERNAL_NOT_SUPPORTED` handler is called without preceding
  `verify_external_state()` call
- **Rationale**: Replay execution could trigger double payments if external
  gateways don't support our idempotency keys

```python
# Pre-Step Check pattern for non-idempotent gateways
def pay_external(ctx):
    # REQUIRED: verify state before mutation
    if not verify_external_state(payment_gateway, lambda: gateway.check_exists(ctx.ref)):
        payment_gateway.pay(ctx.amount, ctx.ref)
```

### FR-19: Zombie Saga Detection

- **Requirement**: The `saga_instances` table MUST include a `replay_count` column
- **Max Replays**: If a saga exceeds `MAX_REPLAYS` (configurable, default: 5),
  it MUST be transitioned to `FAILED_MANUAL_INTERVENTION` status
- **Alerting**: Zombie detection MUST trigger a high-severity alert (P1) for operator intervention
- **Manual resolution**: Operator can inspect, fix Starlark logic, and reset
  replay_count or mark as permanently failed
- **Rationale**: Prevents infinite "Try → Fail → Replay → Fail" loops from
  consuming resources indefinitely

### FR-20: Starlark Dry-Run Testing

- **Requirement**: The service SHALL provide an `ExecuteDryRun` RPC
- **Behavior**: Runs the Starlark script in a virtual environment where all Step Handlers are mocked
- **Output**: Returns the intended "Execution Plan" (what steps would have been called, in what order, with what parameters)
- **No persistence**: Dry-run MUST NOT persist anything to the database
- **Use case**: Tenants can test their Starlark logic before calling `RegisterDataSet` to deploy to production
- **Validation**: Dry-run also validates attribute references against instrument schema

### FR-21: Deterministic UUID Generator

- **Requirement**: The Runtime MUST provide a `ctx.new_uuid()` builtin
- **Implementation**: Use **Version 5 UUIDs (Namespace UUIDs)** per RFC 4122
  - Namespace: `SagaInstance.ID`
  - Name: `"{StepIndex}:{CallIndex}"` (e.g., `"2:0"` for first call in step 2)
- **Stability**: Same saga instance replaying same step produces identical UUIDs
- **Call tracking**: `CallIndex` increments for each `new_uuid()` call within a step, reset to 0 on next step
- **Rationale**: Random UUIDs would break determinism during replay (different UUID each time)
- **Usage**: Reference numbers, correlation IDs, any generated identifiers within saga logic
- **Industry standard**: Version 5 UUIDs are the standard approach for deterministic UUID generation

### FR-22: Saga Hot-Fixing for Zombie Recovery

- **Requirement**: The Admin API MUST allow an operator to hot-fix a stuck
  `SagaInstance` via definition re-pointing (not instance-level script override)
- **Bi-temporal model**: Hot-fix works WITH the versioning system, not around it:
  1. Deploy fixed saga definition as new version (e.g., v1 → v2)
  2. Update stuck instance's `saga_definition_id` to point to new version
  3. Reset `replay_count` to 0, set status to `PENDING`
  4. On resume: replay respects cached `saga_step_results` (completed steps skip)
  5. Failed step executes with NEW definition logic
- **Audit trail**:
  - `saga_instances.saga_definition_id` reflects actual version used
  - `saga_step_results` timestamps show when each step executed
  - Audit log captures: "Instance X re-pointed from v1 to v2 by operator Y,
    reason: Z"
- **Bi-temporal query**: "What actually happened?" → Steps 0-5 under v1, step 6+ under v2
- **Guard rails**:
  - Hot-fix only available for `FAILED_MANUAL_INTERVENTION` status sagas
  - New definition version must pass ACTIVATION validation
- **Compensation scenario**: If completed steps produced wrong results (not just
  failed step), operator must trigger compensation first, then re-point and
  replay

```text
┌─────────────────────────────────────────────────────────────────────────┐
│  HOT-FIX FLOW (Bi-Temporal Compatible)                                  │
├─────────────────────────────────────────────────────────────────────────┤
│                                                                         │
│  BEFORE:                                                                │
│  saga_definitions:  v1 (bug in step 6)                                  │
│  saga_instances:    instance_123, definition_id=v1, step=6, FAILED      │
│  saga_step_results: steps 0-5 COMPLETED (cached)                        │
│                                                                         │
│  HOT-FIX:                                                               │
│  1. INSERT saga_definitions v2 (with fix)                               │
│  2. UPDATE saga_instances SET definition_id=v2, replay_count=0,         │
│            status='PENDING' WHERE id=instance_123                       │
│  3. INSERT audit_log (operator, reason, old_version, new_version)       │
│                                                                         │
│  ON RESUME:                                                             │
│  - Load definition v2                                                   │
│  - Replay: steps 0-5 → cached results exist → SKIP                      │
│  - Replay: step 6 → no cached result → EXECUTE with v2 logic            │
│                                                                         │
│  BI-TEMPORAL RECORD:                                                    │
│  saga_instances.saga_definition_id = v2                                 │
│  saga_step_results[0-5].executed_at < hot-fix time                      │
│  saga_step_results[6+].executed_at > hot-fix time                       │
│                                                                         │
└─────────────────────────────────────────────────────────────────────────┘
```

---

## 4. CEL Valuation: Context and Boundaries

> **Note**: CEL-based valuation is **out of scope** for this PRD but provides
> essential context. This refactor establishes the foundation that the
> Valuation Engine will build upon.

### 4.1 Composition Model (Not Embedding)

Starlark sagas **call** the Valuation Engine; they do not embed CEL valuation logic:

```text
WRONG: CEL embedded in Starlark
──────────────────────────────
def posting_rules(ctx):
    # Don't do this - valuation logic coupled to saga
    value = cel_eval("qty * 0.35", {"qty": ctx.quantity})


RIGHT: Valuation as service call
────────────────────────────────
def posting_rules(ctx):
    # Saga orchestrates; valuation logic is elsewhere
    valuations = valuation_engine.valuate(
        quantity = ctx.quantity,
        instrument = ctx.instrument,
        contexts = ["RETAIL", "WHOLESALE"],
    )
```

### 4.2 CEL Valuation Use Cases (Future)

CEL valuation rules will be stored separately in Reference Data, not in saga definitions:

| Use Case | CEL Expression (stored in Reference Data) |
|----------|-------------------------------------------|
| **Asset pair conversion** | `qty * lookup_rate(from_instrument, to_instrument, ctx.effective_date)` |
| **Time-of-use pricing** | `qty * lookup_tariff(attrs.tou_period, attrs.zone)` |
| **Vintage-aware carbon** | `qty * lookup_price("VCU", attrs.vintage, attrs.project)` |
| **FX conversion** | `amount * lookup_fx(from_ccy, to_ccy, ctx.knowledge_at)` |

### 4.3 Non-Fungible Asset Totalling

CEL valuation enables totalling non-fungible positions of the same instrument class:

```text
Position Keeping holds (non-fungible due to different attributes):
├── 10 VCU (vintage: 2023, project: ABC)
├── 5 VCU (vintage: 2024, project: ABC)
└── 3 VCU (vintage: 2023, project: XYZ)

CEL valuation applied per-bucket:
├── 10 × $45 (2023 vintage price) = $450
├── 5 × $52 (2024 vintage price)  = $520
└── 3 × $45 (2023 vintage price)  = $135
                                    ─────
                        Total:      $1,105
```

The saga orchestrates the totalling; CEL provides the per-bucket calculation.

### 4.4 Saga ↔ Valuation Integration Point

The Starlark saga will call the Valuation Engine via a step handler:

```python
# In saga definition (Starlark)
step(
    name = "valuate_positions",
    action = "valuation_engine.valuate",  # Step handler
    params = lambda ctx: {
        "positions": ctx.positions,
        "contexts": ["MARKET_VALUE", "COST_BASIS"],
        "knowledge_at": ctx.knowledge_at,
    },
)
```

The `valuation_engine.valuate` step handler will:

1. Load CEL valuation rules from Reference Data
2. Fetch market data from MIM (respecting `knowledge_at`)
3. Evaluate CEL expressions
4. Return `ValuationReceipt` with full lineage

This PRD establishes the runtime; the Valuation Engine PRD will define the CEL rule storage and evaluation.

---

## 5. Technical Architecture

### 5.1 System Context

```text
┌─────────────────────────────────────────────────────────────────────────────┐
│                           Service Layer                                      │
│  ┌─────────────┐  ┌─────────────┐  ┌─────────────┐  ┌─────────────┐        │
│  │  Payment    │  │  Current    │  │  Internal   │  │ Settlement  │        │
│  │   Order     │  │  Account    │  │    Bank     │  │  (NEW)      │        │
│  └──────┬──────┘  └──────┬──────┘  └──────┬──────┘  └──────┬──────┘        │
│         │                │                │                │               │
│         └────────────────┴────────────────┴────────────────┘               │
│                                    │                                        │
│                                    ▼                                        │
│  ┌─────────────────────────────────────────────────────────────────────┐   │
│  │                    Saga Runtime (shared/pkg/saga)                    │   │
│  │  ┌─────────────┐  ┌─────────────┐  ┌─────────────┐                  │   │
│  │  │  Starlark   │  │    CEL      │  │    Step     │                  │   │
│  │  │     VM      │  │  Evaluator  │  │  Registry   │                  │   │
│  │  └─────────────┘  └─────────────┘  └─────────────┘                  │   │
│  └─────────────────────────────────────────────────────────────────────┘   │
└─────────────────────────────────────────────────────────────────────────────┘
                                    │
              ┌─────────────────────┼─────────────────────┐
              ▼                     ▼                     ▼
┌─────────────────────┐  ┌─────────────────────┐  ┌─────────────────────┐
│    Reference Data   │  │       Redis         │  │  External Services  │
│   (Saga Storage)    │  │   (Saga Cache)      │  │  (Step Execution)   │
│                     │  │                     │  │                     │
│  saga_definitions   │  │  saga:{tenant}:{n}  │  │  Position Keeping   │
│                     │  │                     │  │  Fin. Accounting    │
│                     │  │                     │  │  Valuation Engine   │
└─────────────────────┘  └─────────────────────┘  └─────────────────────┘
```

### 5.2 Saga Definition Schema

> **Note**: Tenant isolation is schema-based (per ADR-0016). Tables do not
> include `tenant_id` - isolation is enforced via PostgreSQL search_path.

```sql
CREATE TABLE saga_definitions (
    -- Identity
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),

    -- Identification (mirrors InstrumentDefinition pattern)
    name VARCHAR(64) NOT NULL,           -- "withdrawal", "deposit", "payment_execution"
    version INTEGER NOT NULL DEFAULT 1,

    -- The Starlark script
    script TEXT NOT NULL,

    -- Lifecycle
    status VARCHAR(16) NOT NULL DEFAULT 'DRAFT',
    is_system BOOLEAN NOT NULL DEFAULT FALSE,

    -- CEL preconditions (evaluated before saga starts)
    preconditions_expression TEXT,

    -- Metadata
    display_name VARCHAR(128),
    description TEXT,

    -- Bi-temporal timestamps (when was this version active?)
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    activated_at TIMESTAMPTZ,            -- When this version became ACTIVE
    deprecated_at TIMESTAMPTZ,           -- When this version was deprecated

    -- Successor for deprecation lineage
    successor_id UUID REFERENCES saga_definitions(id),

    -- Constraints
    UNIQUE(name, version),
    CHECK (status IN ('DRAFT', 'ACTIVE', 'DEPRECATED')),
    CHECK (char_length(script) <= 65536),  -- 64KB max
    CHECK (char_length(preconditions_expression) <= 4096)
);

CREATE INDEX idx_saga_definitions_lookup
    ON saga_definitions(name, status);

CREATE INDEX idx_saga_definitions_active
    ON saga_definitions(name)
    WHERE status = 'ACTIVE';

-- Bi-temporal query: What saga was active at a given point in time?
CREATE INDEX idx_saga_definitions_temporal
    ON saga_definitions(name, activated_at, deprecated_at);
```

### 5.3 Redis Caching Strategy

**Cache Key Format:**

```text
saga:compiled:{name}:{version}
```

> **Note**: Cache is per-schema (tenant). Redis key namespace is prefixed by tenant at connection level.

**Cache Entry Structure:**

```json
{
  "definition_id": "uuid",
  "name": "withdrawal",
  "version": 2,
  "compiled_at": "2026-01-20T10:00:00Z",
  "script_hash": "sha256:...",
  "preconditions_cel": "<compiled bytecode reference>",
  "ttl_seconds": 3600
}
```

**Caching Flow:**

```text
Execute Saga Request
        │
        ▼
┌───────────────────┐
│ Check Redis Cache │
└────────┬──────────┘
         │
    ┌────┴────┐
    │ HIT?    │
    └────┬────┘
         │
    YES  │  NO
    ┌────┘  └────┐
    │            │
    ▼            ▼
 Return     ┌──────────────┐
 Cached     │ Load from DB │
            └──────┬───────┘
                   │
                   ▼
            ┌──────────────┐
            │   Compile    │
            │  Starlark    │
            └──────┬───────┘
                   │
                   ▼
            ┌──────────────┐
            │ Store Redis  │
            │  (with TTL)  │
            └──────┬───────┘
                   │
                   ▼
               Return
```

**Cache Invalidation:**

| Event | Action |
|-------|--------|
| Definition updated | Delete `saga:compiled:{name}:*` |
| Definition activated | Delete and repopulate |
| Definition deprecated | Delete from cache |
| TTL expiry | Automatic eviction |

### 5.4 Tenant Default Resolution

> **Note**: Schema-based isolation - query runs within tenant's schema via search_path.

```text
┌─────────────────────────────────────────────────────────────┐
│               Saga Resolution Order                          │
│                                                              │
│  Connection: SET search_path = tenant_schema                │
│                                                              │
│  1. Tenant Override    saga_definitions WHERE               │
│                        name = :saga_name AND                │
│                        status = 'ACTIVE'                    │
│                        AND is_system = FALSE                │
│                        ORDER BY version DESC                │
│                        LIMIT 1                              │
│                                                              │
│  2. Platform Default   saga_definitions WHERE               │
│                        name = :saga_name AND                │
│                        status = 'ACTIVE'                    │
│                        AND is_system = TRUE                 │
│                        ORDER BY version DESC                │
│                        LIMIT 1                              │
│                                                              │
│  3. Not Found          Return error                         │
└─────────────────────────────────────────────────────────────┘
```

Platform-provided sagas use `is_system = true` and are seeded to each tenant's
schema during provisioning (same pattern as system instruments in Reference
Data).

### 5.5 Step Handler Registry

Platform-controlled vocabulary of allowed actions:

```go
type StepHandlerRegistry struct {
    handlers map[string]StepHandler
}

// Platform-provided step handlers
var DefaultHandlers = map[string]StepHandler{
    // Position Keeping
    "position_keeping.initiate_log":    positionKeepingInitiateLog,
    "position_keeping.update_log":      positionKeepingUpdateLog,
    "position_keeping.cancel_log":      positionKeepingCancelLog,

    // Financial Accounting
    "financial_accounting.post_entries":     financialAccountingPostEntries,
    "financial_accounting.reverse_entries":  financialAccountingReverseEntries,
    "financial_accounting.create_booking":   financialAccountingCreateBooking,

    // Current Account
    "current_account.create_lien":      currentAccountCreateLien,
    "current_account.execute_lien":     currentAccountExecuteLien,
    "current_account.terminate_lien":   currentAccountTerminateLien,

    // Valuation Engine
    "valuation_engine.valuate":         valuationEngineValuate,

    // Repository (local persistence)
    "repository.save":                  repositorySave,

    // Notifications
    "notification.send":                notificationSend,
}
```

Starlark scripts can ONLY invoke handlers in this registry. Attempting to call an unregistered handler returns an error.

### 5.6 Reference Validation System

#### Reference Types Tracked

| Reference Type | Example | Source of Truth |
|----------------|---------|-----------------|
| Step handlers | `"position_keeping.initiate_log"` | Step handler registry |
| Instruments | `resolve_instrument("KWH")` | `instrument_definitions` |
| Accounts | `resolve_account("clearing", "GBP")` | Internal Bank Account service |
| Other sagas | `invoke_saga("sub_workflow")` | `saga_definitions` |
| Valuation rules | `valuate("KWH", "GBP", "RETAIL")` | Valuation rules (future) |
| **Attribute keys** | `ctx.position.attributes["gsp_code"]` | Instrument attribute schema |

#### Reference Tracking Schema

```sql
-- Track references for impact analysis and validation
CREATE TABLE saga_references (
    saga_definition_id UUID NOT NULL REFERENCES saga_definitions(id) ON DELETE CASCADE,
    reference_type VARCHAR(32) NOT NULL,
    reference_key VARCHAR(128) NOT NULL,
    line_number INTEGER,
    extracted_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),

    PRIMARY KEY (saga_definition_id, reference_type, reference_key)
);

-- Query: What sagas reference this instrument?
CREATE INDEX idx_saga_references_by_target
    ON saga_references(reference_type, reference_key);

-- Query: What does this saga reference?
CREATE INDEX idx_saga_references_by_saga
    ON saga_references(saga_definition_id);
```

#### Validation Phases

```text
┌─────────────────────────────────────────────────────────────────────────────┐
│                             DRAFT Phase                                      │
│                                                                              │
│  Trigger: CreateDraft(), UpdateDefinition()                                 │
│                                                                              │
│  Actions:                                                                    │
│    1. Parse Starlark script                                                 │
│    2. Extract all references (step handlers, instruments, accounts, etc.)   │
│    3. Validate each reference exists                                        │
│    4. Store warnings for missing/deprecated references                      │
│    5. Populate saga_references table                                        │
│                                                                              │
│  Outcome: Save succeeds with warnings; activation blocked if errors         │
└─────────────────────────────────────────────────────────────────────────────┘
                                     │
                                     ▼
┌─────────────────────────────────────────────────────────────────────────────┐
│                          ACTIVATION Phase                                    │
│                                                                              │
│  Trigger: ActivateSaga()                                                    │
│                                                                              │
│  Actions:                                                                    │
│    1. Re-validate ALL references (state may have changed since DRAFT)       │
│    2. Check step handlers exist in registry                                 │
│    3. Check instruments are ACTIVE (not DRAFT or DEPRECATED)                │
│    4. Check accounts exist and are active                                   │
│    5. Check referenced sagas are ACTIVE                                     │
│                                                                              │
│  Outcome: Hard failure if any reference invalid; activation blocked         │
└─────────────────────────────────────────────────────────────────────────────┘
                                     │
                                     ▼
┌─────────────────────────────────────────────────────────────────────────────┐
│                            RUNTIME Phase                                     │
│                                                                              │
│  Trigger: Execute()                                                          │
│                                                                              │
│  Actions:                                                                    │
│    1. Load saga (should be cached and pre-validated)                        │
│    2. On each step, verify handler still registered                         │
│    3. On instrument/account resolution, verify still valid                  │
│                                                                              │
│  Outcome: Fail fast with actionable error message                           │
│           Include: what's missing, where in script, suggested fix           │
└─────────────────────────────────────────────────────────────────────────────┘
```

#### Validation Feedback Format

```text
Saga: withdrawal.star (DRAFT)
Tenant: acme-corp

Validation Results:
═══════════════════

Starlark Syntax
  ✓ Script parses successfully
  ✓ 3 steps defined
  ✓ All steps have compensation defined

Step Handlers
  ✓ position_keeping.initiate_log .......... exists
  ✓ financial_accounting.post_entries ...... exists
  ✗ notification.send_sms .................. NOT FOUND
    └─ Available alternatives: notification.send, notification.send_email

Instrument References
  ✓ GBP .................................. ACTIVE (v1)
  ⚠ KWH .................................. DEPRECATED
    └─ Successor available: KWH-V2 (ACTIVE)
    └─ Consider updating before activation

Account References
  ✓ clearing/GBP ......................... exists
  ✓ tax_withholding/GBP .................. exists

──────────────────────────────────────────────────
Status: BLOCKED - Cannot activate
        1 error, 1 warning

Errors (must fix):
  • Step handler 'notification.send_sms' not found (line 47)

Warnings (recommended):
  • Instrument 'KWH' is deprecated; successor 'KWH-V2' available
```

#### Deprecation Cascade Detection

When deprecating an instrument, account, or saga, check dependencies:

```text
Request: Deprecate instrument KWH (version 1)

Impact Analysis:
════════════════

Active sagas referencing KWH:1
──────────────────────────────
  • energy_settlement.star v2 (tenant: ACME_ENERGY)
      └─ Line 23: resolve_instrument("KWH")
      └─ Line 45: valuation_engine.valuate(instrument="KWH", ...)

  • meter_reconciliation.star v1 (tenant: ACME_ENERGY)
      └─ Line 12: ctx.instrument == "KWH"

  • wholesale_settlement.star v3 (SYSTEM)
      └─ Line 67: position_keeping.initiate_log(instrument="KWH", ...)

Summary: 3 active sagas across 2 tenants

Options:
  [1] Block deprecation until sagas updated
  [2] Force deprecate (sagas will fail at runtime with clear error)
  [3] Specify successor KWH-V2 (sagas warned but continue working)

Recommendation: Option [3] - deprecate with successor
                Sagas will log warnings; runtime continues
                Tenants notified to update their definitions
```

#### Attribute Schema Validation

Sagas that access `ctx.position.attributes["key"]` must be validated against
the instrument definition's attribute schema. This is **bidirectional**:

##### Saga Activation → Instrument Schema

When activating a saga, extract attribute key accesses and validate against
the instrument definition:

```text
Saga: energy_settlement.star (DRAFT → ACTIVE)

Attribute References Extracted:
════════════════════════════════
  • Line 23: ctx.position.attributes["gsp_code"]
  • Line 24: ctx.position.attributes["dno_code"]
  • Line 31: ctx.position.attributes["settlement_period"]

Validating against instrument: KWH
──────────────────────────────────
  ✓ gsp_code ......................... defined (type: string)
  ✓ dno_code ......................... defined (type: string)
  ✗ settlement_period ................ NOT DEFINED
    └─ Available attributes: gsp_code, dno_code, meter_type, profile_class
    └─ Did you mean: 'settlement_date'?

Status: BLOCKED - Cannot activate
        Saga references attribute 'settlement_period' not defined in instrument KWH
```

##### Instrument Update → Saga Dependencies

When modifying an instrument's attribute schema, check for dependent sagas:

```text
Request: Remove attribute 'gsp_code' from instrument KWH

Impact Analysis:
════════════════

Active sagas referencing KWH.attributes["gsp_code"]:
────────────────────────────────────────────────────
  • energy_settlement.star v2 (tenant: ACME_ENERGY)
      └─ Line 23: ctx.position.attributes["gsp_code"]

  • wholesale_reconciliation.star v1 (SYSTEM)
      └─ Line 45: ctx.position.attributes["gsp_code"]

Summary: 2 active sagas depend on this attribute

Options:
  [1] Block removal until sagas updated
  [2] Force remove (sagas will fail at runtime)

Recommendation: Option [1] - update sagas first
```

##### Reference Tracking Schema Extension

```sql
-- Extended to track attribute references
CREATE TABLE saga_references (
    saga_definition_id UUID NOT NULL REFERENCES saga_definitions(id) ON DELETE CASCADE,
    reference_type VARCHAR(32) NOT NULL,
    reference_key VARCHAR(128) NOT NULL,
    -- NEW: For attribute references, track the instrument
    instrument_code VARCHAR(32),          -- Which instrument's attributes
    attribute_key VARCHAR(64),            -- Which attribute key
    line_number INTEGER,
    extracted_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),

    PRIMARY KEY (saga_definition_id, reference_type, reference_key)
);

-- Query: What sagas use this instrument's attribute?
CREATE INDEX idx_saga_references_attribute
    ON saga_references(instrument_code, attribute_key)
    WHERE reference_type = 'attribute';
```

##### Validation at Each Phase

| Phase | Attribute Validation | Outcome |
|-------|---------------------|---------|
| **DRAFT** | Extract attribute accesses, warn if not in schema | Save succeeds with warnings |
| **ACTIVATION** | Re-validate all attribute refs against current schema | Hard fail if missing |
| **INSTRUMENT UPDATE** | Check if attribute removal breaks active sagas | Block or warn |
| **RUNTIME** | Verify attribute exists before access | Fail fast with actionable error |

### 5.7 Party Hierarchy and Data Isolation

Saga execution is scoped to the party hierarchy from the Party Service. This
ensures tenants cannot accidentally or intentionally access data belonging to
other parties.

#### Party Scope Resolution

```text
┌─────────────────────────────────────────────────────────────────────────────┐
│                        Party Hierarchy Example                               │
│                                                                              │
│                      ┌─────────────────────┐                                │
│                      │  ACME Corp (ORG)    │                                │
│                      │  party_id: P001     │                                │
│                      └──────────┬──────────┘                                │
│                                 │                                           │
│              ┌──────────────────┼──────────────────┐                       │
│              │                  │                  │                        │
│     ┌────────▼────────┐ ┌──────▼───────┐ ┌───────▼───────┐                │
│     │ ACME UK (ORG)   │ │ ACME DE (ORG)│ │ ACME FR (ORG) │                │
│     │ party_id: P002  │ │ party_id:P003│ │ party_id: P004│                │
│     └────────┬────────┘ └──────────────┘ └───────────────┘                │
│              │                                                              │
│     ┌────────┴────────┐                                                    │
│     │                 │                                                    │
│┌────▼─────┐  ┌───────▼────────┐                                           │
││ John Doe │  │ Jane Smith     │                                           │
││(INDIV)   │  │ (INDIV)        │                                           │
││P005      │  │ P006           │                                           │
│└──────────┘  └────────────────┘                                           │
└─────────────────────────────────────────────────────────────────────────────┘
```

#### Scope Rules by Party Type

| Party Type | Visible Data |
|------------|--------------|
| **Individual** | Own positions, accounts, transactions only |
| **Organization** | Own + all descendant parties (recursive) |
| **System** | All parties within tenant (admin use only) |

#### Runtime Context Injection

The saga runtime resolves the party tree and injects an immutable `ctx.party_scope`:

```python
# Runtime injects this before saga execution
ctx.party_scope = PartyScope(
    party_id = "P002",          # Executing party
    party_type = "ORGANIZATION",
    visible_parties = ["P002", "P005", "P006"],  # Self + descendants
    tenant_id = "ACME_TENANT",
)

# Saga cannot modify or bypass:
ctx.party_scope.visible_parties.append("P003")  # Error: immutable
del ctx.party_scope  # Error: cannot delete

# All data access filters automatically:
positions = position_keeping.list(party_scope=ctx.party_scope)
# SQL: WHERE party_id IN ('P002', 'P005', 'P006')
```

#### Step Handler Enforcement

Every step handler that accesses data MUST respect party scope:

```go
// Step handler implementation
func positionKeepingList(ctx StarlarkContext, params map[string]any) (any, error) {
    // Runtime ALWAYS passes party_scope - handlers cannot opt out
    partyScope := ctx.PartyScope

    // Query builder enforces scope
    positions, err := r.db.WithContext(ctx).
        Where("party_id IN ?", partyScope.VisibleParties).
        Find(&positions).Error

    return positions, err
}
```

#### Cross-Party Aggregate Views

Organization parties can run sagas that aggregate across their hierarchy:

```python
# aggregate_positions.star - Only valid for ORG party types
def aggregate_by_instrument(ctx):
    """Sum positions across all subsidiaries."""
    if ctx.party_scope.party_type != "ORGANIZATION":
        fail("aggregate sagas require ORGANIZATION party type")

    totals = {}
    for party_id in ctx.party_scope.visible_parties:
        positions = position_keeping.list(party_id=party_id)
        for pos in positions:
            key = pos.instrument_code
            totals[key] = totals.get(key, 0) + pos.quantity

    return totals

saga(
    name = "aggregate_positions",
    version = "1.0.0",
    preconditions = [
        "ctx.party_scope.party_type == 'ORGANIZATION'",
    ],
    steps = [
        step(
            name = "compute_aggregates",
            action = "valuation_engine.valuate",
            params = lambda ctx: {
                "positions": aggregate_by_instrument(ctx),
            },
        ),
    ],
)
```

#### Cross-Party Posting Authorization

**Key distinction**: Read isolation ≠ Write authorization.

A saga executing under party A may create ledger entries affecting party B
when authorized by relationship:

```text
┌─────────────────────────────────────────────────────────────────────────────┐
│                    Cross-Party Posting Examples                             │
├─────────────────────────────────────────────────────────────────────────────┤
│                                                                             │
│  ENERGY SETTLEMENT                                                          │
│  ─────────────────                                                          │
│  Market Operator (party A) settles trade:                                   │
│    • Generator (party B) sells 100 MWh                                      │
│    • Retailer (party C) buys 100 MWh                                        │
│                                                                             │
│  Saga executes as: Market Operator (A)                                      │
│  Postings created:                                                          │
│    DEBIT  Generator (B)  position: -100 MWh                                │
│    CREDIT Retailer (C)   position: +100 MWh                                │
│    DEBIT  Retailer (C)   cash: -$5,000                                     │
│    CREDIT Generator (B)  cash: +$5,000                                     │
│                                                                             │
├─────────────────────────────────────────────────────────────────────────────┤
│                                                                             │
│  WEALTH MANAGEMENT                                                          │
│  ─────────────────                                                          │
│  Custodian (party A) executes client transfer:                             │
│    • Client 1 (party B) transfers $10,000                                  │
│    • Client 2 (party C) receives $10,000                                   │
│                                                                             │
│  Saga executes as: Custodian (A)                                           │
│  Postings created:                                                          │
│    DEBIT  Client 1 (B)  cash: -$10,000                                     │
│    CREDIT Client 2 (C)  cash: +$10,000                                     │
│                                                                             │
└─────────────────────────────────────────────────────────────────────────────┘
```

#### Read vs Write Authorization Model

| Operation | Scope Rule |
|-----------|------------|
| **READ positions** | Party hierarchy only (self + descendants) |
| **READ accounts** | Party hierarchy only |
| **READ transactions** | Party hierarchy only |
| **WRITE postings** | Contextual lookup - saga resolves target from input data |
| **WRITE positions** | Contextual lookup - saga resolves target from input data |

#### Cross-Party Authorization: Contextual Lookup Model

> **Note**: The Party Service currently has `party_association` for personal
> relationships (SPOUSE, DEPENDENT, GUARANTOR). Operational authorization
> (OPERATOR, CUSTODIAN, BROKER) is **not yet implemented**.

Rather than rigid party-to-party relationship tables, authorization flows from
**contextual lookup** using the position's flexible attributes:

```text
┌─────────────────────────────────────────────────────────────────────────────┐
│                    Position Attributes → Account Resolution                  │
├─────────────────────────────────────────────────────────────────────────────┤
│                                                                             │
│  Position (from Position Keeping) with tenant-defined attributes:           │
│    {                                                                        │
│      "id": "pos-123",                                                       │
│      "party_id": "P-CUST-001",                                             │
│      "instrument_code": "KWH",                                              │
│      "quantity": 100,                                                       │
│      "attributes": {                   # Tenant-defined, flexible           │
│        "customer_party_id": "P-CUST-001",                                  │
│        "gsp_code": "P",                # Energy tenant uses this           │
│        "settlement_period": "2026-01-15/HH23",                             │
│      }                                                                      │
│    }                                                                        │
│                                                                             │
│  Saga Context Resolution (using position attributes):                       │
│    1. ctx.position.party_id → current_account.by_party()                   │
│       Result: Customer's current account                                    │
│                                                                             │
│    2. ctx.position.attributes → internal_bank_account.by_attributes()      │
│       Result: Account matching those attributes                             │
│                                                                             │
│  Authorization is IMPLICIT:                                                 │
│    - Saga declares which lookup types it may use                           │
│    - Runtime validates lookups against declaration                          │
│    - Posting targets come from resolved lookups, not arbitrary IDs         │
│                                                                             │
└─────────────────────────────────────────────────────────────────────────────┘
```

#### Saga Authorized Lookups

Each saga declares what account resolution patterns it may use. Lookups are
**generic** - attribute keys are tenant-defined:

```python
# settlement.star - works for any tenant's attribute schema
saga(
    name = "settlement",
    version = "1.0.0",
    authorized_lookups = [
        "current_account.by_party",           # Can resolve party → account
        "internal_bank_account.by_attributes", # Can resolve attributes → account
    ],
    steps = [
        step(
            name = "post_entries",
            action = "financial_accounting.post_entries",
            params = lambda ctx: {
                "postings": [
                    posting(
                        # Resolve customer account from position's party
                        account_id = current_account.by_party(ctx.position.party_id),
                        direction = "CREDIT",
                        amount = ctx.position.quantity * ctx.rate,
                    ),
                    posting(
                        # Resolve internal account from position's attributes
                        # Attributes are tenant-defined (gsp_code, region, etc.)
                        account_id = internal_bank_account.by_attributes(
                            ctx.position.attributes
                        ),
                        direction = "DEBIT",
                        amount = ctx.position.quantity * ctx.wholesale_rate,
                    ),
                ],
            },
        ),
    ],
)
```

#### Runtime Lookup Enforcement

```go
// Runtime validates lookups against saga's authorized_lookups
func (r *Runtime) ResolveLookup(sagaDef SagaDefinition, lookupType string, key any) (Account, error) {
    // Check if saga is authorized for this lookup type
    if !contains(sagaDef.AuthorizedLookups, lookupType) {
        return nil, fmt.Errorf("saga %s not authorized for lookup type %s", sagaDef.Name, lookupType)
    }

    // Perform the lookup - attribute keys are tenant-defined
    switch lookupType {
    case "current_account.by_party":
        return r.currentAccountClient.GetByParty(ctx, key.(uuid.UUID))
    case "internal_bank_account.by_attributes":
        // Generic lookup - matches against attributes JSONB
        attrs := key.(map[string]any)
        return r.internalBankClient.GetByAttributes(ctx, attrs)
    }
}

// Internal Bank Account: generic attribute matching
func (s *InternalBankAccountService) GetByAttributes(ctx context.Context, attrs map[string]any) (*Account, error) {
    query := s.db.Model(&InternalBankAccount{})
    // Build query dynamically from whatever attributes are passed
    for k, v := range attrs {
        query = query.Where("attributes @> ?", map[string]any{k: v})
    }
    var account InternalBankAccount
    return &account, query.First(&account).Error
}
```

#### Optional: Explicit Party Relationships

For use cases requiring explicit authorization tracking (audit, compliance), an
optional `party_relationships` table can be added to Party Service:

```sql
-- PROPOSED: Not currently in Party Service
-- Only needed if explicit relationship auditing required
CREATE TABLE party_relationships (
    id UUID PRIMARY KEY,
    source_party_id UUID NOT NULL REFERENCES party(id),
    target_party_id UUID NOT NULL REFERENCES party(id),
    relationship_type VARCHAR(32) NOT NULL,  -- OPERATOR, CUSTODIAN, BROKER
    permissions JSONB NOT NULL DEFAULT '{}', -- {"can_post": true, "can_settle": true}
    valid_from TIMESTAMPTZ NOT NULL,
    valid_to TIMESTAMPTZ,

    UNIQUE(source_party_id, target_party_id, relationship_type)
);
```

This is **optional** - the contextual lookup model provides authorization implicitly through saga definition.

#### Audit Trail

All saga executions are logged with party context and bi-temporal references:

```sql
CREATE TABLE saga_execution_log (
    id UUID PRIMARY KEY,

    -- Saga reference (bi-temporal: which version was active when?)
    saga_definition_id UUID NOT NULL REFERENCES saga_definitions(id),
    saga_name VARCHAR(64) NOT NULL,       -- Denormalized for query
    saga_version INTEGER NOT NULL,        -- Version that was executed

    -- Party context
    party_id UUID NOT NULL,               -- Executing party
    party_type VARCHAR(16) NOT NULL,      -- INDIVIDUAL, ORGANIZATION, SYSTEM
    visible_parties UUID[] NOT NULL,      -- Parties data was accessed for

    -- Bi-temporal timestamps
    started_at TIMESTAMPTZ NOT NULL,
    completed_at TIMESTAMPTZ,
    knowledge_at TIMESTAMPTZ,             -- For replay: what time context was used

    -- Execution result
    status VARCHAR(16) NOT NULL,          -- RUNNING, COMPLETED, COMPENSATED, FAILED
    input_hash VARCHAR(64),               -- SHA256 of input for replay verification
    output_snapshot JSONB,                -- Result for audit

    -- Error context
    error_message TEXT,
    failed_step VARCHAR(64)
);

-- Query: What sagas touched party P005's data?
CREATE INDEX idx_saga_execution_visible_parties
    ON saga_execution_log USING GIN(visible_parties);

-- Query: What saga version was used for this execution?
CREATE INDEX idx_saga_execution_temporal
    ON saga_execution_log(saga_name, started_at);
```

### 5.8 Bi-Temporal Saga Replay

Audit and compliance require answering: "What saga was used 3 months ago to derive this value?"

#### Temporal Query Pattern

```sql
-- What saga version was ACTIVE on 2025-10-15?
SELECT * FROM saga_definitions
WHERE name = 'energy_settlement'
  AND activated_at <= '2025-10-15'
  AND (deprecated_at IS NULL OR deprecated_at > '2025-10-15')
ORDER BY version DESC
LIMIT 1;

-- What saga was used to produce execution X?
SELECT sd.*
FROM saga_execution_log sel
JOIN saga_definitions sd ON sd.id = sel.saga_definition_id
WHERE sel.id = :execution_id;
```

#### Replay with Historical Saga

```python
# Replay execution with the EXACT saga version that was active then
def replay_execution(execution_id: UUID, knowledge_at: datetime) -> SagaResult:
    # Get original execution
    original = saga_execution_log.get(execution_id)

    # Load the saga version that was used (not current ACTIVE)
    saga_def = saga_definitions.get(original.saga_definition_id)

    # Replay with same inputs and knowledge_at
    return runtime.execute(
        saga_definition = saga_def,
        inputs = original.input_snapshot,
        knowledge_at = knowledge_at,  # Bi-temporal: what we knew then
        mode = "REPLAY",              # No side effects, just compute
    )
```

#### Saga Version Lineage

```text
┌─────────────────────────────────────────────────────────────────────────────┐
│                    Saga Version Timeline                                     │
├─────────────────────────────────────────────────────────────────────────────┤
│                                                                             │
│  energy_settlement v1.0                                                     │
│  ├─ activated_at: 2025-01-01                                               │
│  ├─ deprecated_at: 2025-06-15                                              │
│  └─ successor_id: → v2.0                                                   │
│                                                                             │
│  energy_settlement v2.0                                                     │
│  ├─ activated_at: 2025-06-15                                               │
│  ├─ deprecated_at: 2025-11-01                                              │
│  └─ successor_id: → v3.0                                                   │
│                                                                             │
│  energy_settlement v3.0                                                     │
│  ├─ activated_at: 2025-11-01                                               │
│  ├─ deprecated_at: NULL (current)                                          │
│  └─ successor_id: NULL                                                     │
│                                                                             │
│  Query: "What saga was active on 2025-08-20?"                              │
│  Answer: energy_settlement v2.0                                            │
│                                                                             │
└─────────────────────────────────────────────────────────────────────────────┘
```

#### Replay Verification

To verify historical calculations haven't drifted:

```python
# Verify: does replaying with original saga produce same result?
def verify_execution(execution_id: UUID) -> VerificationResult:
    original = saga_execution_log.get(execution_id)

    replayed = replay_execution(
        execution_id = execution_id,
        knowledge_at = original.knowledge_at,
    )

    return VerificationResult(
        original_hash = original.output_hash,
        replayed_hash = hash(replayed.output),
        matches = original.output_hash == hash(replayed.output),
        drift_details = diff(original.output_snapshot, replayed.output) if not matches else None,
    )
```

### 5.9 Durable Execution (Pod Survival)

Saga execution must survive pod restarts. This is achieved through **Replay** -
re-executing the Starlark script while returning cached results for completed
steps.

#### Ownership Model

| Component | Location | Rationale |
|-----------|----------|-----------|
| `saga_definitions` | Reference Data (shared) | Definitions are tenant config, cached globally |
| `saga_instances` | Each service's schema | Execution state is service-local (like audit log) |
| `saga_step_results` | Each service's schema | Step results are service-local |

> **Pattern**: Common schema definition, service-local tables. Each service
> (Payment Order, Current Account, etc.) has its own saga execution state.

#### Service-Local Execution State Schema

```sql
-- Each service has these tables in its schema (common pattern, local data)

CREATE TABLE saga_instances (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),

    -- Saga definition reference
    saga_definition_id UUID NOT NULL,     -- References saga_definitions (cross-service)
    saga_name VARCHAR(64) NOT NULL,       -- Denormalized for query
    saga_version INTEGER NOT NULL,

    -- Input and context (for replay)
    input_snapshot JSONB NOT NULL,
    party_id UUID NOT NULL,
    knowledge_at TIMESTAMPTZ,             -- Bi-temporal context

    -- Ownership (race condition prevention)
    claimed_by_pod VARCHAR(128),          -- e.g., "payment-order-5d4f8c-xyz"
    claimed_at TIMESTAMPTZ,
    lease_expires_at TIMESTAMPTZ,         -- claimed_at + lease_duration (default 5 min)

    -- Progress
    current_step_index INTEGER NOT NULL DEFAULT 0,
    replay_count INTEGER NOT NULL DEFAULT 0,   -- Incremented on each replay attempt
    status VARCHAR(32) NOT NULL DEFAULT 'PENDING',
    -- PENDING, RUNNING, COMPLETED, COMPENSATING, COMPENSATED, FAILED, FAILED_MANUAL_INTERVENTION

    -- Timestamps
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    started_at TIMESTAMPTZ,
    completed_at TIMESTAMPTZ,

    -- Error context
    error_message TEXT,
    failed_step_index INTEGER
);

CREATE INDEX idx_saga_instances_orphaned
    ON saga_instances(status, lease_expires_at)
    WHERE status IN ('PENDING', 'RUNNING', 'COMPENSATING');

CREATE TABLE saga_step_results (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    saga_instance_id UUID NOT NULL REFERENCES saga_instances(id) ON DELETE CASCADE,
    step_index INTEGER NOT NULL,
    step_name VARCHAR(64) NOT NULL,

    -- Idempotency (critical for replay safety)
    idempotency_key VARCHAR(128) NOT NULL,

    -- Result (for replay - skip re-execution)
    output_snapshot JSONB,
    status VARCHAR(16) NOT NULL,          -- COMPLETED, FAILED
    executed_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),

    -- Causation linkage
    causation_id UUID NOT NULL,

    UNIQUE(saga_instance_id, step_index),
    UNIQUE(idempotency_key)
);
```

#### Lease-Based Claiming (Race Condition Prevention)

When multiple pods exist, only one should process a given saga:

```go
// Pod startup or worker loop: claim orphaned sagas
func (w *SagaWorker) claimOrphanedSagas(ctx context.Context) ([]uuid.UUID, error) {
    // SELECT FOR UPDATE SKIP LOCKED prevents race conditions
    rows, err := w.db.QueryContext(ctx, `
        UPDATE saga_instances
        SET claimed_by_pod = $1,
            claimed_at = NOW(),
            lease_expires_at = NOW() + INTERVAL '5 minutes'
        WHERE id IN (
            SELECT id FROM saga_instances
            WHERE status IN ('PENDING', 'RUNNING', 'COMPENSATING')
              AND (
                  lease_expires_at < NOW()           -- Lease expired (pod died)
                  OR claimed_by_pod IS NULL          -- Never claimed
              )
            FOR UPDATE SKIP LOCKED                   -- Prevent race with other pods
            LIMIT 10                                 -- Batch size
        )
        RETURNING id
    `, w.podID)
    // ... return claimed instance IDs
}
```

#### Replay Execution Flow

```text
┌─────────────────────────────────────────────────────────────────────────────┐
│                    Durable Execution via Replay                              │
├─────────────────────────────────────────────────────────────────────────────┤
│                                                                             │
│  NORMAL EXECUTION (Pod A):                                                  │
│  ─────────────────────────                                                  │
│  1. INSERT saga_instances (claimed_by="pod-A", lease_expires=NOW()+5m)     │
│  2. Execute Step 0:                                                         │
│     a. Generate idempotency_key = "saga_{id}_step_0"                       │
│     b. Call step handler                                                    │
│     c. INSERT saga_step_results (output, causation_id)                     │
│     d. UPDATE saga_instances SET current_step_index = 1                    │
│  3. Renew lease (background goroutine, every 2 minutes)                    │
│  4. Execute Step 1 → same pattern                                          │
│  5. Pod A dies mid-Step 2 (after handler call, before result save)         │
│                                                                             │
│  RECOVERY (Pod B picks up orphaned saga):                                  │
│  ─────────────────────────────────────────                                 │
│  1. SELECT orphaned sagas WHERE lease_expires < NOW()                      │
│  2. UPDATE ... SET claimed_by="pod-B" ... FOR UPDATE SKIP LOCKED          │
│  3. Load saga_definition (from Reference Data, cached)                     │
│  4. Load saga_step_results for this instance                               │
│  5. REPLAY Starlark script from start:                                     │
│     Step 0: Check saga_step_results → EXISTS → return cached output        │
│     Step 1: Check saga_step_results → EXISTS → return cached output        │
│     Step 2: Check saga_step_results → NOT FOUND → execute handler          │
│             Handler uses idempotency_key → downstream service says         │
│             "already processed" → return existing result                   │
│  6. Continue normally from Step 3                                          │
│                                                                             │
└─────────────────────────────────────────────────────────────────────────────┘
```

#### Step Handler Wrapper (Replay-Safe)

```go
// Runtime wraps all step handler calls for replay safety
func (r *Runtime) executeStep(
    ctx context.Context, instance *SagaInstance, stepIndex int, handler StepHandler,
) (any, error) {
    // Generate deterministic idempotency key
    idempotencyKey := fmt.Sprintf("saga_%s_step_%d", instance.ID, stepIndex)

    // Check if step already completed (replay case)
    existing, err := r.stepResultRepo.GetByIdempotencyKey(ctx, idempotencyKey)
    if err == nil && existing != nil {
        log.Info("Replaying step - returning cached result",
            "saga_id", instance.ID, "step", stepIndex)
        return existing.OutputSnapshot, nil
    }

    // Not yet executed - call the handler
    output, err := handler.Execute(ctx, StepContext{
        IdempotencyKey: idempotencyKey,
        CausationID:    uuid.New(),
        IsSimulation:   instance.IsSimulation,
        KnowledgeAt:    instance.KnowledgeAt,
    })
    if err != nil {
        return nil, err
    }

    // CRITICAL: Transaction Affinity - result save and index update MUST be atomic
    tx, err := r.db.BeginTx(ctx, nil)
    if err != nil {
        return nil, fmt.Errorf("failed to begin transaction: %w", err)
    }
    defer tx.Rollback()

    // 1. Persist step result
    err = r.stepResultRepo.SaveTx(tx, &SagaStepResult{
        SagaInstanceID:  instance.ID,
        StepIndex:       stepIndex,
        IdempotencyKey:  idempotencyKey,
        OutputSnapshot:  output,
        Status:          "COMPLETED",
        CausationID:     causationID,
    })
    if err != nil {
        return nil, fmt.Errorf("failed to persist step result: %w", err)
    }

    // 2. Update current_step_index in SAME transaction
    _, err = tx.ExecContext(ctx, `
        UPDATE saga_instances
        SET current_step_index = $1, replay_count = 0
        WHERE id = $2
    `, stepIndex+1, instance.ID)
    if err != nil {
        return nil, fmt.Errorf("failed to update step index: %w", err)
    }

    if err := tx.Commit(); err != nil {
        return nil, fmt.Errorf("failed to commit step: %w", err)
    }

    return output, nil
}
```

> **Implementation Directive: Transaction Affinity**
>
> The update to `current_step_index` and the insertion into `saga_step_results`
> MUST happen in the same database transaction. This prevents the "Gap of
> Uncertainty" where:
>
> - A result is saved but the index isn't moved (step re-executes on recovery,
>   but idempotency key catches it)
> - Or the index is moved but result isn't saved (step is skipped on recovery,
>   losing data)
>
> By making these atomic, we guarantee that saga state is always consistent.
> The `replay_count` is also reset to 0 on successful step completion.
>
> **Database Requirement**: PostgreSQL fully supports transactional DDL and
> multi-statement transactions within a single `BEGIN`/`COMMIT` block. This is
> the "Gold Standard" for preventing "Ghost Steps" where partial state persists.

#### Lease Renewal (Keep-Alive)

```go
// While processing, keep renewing the lease to prevent other pods from claiming
func (w *SagaWorker) renewLease(ctx context.Context, instanceID uuid.UUID) {
    ticker := time.NewTicker(2 * time.Minute)  // Renew well before 5min expiry
    defer ticker.Stop()

    for {
        select {
        case <-ctx.Done():
            return
        case <-ticker.C:
            _, err := w.db.ExecContext(ctx, `
                UPDATE saga_instances
                SET lease_expires_at = NOW() + INTERVAL '5 minutes'
                WHERE id = $1 AND claimed_by_pod = $2
            `, instanceID, w.podID)
            if err != nil {
                log.Warn("Failed to renew saga lease", "instance_id", instanceID, "error", err)
            }
        }
    }
}
```

### 5.10 Starlark Builtins

Functions available within Starlark scripts:

| Builtin | Signature | Purpose |
|---------|-----------|---------|
| `cel_eval` | `cel_eval(expr, context) → value` | Evaluate CEL expression |
| `posting` | `posting(account_id, direction, amount, description) → Posting` | Create posting instruction |
| `resolve_account` | `resolve_account(purpose, currency) → account_id` | Lookup internal bank account |
| `step` | `step(name, action, params, compensation) → Step` | Define saga step |
| `saga` | `saga(name, version, steps, preconditions) → SagaDefinition` | Define saga |

### 5.11 Example Saga Definition

```python
# withdrawal.star
# A safe subset of Python for defining withdrawal workflows

def posting_rules(ctx):
    """Generate ledger postings based on tenant configuration."""
    postings = []
    net_amount = ctx.amount

    # Tenant-specific: Withholding tax
    if ctx.tenant.requires_withholding_tax:
        tax = cel_eval(ctx.tenant.withholding_tax_expr, {
            "amount": ctx.amount,
        })
        if tax > 0:
            postings.append(posting(
                account_id = resolve_account("tax_withholding", ctx.currency),
                direction = "CREDIT",
                amount = tax,
                description = "Withholding tax",
            ))
            net_amount = net_amount - tax

    # Tenant-specific: Transaction fee
    if ctx.tenant.transaction_fee_expr:
        fee = cel_eval(ctx.tenant.transaction_fee_expr, {
            "amount": ctx.amount,
            "tier": ctx.account.tier,
        })
        if fee > 0:
            postings.append(posting(
                account_id = resolve_account("fee_income", ctx.currency),
                direction = "CREDIT",
                amount = fee,
                description = "Transaction fee",
            ))
            net_amount = net_amount - fee

    # Always: Customer debit (net amount)
    postings.append(posting(
        account_id = ctx.account.id,
        direction = "DEBIT",
        amount = net_amount,
        description = "Withdrawal",
    ))

    # Always: Clearing credit (full amount)
    postings.append(posting(
        account_id = resolve_account("clearing", ctx.currency),
        direction = "CREDIT",
        amount = ctx.amount,
        description = "Clearing",
    ))

    return postings


def compensation_rules(ctx, completed_postings):
    """Generate reversing entries for compensation."""
    return [
        posting(
            account_id = p.account_id,
            direction = "CREDIT" if p.direction == "DEBIT" else "DEBIT",
            amount = p.amount,
            description = "REVERSAL: " + p.description,
        )
        for p in completed_postings
    ]


# Saga definition
saga(
    name = "withdrawal",
    version = "2.0.0",

    steps = [
        step(
            name = "log_position",
            action = "position_keeping.initiate_log",
            params = lambda ctx: {
                "account_id": ctx.account.id,
                "direction": "DEBIT",
                "amount": ctx.amount,
            },
            compensation = "position_keeping.cancel_log",
        ),
        step(
            name = "post_ledger",
            action = "financial_accounting.post_entries",
            params = lambda ctx: {
                "postings": posting_rules(ctx),
            },
            compensation = lambda ctx, result: {
                "postings": compensation_rules(ctx, result.completed_postings),
            },
        ),
        step(
            name = "save_account",
            action = "repository.save",
            compensation = None,
        ),
    ],

    preconditions = [
        "ctx.amount > 0",
        "ctx.account.status == 'ACTIVE'",
        "ctx.account.available_balance >= ctx.amount",
    ],
)
```

### 5.12 Saga Composition

Sagas can invoke other sagas as steps, enabling reusable workflow components.

#### The `invoke_saga()` Builtin

```python
# parent_saga.star
saga(
    name = "complex_settlement",
    version = "1.0.0",
    steps = [
        step(
            name = "validate_positions",
            action = "position_keeping.validate",
            params = lambda ctx: {"positions": ctx.positions},
        ),
        step(
            name = "process_fees",
            action = "invoke_saga",  # Special action type
            params = lambda ctx: {
                "saga_name": "fee_calculation",  # Child saga
                "saga_version": None,  # Latest ACTIVE
                "context": {
                    "amount": ctx.amount,
                    "fee_type": "SETTLEMENT",
                },
            },
            compensation = lambda ctx, result: {
                "saga_name": "fee_calculation",
                "action": "compensate",
                "execution_id": result.execution_id,
            },
        ),
        step(
            name = "post_ledger",
            action = "financial_accounting.post_entries",
            params = lambda ctx: {"postings": ctx.postings},
        ),
    ],
)
```

#### Compensation Cascade

When a parent saga fails, child saga compensation is triggered automatically in
LIFO order:

```text
Parent Saga Execution:
──────────────────────
Step 1: validate_positions ✓
Step 2: process_fees (invoke_saga → fee_calculation)
    └─ Child Saga: fee_calculation
       Step 2.1: calculate_fee ✓
       Step 2.2: record_fee ✓
Step 3: post_ledger ✗ FAILED

Compensation Cascade:
────────────────────
Step 3: post_ledger - no compensation (never completed)
Step 2: process_fees - compensate child saga
    └─ Child Saga: fee_calculation (compensating)
       Step 2.2: record_fee → REVERSE ✓
       Step 2.1: calculate_fee → REVERSE ✓
Step 1: validate_positions - compensate ✓
```

#### Scope Inheritance

Child sagas inherit (cannot escalate) the parent's party scope:

```python
# Parent executing as party P002 (ACME UK)
# ctx.party_scope.visible_parties = ["P002", "P005", "P006"]

# Child saga receives SAME scope - cannot access P003 (ACME DE)
invoke_saga("fee_calculation", context={...})
# Child ctx.party_scope.visible_parties = ["P002", "P005", "P006"] (inherited)
```

#### Circular Reference Detection

The runtime detects and rejects circular saga invocations at multiple phases:

| Phase | Detection Method |
|-------|-----------------|
| **DRAFT** | Static analysis of `invoke_saga()` calls in Starlark AST |
| **ACTIVATION** | Check all referenced sagas; fail if cycle detected |
| **RUNTIME** | Maintain call stack; fail if saga already in stack |

```text
Error: Circular saga reference detected
────────────────────────────────────────
saga_a.star → invoke_saga("saga_b")
saga_b.star → invoke_saga("saga_c")
saga_c.star → invoke_saga("saga_a") ← CYCLE

Cannot activate saga_c: would create circular dependency.
```

#### Composition Depth Limit

| Constraint | Value | Rationale |
|------------|-------|-----------|
| Max nesting depth | 5 | Prevent deep call stacks |
| Max total steps | 50 | Limit execution complexity |
| Child saga timeout | Inherited from parent | Prevent runaway children |

### 5.13 Meridian Starlark Extensions

Meridian extends vanilla Starlark with domain-specific builtins. These are the
**only** functions available beyond standard Starlark syntax.

#### Core Builtins Reference

| Builtin | Signature | Description |
|---------|-----------|-------------|
| `saga()` | `saga(name, version, steps, preconditions=None)` | Define a saga workflow |
| `step()` | `step(name, action, params, compensation=None)` | Define a saga step |
| `posting()` | `posting(account_id, direction, amount, description=None)` | Create ledger posting instruction |
| `cel_eval()` | `cel_eval(expression, context) → value` | Evaluate CEL expression |
| `resolve_account()` | `resolve_account(purpose, currency) → account_id` | Lookup internal bank account by purpose |
| `resolve_instrument()` | `resolve_instrument(code, version=None) → instrument` | Lookup instrument definition |
| `invoke_saga()` | `invoke_saga(name, version=None, context={}) → result` | Invoke child saga |
| `valuate()` | `valuate(instrument, quantity, context_type) → valuation` | Call Valuation Engine (single context) |
| `valuate_batch()` | `valuate_batch(instrument, quantity, context_types[]) → Dict[context_type, valuation]` | Valuate same basis across multiple contexts; returns dictionary keyed by context_type (e.g., `results["RETAIL"]`, `results["WHOLESALE"]`) |
| `fail()` | `fail(message)` | Abort saga with error message |
| `log()` | `log(level, message, **fields)` | Emit structured log entry |
| `ctx.new_uuid()` | `ctx.new_uuid() → UUID` | Deterministic Version 5 UUID (namespace=saga_id, name=step:call). Stable across replays |
| `verify_external_state()` | `verify_external_state(handler, check_fn) → bool` | Pre-Step Check for non-idempotent external handlers. Required before EXTERNAL_NOT_SUPPORTED calls |

#### Data Access Builtins

| Builtin | Signature | Description |
|---------|-----------|-------------|
| `position_keeping.list()` | `list(party_id=None, instrument=None) → [Position]` | List positions (party-scoped) |
| `position_keeping.get()` | `get(position_id) → Position` | Get single position (party-scoped) |
| `market_data.lookup()` | `lookup(dataset, resolution_key, knowledge_at=None) → Observation` | Get market price |
| `party.get()` | `get(party_id) → Party` | Get party details (scope-checked) |

#### Built-in Types

```python
# Posting - instruction to create ledger entry
Posting = {
    "account_id": UUID,
    "direction": "DEBIT" | "CREDIT",
    "amount": Decimal,
    "description": str,
    "metadata": dict,
}

# ValuationResult - output from valuate()
ValuationResult = {
    "value": Decimal,
    "currency": str,
    "as_of": Timestamp,
    "rule_id": UUID,
    "lineage": [ObservationReference],
}

# Position - from position_keeping
Position = {
    "id": UUID,
    "party_id": UUID,
    "instrument_code": str,
    "quantity": Decimal,
    "attributes": dict,
    "fungibility_key": str,
}

# SagaResult - output from invoke_saga()
SagaResult = {
    "execution_id": UUID,
    "status": "COMPLETED" | "FAILED",
    "output": any,
    "steps_completed": int,
}
```

#### What Is NOT Available

Meridian Starlark explicitly excludes:

| Excluded | Reason |
|----------|--------|
| `import` | No external modules |
| `open()`, `read()` | No file I/O |
| `http`, `requests` | No network access |
| `os`, `sys` | No system access |
| `exec()`, `eval()` | No dynamic code |
| `while True` | No unbounded loops |
| Global mutation | Deterministic execution |

---

## 6. Security Constraints

### 6.1 Starlark Sandbox

| Constraint | Value | Rationale |
|------------|-------|-----------|
| Max script size | 64 KB | Prevent memory exhaustion |
| Max execution time | 5 seconds | Prevent runaway scripts |
| No `while` loops | Language design | Guaranteed termination |
| No recursion depth > 50 | Runtime limit | Prevent stack overflow |
| No file I/O | Language design | Sandboxed execution |
| No network access | Language design | No external calls |
| Deterministic | Language design | Reproducible execution |

#### Implementation Guidance (go.starlark.net)

The Go implementation requires explicit hardening beyond Starlark's language-level safety.

##### Restricted Built-in Environment

Create a custom `starlark.Thread` with filtered built-ins:

```go
// Create hardened Starlark environment
func NewSagaThread(ctx context.Context, auditLogger AuditLogger) *starlark.Thread {
    thread := &starlark.Thread{
        Name: "saga-executor",
        Print: func(_ *starlark.Thread, msg string) {
            // Route print() to audit system, not stdout
            auditLogger.Log("STARLARK_PRINT", msg)
        },
    }

    // Set execution timeout
    thread.SetLocal("context", ctx) // For cancellation checks

    return thread
}

// Whitelisted built-ins only - no load(), no file access
var SagaBuiltins = starlark.StringDict{
    // Domain-specific orchestration functions
    "saga":               starlark.NewBuiltin("saga", sagaBuiltin),
    "step":               starlark.NewBuiltin("step", stepBuiltin),
    "posting":            starlark.NewBuiltin("posting", postingBuiltin),
    "cel_eval":           starlark.NewBuiltin("cel_eval", celEvalBuiltin),
    "resolve_account":    starlark.NewBuiltin("resolve_account", resolveAccountBuiltin),
    "resolve_instrument": starlark.NewBuiltin("resolve_instrument", resolveInstrumentBuiltin),
    "invoke_saga":        starlark.NewBuiltin("invoke_saga", invokeSagaBuiltin),
    "fail":               starlark.NewBuiltin("fail", failBuiltin),
    "log":                starlark.NewBuiltin("log", logBuiltin),

    // Safe subset of standard library
    "True":  starlark.True,
    "False": starlark.False,
    "None":  starlark.None,
    "list":  starlark.NewBuiltin("list", starlark.ListBuiltin),
    "dict":  starlark.NewBuiltin("dict", starlark.DictBuiltin),
    "len":   starlark.NewBuiltin("len", starlark.LenBuiltin),
    "str":   starlark.NewBuiltin("str", starlark.StrBuiltin),
    "int":   starlark.NewBuiltin("int", starlark.IntBuiltin),

    // Explicitly BLOCKED (not included):
    // - load()      → No module imports
    // - print()     → Replaced with audit-routed version above
    // - time.now()  → Use ctx.knowledge_at instead
    // - random()    → Non-deterministic, forbidden
}
```

##### Timeout and Cancellation

Use Go's `context` package to enforce execution limits:

```go
func (r *Runtime) ExecuteSaga(
    ctx context.Context, def *SagaDefinition, input any,
) (*SagaResult, error) {
    // Enforce 5-second timeout
    ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
    defer cancel()

    thread := NewSagaThread(ctx, r.auditLogger)

    // Check for cancellation periodically during execution
    thread.SetLocal("cancel_check", func() error {
        select {
        case <-ctx.Done():
            return fmt.Errorf("saga execution cancelled: %w", ctx.Err())
        default:
            return nil
        }
    })

    // Execute with resource limits
    _, err := starlark.ExecFile(thread, def.Name, def.Script, SagaBuiltins)
    if err != nil {
        if errors.Is(err, context.DeadlineExceeded) {
            return nil, fmt.Errorf("saga exceeded 5s execution limit")
        }
        return nil, err
    }
    // ...
}
```

##### Disabled Functions Reference

| Function | Status | Alternative |
|----------|--------|-------------|
| `load()` | **Blocked** | Use whitelisted builtins only |
| `print()` | **Redirected** | Routes to `AuditLogger`, not stdout |
| `time.now()` | **Blocked** | Use `ctx.knowledge_at` or `ctx.effective_at` |
| `random()` | **Blocked** | Use `ctx.new_uuid()` for deterministic IDs |
| `exec()` | **Blocked** | Not in go.starlark.net, explicitly excluded |
| `compile()` | **Blocked** | Not in go.starlark.net, explicitly excluded |
| `open()` | **Blocked** | No file I/O |
| `http.*` | **Blocked** | No network; use step handlers for external calls |

##### Static Analysis at Upload

Validate scripts before storing in `saga_definitions`:

```go
func (v *SagaValidator) ValidateScript(script string) []ValidationError {
    var errors []ValidationError

    // 1. Parse without executing
    _, err := starlark.ExecFile(nil, "validation", script, SagaBuiltins)
    if err != nil {
        errors = append(errors, ValidationError{
            Type: "SYNTAX", Message: err.Error(),
        })
    }

    // 2. AST analysis for suspicious patterns
    f, _ := syntax.Parse("validation", script, 0)
    syntax.Walk(f, func(n syntax.Node) bool {
        switch x := n.(type) {
        case *syntax.CallExpr:
            // Check for blocked function calls
            if ident, ok := x.Fn.(*syntax.Ident); ok {
                if isBlocked(ident.Name) {
                    errors = append(errors, ValidationError{
                        Type:    "BLOCKED_FUNCTION",
                        Message: fmt.Sprintf("function '%s' is not allowed", ident.Name),
                        Line:    int(x.Lparen.Line),
                    })
                }
            }
        case *syntax.ForStmt:
            // Warn on deeply nested loops (potential DoS)
            depth := countLoopDepth(x)
            if depth > 3 {
                errors = append(errors, ValidationError{
                    Type:    "COMPLEXITY",
                    Message: fmt.Sprintf("loop nesting depth %d exceeds limit", depth),
                    Line:    int(x.For.Line),
                })
            }
        }
        return true
    })

    return errors
}
```

##### Memory and Allocation Monitoring

Track allocations during execution for observability:

```go
func (r *Runtime) ExecuteWithLimits(
    ctx context.Context, def *SagaDefinition, input any,
) (*SagaResult, error) {
    // Track allocations
    var memStats runtime.MemStats
    runtime.ReadMemStats(&memStats)
    startAlloc := memStats.TotalAlloc

    result, err := r.ExecuteSaga(ctx, def, input)

    runtime.ReadMemStats(&memStats)
    allocDelta := memStats.TotalAlloc - startAlloc

    // Log allocation for monitoring; alert if excessive
    if allocDelta > 10*1024*1024 { // 10MB threshold
        r.metrics.RecordHighAllocation(def.Name, allocDelta)
        r.alerter.Warn("saga_high_allocation", map[string]any{
            "saga":        def.Name,
            "alloc_bytes": allocDelta,
        })
    }

    return result, err
}
```

### 6.2 CEL Constraints (from ADR-014)

| Constraint | Value |
|------------|-------|
| Max expression length | 4 KB |
| Max expression depth | 10 levels |
| Cost limit | 10,000 units |

### 6.3 Step Handler Authorization

- Handlers are platform-controlled Go functions
- Starlark cannot invoke arbitrary code
- New handlers require platform deployment and review

### 6.4 Critical Implementation Directives

The following directives are **mandatory** during Phase 1 and Durable Execution implementation:

#### A. Recovery Worker: Staggered Lease Strategy (SAGA-044)

The recovery worker MUST use a "Staggered Lease" strategy to prevent thundering herd:

```go
// CORRECT: Random jitter prevents stampede on cluster-wide restart
func (w *SagaWorker) claimOrphanedSagas(ctx context.Context) {
    // Jitter: 0-500ms random delay before claiming
    jitter := time.Duration(rand.Intn(500)) * time.Millisecond
    time.Sleep(jitter)

    orphans := w.repo.FindOrphaned(ctx, w.claimBatchSize)
    for _, saga := range orphans {
        w.attemptClaim(ctx, saga)
    }
}
```

**Rationale**: Without jitter, all pods attempt to claim all orphaned sagas at the
same microsecond after restart, causing lock contention on `saga_instances`.

#### B. Step Output Hydration: Immutable Structs (Replay Safety)

When replaying a saga, the runtime MUST "hydrate" the Starlark VM with results
from `saga_step_results`. Step handlers MUST return **Starlark Structs** (immutable)
rather than Dicts:

```go
// CORRECT: Return Struct (immutable) - script cannot modify cached result
func (h *PaymentHandler) Execute(ctx *SagaContext) (starlark.Value, error) {
    result := &PaymentResult{ID: "pay_123", Status: "PENDING"}
    return starlarkstruct.FromStringDict(
        starlark.String("PaymentResult"),
        starlark.StringDict{
            "id":     starlark.String(result.ID),
            "status": starlark.String(result.Status),
        },
    ), nil
}

// WRONG: Dict is mutable - script could modify cached replay result
func (h *PaymentHandler) Execute(ctx *SagaContext) (starlark.Value, error) {
    return starlark.NewDict(1), nil  // Mutable!
}
```

**Constraint**: All step handler return types MUST be validated as Struct at
handler registration time.

#### C. Compensation Context: Full Failure Context (SAGA-052)

Compensation logic MUST have access to the **full context** of the failed execution:

```go
type CompensationContext struct {
    // Standard context
    SagaContext

    // Failure-specific fields (REQUIRED for compensation logic)
    FailedStepIndex  int              `json:"failed_step_index"`
    FailedStepName   string           `json:"failed_step_name"`
    ErrorMessage     string           `json:"error_message"`
    ErrorCode        string           `json:"error_code,omitempty"`
    CompletedResults []StepResult     `json:"completed_results"`
}
```

**Requirement**: Compensation functions can decide between "undo everything" and
"partial compensation" based on `failed_step_index` and `error_message`:

```python
# In Starlark compensation:
def compensate(ctx):
    if ctx.failed_step_index < 2:
        # Early failure - just reverse the lien
        reverse_lien(ctx.completed_results[0])
    else:
        # Late failure - full reversal needed
        reverse_all_postings(ctx.completed_results)
```

#### D. Zombie Alerting: Immediate Incident Response

When a saga transitions to `FAILED_MANUAL_INTERVENTION`, the system MUST:

1. **Trigger immediate P1 alert** to the Incident Response Runbook
2. **Preserve bi-temporal integrity**: When an operator hot-fixes via Admin API,
   the `knowledge_at` timestamp for resume MUST be the *original* knowledge
   timestamp of the instance, NOT the time of the fix

```go
// Hot-fix preserves original knowledge_at
func (api *AdminAPI) HotFixSaga(ctx context.Context, req HotFixRequest) error {
    saga, _ := api.repo.FindByID(ctx, req.SagaInstanceID)

    // CRITICAL: Use original knowledge_at, not time.Now()
    resumeCtx := &SagaContext{
        KnowledgeAt: saga.OriginalKnowledgeAt,  // Preserves bi-temporal integrity
        EffectiveAt: saga.EffectiveAt,
    }

    // Audit the hot-fix with current time
    api.audit.Log(AuditEntry{
        Action:       "SAGA_HOT_FIX",
        Operator:     req.OperatorID,
        SagaID:       req.SagaInstanceID,
        OldVersion:   saga.SagaDefinitionID,
        NewVersion:   req.NewDefinitionID,
        Reason:       req.Reason,
        HotFixTime:   time.Now(),  // Current time for audit
    })

    return api.runtime.Resume(resumeCtx, saga, req.NewDefinitionID)
}
```

**Why**: Step results from steps 0-5 must have timestamps that precede the hot-fix.
Steps 6+ (after hot-fix) must have timestamps after the fix. This maintains the
audit trail's bi-temporal correctness.

---

## 7. Existing Saga Mapping

### Current Go Sagas → Starlark Definitions

| Current File | Service | New Definition | Step Handlers Extracted |
|--------------|---------|----------------|-------------------------|
| `shared/pkg/clients/saga.go` | Shared | `shared/pkg/saga/runtime.go` | N/A (runtime, not definition) |
| `payment_orchestrator.go:128-281` | Payment Order | `payment_execution.star` | `current_account.create_lien`, `payment_gateway.send`, `financial_accounting.post_entries` |
| `withdrawal_orchestrator.go:100-185` | Current Account | `withdrawal.star` | `position_keeping.initiate_log`, `financial_accounting.post_entries`, `repository.save` |
| `deposit_orchestrator.go:100-185` | Current Account | `deposit.star` | `position_keeping.initiate_log`, `financial_accounting.post_entries`, `repository.save` |

### Test Coverage Mapping

| Current Test | New Test | What It Validates |
|--------------|----------|-------------------|
| `saga_test.go` (14 cases) | `runtime_test.go` | Orchestrator contract: step order, LIFO compensation, context cancellation |
| `payment_orchestrator_test.go` | `handlers/payment_test.go` | Step handler behavior (Go code, unchanged) |
| `withdrawal_orchestrator_test.go` | `handlers/current_account_test.go` | Step handler behavior (Go code, unchanged) |
| N/A | `definition_test.go` | Starlark parsing, reference extraction, validation |
| N/A | `registry_test.go` | CRUD, lifecycle, tenant resolution, caching |
| Integration tests | Integration tests (same) | End-to-end saga execution, same expected outcomes |

### Adding New Tests

| Test Type | How to Add | Example |
|-----------|------------|---------|
| **Step handler test** | Standard Go unit test | `TestPositionKeepingInitiateLog_Success` |
| **Definition parsing test** | Load `.star` file, assert steps extracted | `TestWithdrawalStar_ParsesCorrectly` |
| **Reference validation test** | Create saga with missing ref, assert warning | `TestValidation_MissingHandler_ReturnsWarning` |
| **Tenant override test** | Create system + tenant saga, assert tenant wins | `TestResolution_TenantOverridesSystem` |
| **Simulation test** | Run DRAFT saga with `knowledge_at`, assert no side effects | `TestSimulation_NoLiveDataModified` |

---

## 8. Service Feature Gap Analysis

This PRD references capabilities across multiple services. This section clarifies what **exists today** vs what is **proposed**.

### Existing Features

| Service | Feature | Status | Notes |
|---------|---------|--------|-------|
| **Party Service** | `party` table with PERSON/ORGANIZATION types | ✅ Exists | Core party identity |
| **Party Service** | `party_association` for personal relationships | ✅ Exists | SPOUSE, DEPENDENT, GUARANTOR, etc. |
| **Party Service** | Party hierarchy (org → child parties) | ❓ Partial | Need to verify recursive query support |
| **Current Account** | `account.party_id` reference | ✅ Exists | Links account to party (not FK) |
| **Current Account** | Account lookup by party | ✅ Exists | `current_account.by_party()` |
| **Internal Bank Account** | `attributes` JSONB column | ✅ Exists | Can store GSP, DNO, etc. |
| **Internal Bank Account** | Lookup by attributes | ❓ Partial | May need index/API |
| **Reference Data** | Instrument definitions with lifecycle | ✅ Exists | Pattern to follow |
| **Position Keeping** | Position with `party_id` | ✅ Exists | Core position model |
| **Market Information** | Bi-temporal observations | ✅ Exists | `knowledge_at` support |

### Proposed Features (This PRD)

| Service | Feature | Priority | Notes |
|---------|---------|----------|-------|
| **Reference Data** | `saga_definitions` table | P0 | New table for Starlark scripts |
| **Reference Data** | `saga_references` table | P0 | Reference tracking for validation |
| **Reference Data** | `saga_execution_log` table | P1 | Audit trail with bi-temporal |
| **Shared Runtime** | Starlark VM integration | P0 | `go.starlark.net` |
| **Shared Runtime** | Step handler registry | P0 | Platform-controlled actions |
| **Shared Runtime** | Party scope injection | P0 | `ctx.party_scope` |
| **Party Service** | Party hierarchy query (recursive) | P1 | `visible_parties` resolution |
| **Party Service** | `party_relationships` table (optional) | P2 | OPERATOR, CUSTODIAN, BROKER |
| **Internal Bank Account** | Lookup by GSP code | P1 | `by_attributes(gsp="P")` |

### Integration Points Requiring Coordination

| Integration | Services Involved | Dependency |
|-------------|-------------------|------------|
| Party scope resolution | Party Service ↔ Saga Runtime | Runtime calls Party Service to resolve hierarchy |
| Account lookup | Current Account ↔ Saga Runtime | Runtime calls Current Account for party's accounts |
| Internal account lookup | Internal Bank Account ↔ Saga Runtime | Runtime calls IBA for GSP/DNO accounts |
| Position access | Position Keeping ↔ Saga Runtime | Step handlers query positions with party scope |
| Valuation (future) | Valuation Engine ↔ Saga Runtime | `valuate()` step handler |

### Flexible Attribute Model for Account Resolution

Position attributes are **tenant-defined** via the asset class model. Sagas
use these attributes for account resolution without hardcoding attribute keys:

```text
┌─────────────────────────────────────────────────────────────────────────────┐
│                    Tenant-Defined Attribute Examples                         │
├─────────────────────────────────────────────────────────────────────────────┤
│                                                                             │
│  ENERGY TENANT:                                                             │
│    position.attributes = {                                                  │
│      "gsp_code": "P",           # Grid Supply Point                        │
│      "dno_code": "WPD",         # Distribution Network Operator            │
│      "settlement_period": "HH23"                                           │
│    }                                                                        │
│                                                                             │
│  WEALTH TENANT:                                                             │
│    position.attributes = {                                                  │
│      "custodian_id": "CUST-001", # Custodian                               │
│      "sub_account": "TRADING",   # Account classification                  │
│    }                                                                        │
│                                                                             │
│  CARBON TENANT:                                                             │
│    position.attributes = {                                                  │
│      "vintage": "2024",          # Credit vintage year                     │
│      "registry": "VERRA",        # Carbon registry                         │
│      "project_id": "VCS-1234"                                              │
│    }                                                                        │
│                                                                             │
└─────────────────────────────────────────────────────────────────────────────┘
```

| Lookup Type | Method | Notes |
|-------------|--------|-------|
| Party → Account | `current_account.by_party(party_id)` | Standard party lookup |
| Attributes → Account | `internal_bank_account.by_attributes(attrs)` | Generic JSONB matching |
| Party details | `party.get(party_id)` | Scope-checked party lookup |

**Required**: Internal Bank Account service needs generic attribute-based lookup API:

- Input: `map[string]any` (tenant-defined keys)
- Query: JSONB `@>` containment or key matching
- Index: GIN index on `attributes` column for performance

---

## 9. Implementation Tasks

| Task ID | Description | Priority | Dependencies |
|---------|-------------|----------|--------------|
| **SAGA-001** | Create `saga_definitions` table in Reference Data | P0 | - |
| **SAGA-002** | Implement `SagaRegistry` interface (CRUD + lifecycle) | P0 | SAGA-001 |
| **SAGA-003** | Integrate `go.starlark.net` runtime | P0 | - |
| **SAGA-004** | Implement Starlark builtins (`cel_eval`, `posting`, etc.) | P0 | SAGA-003 |
| **SAGA-005** | Create step handler registry with default handlers | P0 | - |
| **SAGA-006** | Implement Redis caching layer | P1 | SAGA-002 |
| **SAGA-007** | Implement tenant default resolution | P1 | SAGA-002, SAGA-006 |
| **SAGA-008** | Migrate `withdrawal_orchestrator.go` to Starlark | P0 | SAGA-003, SAGA-004, SAGA-005 |
| **SAGA-009** | Migrate `deposit_orchestrator.go` to Starlark | P1 | SAGA-008 |
| **SAGA-010** | Migrate `payment_orchestrator.go` to Starlark | P1 | SAGA-008 |
| **SAGA-011** | Implement simulation mode for DRAFT sagas | P1 | SAGA-008 |
| **SAGA-012** | Create saga execution audit logging | P1 | SAGA-008 |
| **SAGA-013** | Seed platform default sagas | P0 | SAGA-002 |
| **SAGA-014** | Admin API for saga management | P2 | SAGA-002 |
| **SAGA-015** | Documentation and tenant onboarding guide | P2 | SAGA-008 |
| **SAGA-016** | Create `saga_references` table | P0 | SAGA-001 |
| **SAGA-017** | Implement reference extraction from Starlark AST | P0 | SAGA-003, SAGA-016 |
| **SAGA-018** | Implement DRAFT phase validation with warnings | P0 | SAGA-017 |
| **SAGA-019** | Implement ACTIVATION phase validation (hard fail) | P0 | SAGA-017 |
| **SAGA-020** | Implement deprecation impact analysis | P1 | SAGA-016 |
| **SAGA-021** | Add validation feedback API endpoint | P1 | SAGA-018, SAGA-019 |
| **SAGA-022** | Implement party scope resolution from Party Service | P0 | SAGA-003 |
| **SAGA-023** | Add `ctx.party_scope` injection with immutability enforcement | P0 | SAGA-022 |
| **SAGA-024** | Implement `authorized_lookups` declaration and runtime enforcement | P0 | SAGA-022 |
| **SAGA-025** | Add `party_id` and `visible_parties` to saga execution audit log | P1 | SAGA-012 |
| **SAGA-026** | Implement `invoke_saga()` builtin with scope inheritance | P1 | SAGA-004, SAGA-023 |
| **SAGA-027** | Add circular saga reference detection (DRAFT + ACTIVATION) | P1 | SAGA-017, SAGA-026 |
| **SAGA-028** | Add runtime circular detection via call stack | P1 | SAGA-026 |
| **SAGA-029** | Implement compensation cascade for child sagas | P1 | SAGA-026 |
| **SAGA-030** | Add composition depth and total steps limits | P1 | SAGA-026 |
| **SAGA-031** | Add bi-temporal index on `saga_definitions` for version replay | P1 | SAGA-001 |
| **SAGA-032** | Implement `replay_execution()` with historical saga version | P1 | SAGA-012, SAGA-031 |
| **SAGA-033** | Add `verify_execution()` for audit drift detection | P2 | SAGA-032 |
| **SAGA-034** | Internal Bank Account: Add generic attribute-based lookup API (`by_attributes(map)`) | P1 | - |
| **SAGA-035** | Party Service: Implement recursive party hierarchy query | P1 | - |
| **SAGA-036** | Party Service: Add `party_relationships` table (optional) | P2 | - |
| **SAGA-037** | Extract attribute key accesses from Starlark AST | P0 | SAGA-017 |
| **SAGA-038** | Validate attribute refs against instrument schema at ACTIVATION | P0 | SAGA-037 |
| **SAGA-039** | Add attribute dependency check to instrument schema update | P1 | SAGA-037 |
| **SAGA-040** | Extend `saga_references` table for attribute tracking | P0 | SAGA-016, SAGA-037 |
| **SAGA-041** | Create `saga_instances` table (service-local, common pattern) | P0 | - |
| **SAGA-042** | Create `saga_step_results` table with idempotency keys | P0 | SAGA-041 |
| **SAGA-043** | Implement lease-based claiming with `FOR UPDATE SKIP LOCKED` | P0 | SAGA-041 |
| **SAGA-044** | Implement orphan saga detection and adoption on pod startup | P0 | SAGA-043 |
| **SAGA-045** | Implement replay execution (skip completed steps) | P0 | SAGA-042 |
| **SAGA-046** | Add lease renewal background worker | P1 | SAGA-043 |
| **SAGA-047** | Implement `valuate_batch()` builtin for multi-context valuation | P1 | SAGA-004 |
| **SAGA-048** | Add Logic/Physics Linter (warn on math in Starlark, enforce Pre-Step Check for EXTERNAL_NOT_SUPPORTED) | P1 | SAGA-017, SAGA-053 |
| **SAGA-049** | Define step handler output schemas (typed contracts) | P1 | SAGA-005 |
| **SAGA-050** | Validate script accesses against handler output schemas | P2 | SAGA-049 |
| **SAGA-051** | Add `ctx.is_simulation` flag and handler enforcement | P1 | SAGA-004 |
| **SAGA-052** | Implement automatic `causation_id` propagation to compensations | P1 | SAGA-004 |
| **SAGA-053** | Add idempotency declaration to step handler interface (EXTERNAL_SUPPORTED/NOT_SUPPORTED) | P0 | SAGA-005 |
| **SAGA-054** | Implement ACTIVATION fail-fast for non-idempotent external handlers without Pre-Step Check | P0 | SAGA-053, SAGA-019 |
| **SAGA-055** | Implement zombie saga detection (`replay_count` > MAX_REPLAYS → FAILED_MANUAL_INTERVENTION) | P0 | SAGA-044 |
| **SAGA-056** | Add high-severity alerting for zombie sagas (operator notification) | P1 | SAGA-055 |
| **SAGA-057** | Implement `ExecuteDryRun` RPC (mocked handlers, execution plan output) | P1 | SAGA-017 |
| **SAGA-058** | Implement `ctx.new_uuid()` deterministic UUID generator (Version 5 UUIDs) | P0 | SAGA-004 |
| **SAGA-059** | Implement `verify_external_state()` builtin for Pre-Step Check pattern | P0 | SAGA-053 |
| **SAGA-060** | Add Admin API for saga hot-fixing (definition re-pointing, replay_count reset) | P1 | SAGA-055 |

---

## 10. Migration Strategy

### Phase 1: Foundation (SAGA-001 through SAGA-007)

- Schema, registry, runtime, caching
- No production impact

### Phase 2: Prove the Pattern (SAGA-008)

- Migrate withdrawal saga
- Run in shadow mode alongside Go implementation
- Compare outputs, verify correctness

### Phase 3: Expand (SAGA-009, SAGA-010)

- Migrate remaining sagas
- Deprecate Go orchestrators

### Phase 4: Enable Tenants (SAGA-011 through SAGA-015)

- Simulation mode, admin API, documentation
- Tenant self-service for custom sagas

### Phase 5: Party Isolation & Composition (SAGA-022 through SAGA-030)

- Party scope resolution and injection
- Authorized lookups for cross-party posting (contextual model)
- `invoke_saga()` builtin for saga composition
- Circular reference detection and compensation cascade
- Enables multi-party settlement (energy, wealth management)

### Phase 6: Bi-Temporal & Service Integration (SAGA-031 through SAGA-036)

- Bi-temporal saga versioning for audit replay
- Historical saga replay with `verify_execution()`
- Internal Bank Account generic attribute-based lookup (tenant-defined keys)
- Party Service recursive hierarchy query
- Optional: Party relationships table for explicit authorization tracking

### Phase 7: Attribute Schema Validation (SAGA-037 through SAGA-040)

- Extract attribute key accesses from Starlark AST
- Validate saga attribute refs against instrument schema at ACTIVATION
- Block instrument schema changes that would break active sagas
- Bidirectional dependency tracking (saga ↔ instrument attributes)

### Phase 8: Durable Execution (SAGA-041 through SAGA-046)

- Service-local `saga_instances` and `saga_step_results` tables
- Lease-based claiming prevents race conditions across pods
- Orphan detection and adoption on pod startup
- Replay execution skips completed steps using cached results
- Transforms Meridian into "Mini-Temporal" for BIAN ledgers

### Phase 9: Hardening & Validation (SAGA-047 through SAGA-052)

- `valuate_batch()` for multi-context valuation (same basis guarantee)
- Logic/Physics Linter warns on math in Starlark ("move to CEL")
- Step handler output schemas (typed contracts)
- Simulation mode enforcement (`ctx.is_simulation`)
- Causation ID propagation to compensation steps

---

## 11. Success Criteria

| Metric | Target | Measurement |
|--------|--------|-------------|
| **Correctness** | 100% parity with Go sagas | Shadow mode comparison |
| **Performance** | < 50ms saga load time (cached) | P99 latency |
| **Cache hit rate** | > 95% | Redis metrics |
| **Tenant adoption** | 3+ custom sagas within 90 days | Usage tracking |
| **Deployment reduction** | 0 deployments for tenant workflow changes | Release tracking |

### Acceptance Criteria: Party-Level Data Isolation

| ID | Criterion | Test Method |
|----|-----------|-------------|
| **AC-PI-01** | Individual party saga CANNOT read positions of sibling parties | Unit test: assert `position_keeping.list()` returns empty for sibling party_id |
| **AC-PI-02** | Organization party saga CAN read positions of descendant parties | Unit test: assert `position_keeping.list()` returns descendant positions |
| **AC-PI-03** | Saga `ctx.party_scope` is immutable | Unit test: assert mutation throws error |
| **AC-PI-04** | Cross-party posting governed by contextual lookup model (Section 5.7) | Integration test: posting to unrelated party fails |
| **AC-PI-05** | Authorized cross-party posting succeeds via contextual visibility rules | Integration test: contextual lookup resolves counterparty accounts correctly |
| **AC-PI-06** | Saga execution log includes `party_id` and `visible_parties` from contextual lookup | Unit test: verify audit fields reflect contextual visibility at execution time |
| **AC-PI-07** | Child saga inherits parent party scope (cannot escalate) | Unit test: `invoke_saga()` passes same `party_scope` |
| **AC-PI-08** | Query by `visible_parties` returns correct executions | Query test: GIN index query returns expected results |

### Acceptance Criteria: Saga Composition

| ID | Criterion | Test Method |
|----|-----------|-------------|
| **AC-SC-01** | `invoke_saga()` executes child saga synchronously | Unit test: verify child completes before parent continues |
| **AC-SC-02** | Parent failure triggers child compensation (LIFO) | Integration test: fail step 3, verify step 2 child compensates |
| **AC-SC-03** | Circular saga references detected at ACTIVATION | Unit test: A→B→C→A fails activation |
| **AC-SC-04** | Circular saga references detected at RUNTIME (defense in depth) | Unit test: call stack check prevents re-entry |
| **AC-SC-05** | Nesting depth > 5 rejected | Unit test: 6-level nesting fails |
| **AC-SC-06** | Total steps > 50 rejected | Unit test: saga with 51 total steps fails |
| **AC-SC-07** | Child saga result accessible in parent context | Unit test: `result.execution_id` available for compensation |

### Acceptance Criteria: Reference Validation

| ID | Criterion | Test Method |
|----|-----------|-------------|
| **AC-RV-01** | DRAFT saves with warnings for missing references | Unit test: save succeeds, warnings returned |
| **AC-RV-02** | ACTIVATION fails with missing step handler | Unit test: activation rejected, error message includes handler name |
| **AC-RV-03** | ACTIVATION fails with DEPRECATED instrument reference | Unit test: activation rejected, successor suggested |
| **AC-RV-04** | Deprecation impact analysis lists dependent sagas | Unit test: deprecate instrument shows 3 dependent sagas |
| **AC-RV-05** | RUNTIME fails fast with actionable error for invalid reference | Integration test: error includes line number and suggestion |

### Acceptance Criteria: Attribute Schema Validation

| ID | Criterion | Test Method |
|----|-----------|-------------|
| **AC-AV-01** | DRAFT extracts attribute key accesses from Starlark | Unit test: parse `ctx.position.attributes["gsp_code"]`, extract "gsp_code" |
| **AC-AV-02** | ACTIVATION fails if attribute not in instrument schema | Unit test: saga refs `["foo"]`, instrument has no `foo` → blocked |
| **AC-AV-03** | ACTIVATION succeeds if all attributes exist in schema | Unit test: saga refs match instrument attributes → activated |
| **AC-AV-04** | Instrument attribute removal blocked if saga depends on it | Integration test: remove attr → error listing dependent sagas |
| **AC-AV-05** | Instrument attribute addition does not affect existing sagas | Unit test: add new attr → no saga validation errors |
| **AC-AV-06** | `saga_references` tracks attribute refs with instrument code | Query test: find sagas using `KWH.attributes["gsp_code"]` |
| **AC-AV-07** | Validation feedback suggests similar attribute names | Unit test: ref `settlement_period`, suggest `settlement_date` |

### Acceptance Criteria: Durable Execution

| ID | Criterion | Test Method |
|----|-----------|-------------|
| **AC-DE-01** | Saga state persisted before each step execution | Unit test: verify `saga_step_results` row exists before handler returns |
| **AC-DE-02** | Pod restart resumes saga from last completed step | Integration test: kill pod mid-saga, restart, verify completion |
| **AC-DE-03** | Replay returns cached results for completed steps | Unit test: replay saga, verify handler not called for completed steps |
| **AC-DE-04** | Lease-based claiming prevents duplicate processing | Concurrency test: two pods claim same saga, only one succeeds |
| **AC-DE-05** | Orphan detection finds sagas with expired leases | Unit test: expire lease, verify saga appears in orphan query |
| **AC-DE-06** | Idempotency keys prevent duplicate side effects | Integration test: replay step, downstream service returns cached result |
| **AC-DE-07** | Lease renewal extends expiry while processing | Unit test: verify `lease_expires_at` updated during long saga |

### Acceptance Criteria: Determinism & Hardening

| ID | Criterion | Test Method |
|----|-----------|-------------|
| **AC-DH-01** | No `time.now()` or clock access in Starlark | Unit test: attempt to call time function, verify error |
| **AC-DH-02** | All time logic uses `ctx.knowledge_at` or `ctx.effective_at` | Code review: audit all handlers for time access |
| **AC-DH-03** | Step handler returns Starlark-compatible Dict/Struct | Unit test: verify all handlers return typed responses |
| **AC-DH-04** | `ctx.is_simulation` prevents side effects in sim mode | Integration test: run simulation, verify no real transactions |
| **AC-DH-05** | `causation_id` auto-propagated to compensation steps | Unit test: verify compensation has parent's causation_id |
| **AC-DH-06** | `valuate_batch()` uses identical measurement for all contexts | Unit test: verify all valuations reference same basis |
| **AC-DH-07** | Logic/Physics Linter warns on `a * b` in Starlark | Unit test: script with multiplication, verify warning |

### Acceptance Criteria: External Integration & Resilience

| ID | Criterion | Test Method |
|----|-----------|-------------|
| **AC-EI-01** | External step handlers declare idempotency capability | Code review: all external handlers have `idempotency` attribute |
| **AC-EI-02** | ACTIVATION fails for non-idempotent external handler without Pre-Step Check | Unit test: saga with non-idempotent handler, verify activation error |
| **AC-EI-03** | Zombie saga (replay_count > MAX_REPLAYS) transitions to FAILED_MANUAL_INTERVENTION | Integration test: force 6 replays, verify status transition |
| **AC-EI-04** | Zombie detection triggers high-severity alert | Integration test: verify alerting system receives P1 notification |
| **AC-EI-05** | `ExecuteDryRun` returns execution plan without persisting | Unit test: call dry-run, verify no database writes |
| **AC-EI-06** | `ctx.new_uuid()` returns deterministic UUID (stable across replays) | Unit test: replay saga, verify same UUIDs generated |
| **AC-EI-07** | Transaction affinity: step result + index update are atomic | Unit test: inject failure mid-step, verify no partial state |
| **AC-EI-08** | `verify_external_state()` prevents duplicate external calls | Integration test: replay saga, verify external call made once |
| **AC-EI-09** | Linter warns on EXTERNAL_NOT_SUPPORTED without verify_external_state | Unit test: script missing check, verify linter warning |
| **AC-EI-10** | Saga hot-fix re-points instance to new definition version | Admin API test: hot-fix stuck saga, verify `saga_definition_id` updated, resumed execution |
| **AC-EI-11** | Hot-fix audit trail captures operator, reason, old/new version | Unit test: verify audit log contains hot-fix details |
| **AC-EI-12** | Hot-fix preserves bi-temporal integrity (step_results timestamps accurate) | Query test: after hot-fix, steps 0-5 timestamps < hot-fix, step 6+ > hot-fix |

---

## 12. Risks and Mitigations

| Risk | Impact | Likelihood | Mitigation |
|------|--------|------------|------------|
| Starlark performance insufficient | High | Low | CEL for hot path; Starlark for orchestration only |
| Tenant writes broken saga | Medium | Medium | Simulation mode required before activation |
| Redis cache failure | Medium | Low | Fallback to database; circuit breaker |
| Migration breaks existing flows | High | Medium | Shadow mode comparison; feature flags |

---

## 13. Appendix: Why Starlark?

### Comparison with Alternatives

| Option | Pros | Cons |
|--------|------|------|
| **Starlark** | Python syntax, guaranteed termination, Google-maintained | Learning curve for operations |
| **CEL** | Fast, already in use | Not expressive enough for orchestration |
| **Lua** | Fast, embeddable | Less familiar syntax, Turing-complete |
| **JavaScript** | Familiar | Turing-complete, security concerns |
| **YAML/JSON config** | Simple | Not expressive enough, becomes unwieldy |
| **Go plugins** | Native performance | Requires deployment, security risks |

### The "Safe Python" Pitch

For tenant communication:

> Saga definitions use Python syntax - specifically, a safe subset designed
> for workflow configuration. If you can write a Python function, you can
> write a saga. The platform guarantees your script will always terminate and
> cannot access files or networks.

### What Starlark Removes from Python

| Removed | Rationale |
|---------|-----------|
| `while` loops | Guaranteed termination |
| Unbounded `for` | Must iterate over finite collections |
| `import` | No external code |
| File I/O | Sandboxed |
| Network | Sandboxed |
| `exec`/`eval` | No dynamic code execution |
| Global state mutation | Deterministic execution |

### What Starlark Keeps from Python

| Kept | Example |
|------|---------|
| `def` functions | `def posting_rules(ctx):` |
| `if`/`elif`/`else` | `if ctx.amount > 0:` |
| `for` over collections | `for p in postings:` |
| List comprehensions | `[p.amount for p in postings]` |
| Dictionaries | `{"key": "value"}` |
| String formatting | `f"Amount: {amount}"` |
| Lambda expressions | `lambda ctx: ctx.amount * 0.1` |

---

## 14. Links

- [ADR-028: Starlark Saga Orchestration with CEL Valuation](../adr/0028-starlark-saga-cel-valuation.md)
- [ADR-014: Financial Instrument Reference Data](../adr/0014-financial-instrument-reference-data.md)
- [go.starlark.net](https://pkg.go.dev/go.starlark.net/starlark) - Starlark Go implementation
- [Starlark Language Spec](https://github.com/bazelbuild/starlark/blob/master/spec.md)
- [google/cel-go](https://github.com/google/cel-go) - CEL Go implementation
- [Party Service](../adr/0003-party-management.md) - Party hierarchy and relationships (cross-party authorization)
