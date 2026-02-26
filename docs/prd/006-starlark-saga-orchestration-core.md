# PRD: Starlark Saga Orchestration (Core)

**Status:** Implemented
**Version:** 1.1
**Author:** Architecture Team
**Task Master Tag:** `starlark-saga-orchestration` (24/24 tasks done)
**ADR Reference:** [ADR-028](../adr/0028-starlark-saga-cel-valuation.md)
**Companion PRD:** [Durable Execution Engine](./005-durable-execution-engine.md)

---

## Table of Contents

- [1. Executive Summary](#1-executive-summary)
- [2. BIAN Alignment](#2-bian-alignment)
- [3. Functional Requirements](#3-functional-requirements)
- [4. CEL Valuation: Context and Boundaries](#4-cel-valuation-context-and-boundaries)
- [5. Technical Architecture](#5-technical-architecture)
- [6. Security Constraints](#6-security-constraints)
- [7. Existing Saga Mapping](#7-existing-saga-mapping)
- [8. Service Feature Gap Analysis](#8-service-feature-gap-analysis)
- [9. Parallel Work Streams](#9-parallel-work-streams)
- [10. Success Criteria](#10-success-criteria)
- [11. Risks and Mitigations](#11-risks-and-mitigations)
- [12. Appendix: Why Starlark?](#12-appendix-why-starlark)
- [13. Links](#13-links)

---

## 1. Executive Summary

The Starlark Saga Orchestration system migrates hardcoded Go saga logic to
runtime-configurable workflow definitions. Saga definitions are stored in the
Reference Data service (alongside instrument definitions), cached in Redis for
performance, and executed by a shared runtime.

This PRD covers the **Core** orchestration layer - the schema, runtime, registry,
validation, party isolation, and composition subsystems. The companion PRD covers
the Durable Execution Engine (checkpointing, recovery, and resilience features).

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

### Scope of This PRD

This Core PRD covers **Streams 1-6** of the implementation:

| Stream | Focus | Key Capabilities |
|--------|-------|------------------|
| **1** | Schema | Database tables for saga definitions and references |
| **2** | Runtime | Starlark VM, builtins, Decimal type |
| **3** | Registry | CRUD, lifecycle, caching, tenant resolution |
| **4** | Validation | Reference extraction, activation checks, linting |
| **5** | Party Isolation | Party scope resolution, data access controls |
| **6** | Composition | invoke_saga, circular detection, compensation cascade |

The **Durable Execution Engine** (Streams 7-10: checkpointing, recovery, async handling)
is covered in the companion PRD.

---

## 2. BIAN Alignment

This capability extends multiple BIAN service domains by externalizing their orchestration logic:

| Service Domain | Current Implementation | With Starlark |
|----------------|----------------------|---------------|
| Payment Order | `payment_orchestrator.go` | `payment_execution.star` |
| Current Account | `withdrawal_orchestrator.go`, `deposit_orchestrator.go` | `withdrawal.star`, `deposit.star` |
| Internal Account | Clearing operations | `clearing_settlement.star` |
| *NEW* Settlement | N/A | `energy_settlement.star`, `asset_settlement.star` |

The saga definitions become **Administrative Plan Records** - auditable configuration that governs workflow execution.

---

## 3. Functional Requirements

### FR-1: Saga Definition Storage

- **Requirement**: Saga definitions MUST be stored in Reference Data service
  with lifecycle management (DRAFT -> ACTIVE -> DEPRECATED)
- **Pattern**: Follow `InstrumentDefinition` model from ADR-014
- **Constraint**: ACTIVE definitions are immutable; create new version to change

### FR-2: Starlark Runtime Execution

- **Requirement**: The system MUST execute Starlark scripts with guaranteed termination
- **Language**: Starlark (deterministic subset of Python - no while loops, no I/O, no imports)
- **Builtins**: Platform provides `cel_eval()`, `posting()`, `resolve_account()`, step handlers

### FR-34: External Lookup Result Capture (Replay Safety)

- **Requirement**: The Runtime MUST capture and serialize all external lookup results into
  `saga_step_results` if they are called outside of a formal `step()` block
- **Problem**: Replay-based recovery relies on determinism. If `resolve_account()` returns a
  different ID during replay than it did during the first run, the logic will diverge
- **Affected builtins**: `resolve_account()`, `resolve_instrument()`, any lookup that touches
  Reference Data or external services
- **Implementation options**:
  1. **Implicit Step Wrapping**: Runtime auto-wraps lookup calls in ephemeral steps
  2. **Input Snapshot Enhancement**: Expand `input_snapshot` to include all lookup results
  3. **Lookup Cache Table**: Dedicated `saga_lookup_results` table for non-step lookups
- **Guarantee**: Same saga instance replaying same lookups produces identical results
- **Bridge to Durable PRD**: This requirement enables the Durable Execution Engine's replay
  mechanism to function correctly even when scripts call lookups outside formal steps

### FR-3: CEL Integration for Calculations

- **Requirement**: Starlark scripts MUST call CEL expressions for financial calculations
- **Rationale**: CEL provides ~100ns evaluation; Starlark handles orchestration flow
- **Constraint**: Valuation maths MUST NOT be implemented directly in Starlark

### FR-4: Tenant Default with Override

- **Requirement**: Platform provides default saga definitions; tenants MAY override
- **Resolution order**: Tenant-specific -> Platform default
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
- **Organisation party**: Access own + descendant parties (enables aggregate views)
- **Enforcement**: Runtime resolves party tree; injects immutable `ctx.party_scope`
- **No bypass**: Saga authors cannot access parties outside their scope
- **Audit**: All executions logged with `party_id` for compliance

### FR-35: Visibility Manifest Pre-Flight Check

- **Requirement**: Before a Saga transitions from PENDING to RUNNING, the Runtime SHALL
  generate a "Visibility Manifest" listing all Party IDs that will be accessed
- **Problem**: "Implicit Authorisation" (allowing access mid-execution) is high-risk for
  auditors and can result in partial executions that fail halfway due to permission issues
- **Implementation**:
  1. Static analysis extracts all `party_id` references from the Starlark AST
  2. Runtime resolves executing party's recursive visibility (from Party Service hierarchy)
  3. Manifest = union of all referenced party IDs in the saga
  4. Pre-flight check: IF executing party does NOT have visibility over the entire manifest,
     THEN saga MUST fail-fast BEFORE the first step executes
- **Benefit**: Moves security check from "middle of execution" to "pre-flight," preventing
  partial executions that fail halfway due to permission issues
- **Error message**: "Party P002 lacks visibility over parties [P003, P007] required by
  this saga. Execution blocked."
- **Exemptions**: Contextual lookups (e.g., `resolve_account("clearing", currency)`) where
  the target party is determined at runtime are validated per-step, not pre-flight

### FR-12: Saga Composition

- **Requirement**: Sagas MAY invoke other sagas as steps via `invoke_saga()` builtin
- **Compensation**: Child saga compensation cascades automatically on parent failure
- **Scope inheritance**: Child saga inherits parent's party scope (cannot escalate)
- **Circular detection**: Runtime MUST detect and reject circular saga references

### FR-27: Starlark Decimal Type

- **Requirement**: The Runtime MUST provide a custom Starlark type for Decimals
- **Problem**: Starlark natively supports `int`, `float`, `string` but financial maths
  requires `shopspring/decimal` precision
- **Implementation**: Custom `Decimal` type in `shared/pkg/saga` with operator overloading
  (`+`, `-`, `*`, `/`)
- **Step handler contract**: All handlers returning monetary values (especially Valuation)
  MUST return `Decimal` type, not `float`
- **Starlark usage**: `retail_val + wholesale_val` maintains Go backend precision
- **Prevention**: Eliminates "Rounding Drift" during saga execution

### FR-33: Semantic Logic/Physics Linter

- **Requirement**: The Linter SHALL be semantic, not just syntactic, for Decimal arithmetic
- **Problem**: Developers may bypass "no maths in Starlark" rule using new Decimal type
- **Detection**: Warn on any arithmetic operator (`+`, `-`, `*`, `/`) where operands
  are not derived from simple counters or loop indices
- **Suggested message**: "Financial maths detected. Move this to a CEL Valuation
  Strategy in Reference Data."
- **Exemptions**: Counter arithmetic (`i + 1`), list indexing, percentage calculations
  using pre-validated rates from Valuation Engine
- **Enforcement level**: WARNING at DRAFT, ERROR at ACTIVATION (configurable)

---

## 4. CEL Valuation: Context and Boundaries

> **Note**: CEL-based valuation is **out of scope** for this PRD but provides
> essential context. This refactor establishes the foundation that the
> Valuation Engine will build upon.

### 4.1 Composition Model (Not Embedding)

Starlark sagas **call** the Valuation Engine; they do not embed CEL valuation logic:

```text
WRONG: CEL embedded in Starlark
----------------------------------
def posting_rules(ctx):
    # Don't do this - valuation logic coupled to saga
    value = cel_eval("qty * 0.35", {"qty": ctx.quantity})


RIGHT: Valuation as service call
--------------------------------
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
+-- 10 VCU (vintage: 2023, project: ABC)
+-- 5 VCU (vintage: 2024, project: ABC)
+-- 3 VCU (vintage: 2023, project: XYZ)

CEL valuation applied per-bucket:
+-- 10 x $45 (2023 vintage price) = $450
+-- 5 x $52 (2024 vintage price)  = $520
+-- 3 x $45 (2023 vintage price)  = $135
                                    -----
                        Total:      $1,105
```

The saga orchestrates the totalling; CEL provides the per-bucket calculation.

### 4.4 Saga <-> Valuation Integration Point

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
+-----------------------------------------------------------------------------+
|                           Service Layer                                      |
|  +-------------+  +-------------+  +-------------+  +-------------+        |
|  |  Payment    |  |  Current    |  |  Internal   |  | Settlement  |        |
|  |   Order     |  |  Account    |  |    Bank     |  |  (NEW)      |        |
|  +------+------+  +------+------+  +------+------+  +------+------+        |
|         |                |                |                |               |
|         +----------------+----------------+----------------+               |
|                                    |                                        |
|                                    v                                        |
|  +---------------------------------------------------------------------+   |
|  |                    Saga Runtime (shared/pkg/saga)                    |   |
|  |  +-------------+  +-------------+  +-------------+                  |   |
|  |  |  Starlark   |  |    CEL      |  |    Step     |                  |   |
|  |  |     VM      |  |  Evaluator  |  |  Registry   |                  |   |
|  |  +-------------+  +-------------+  +-------------+                  |   |
|  +---------------------------------------------------------------------+   |
+-----------------------------------------------------------------------------+
                                    |
              +---------------------+---------------------+
              v                     v                     v
+---------------------+  +---------------------+  +---------------------+
|    Reference Data   |  |       Redis         |  |  External Services  |
|   (Saga Storage)    |  |   (Saga Cache)      |  |  (Step Execution)   |
|                     |  |                     |  |                     |
|  saga_definitions   |  |  saga:{tenant}:{n}  |  |  Position Keeping   |
|                     |  |                     |  |  Fin. Accounting    |
|                     |  |                     |  |  Valuation Engine   |
+---------------------+  +---------------------+  +---------------------+
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
        |
        v
+-------------------+
| Check Redis Cache |
+--------+----------+
         |
    +----+----+
    | HIT?    |
    +----+----+
         |
    YES  |  NO
    +----+  +----+
    |            |
    v            v
 Return     +----------------+
 Cached     | Load from DB   |
            +------+---------+
                   |
                   v
            +----------------+
            |   Compile      |
            |  Starlark      |
            +------+---------+
                   |
                   v
            +----------------+
            | Store Redis    |
            |  (with TTL)    |
            +------+---------+
                   |
                   v
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
+-------------------------------------------------------------+
|               Saga Resolution Order                          |
|                                                              |
|  Connection: SET search_path = tenant_schema                |
|                                                              |
|  1. Tenant Override    saga_definitions WHERE               |
|                        name = :saga_name AND                |
|                        status = 'ACTIVE'                    |
|                        AND is_system = FALSE                |
|                        ORDER BY version DESC                |
|                        LIMIT 1                              |
|                                                              |
|  2. Platform Default   saga_definitions WHERE               |
|                        name = :saga_name AND                |
|                        status = 'ACTIVE'                    |
|                        AND is_system = TRUE                 |
|                        ORDER BY version DESC                |
|                        LIMIT 1                              |
|                                                              |
|  3. Not Found          Return error                         |
+-------------------------------------------------------------+
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
| Accounts | `resolve_account("clearing", "GBP")` | Internal Account service |
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
+-----------------------------------------------------------------------------+
|                             DRAFT Phase                                      |
|                                                                              |
|  Trigger: CreateDraft(), UpdateDefinition()                                 |
|                                                                              |
|  Actions:                                                                    |
|    1. Parse Starlark script                                                 |
|    2. Extract all references (step handlers, instruments, accounts, etc.)   |
|    3. Validate each reference exists                                        |
|    4. Store warnings for missing/deprecated references                      |
|    5. Populate saga_references table                                        |
|                                                                              |
|  Outcome: Save succeeds with warnings; activation blocked if errors         |
+-----------------------------------------------------------------------------+
                                     |
                                     v
+-----------------------------------------------------------------------------+
|                          ACTIVATION Phase                                    |
|                                                                              |
|  Trigger: ActivateSaga()                                                    |
|                                                                              |
|  Actions:                                                                    |
|    1. Re-validate ALL references (state may have changed since DRAFT)       |
|    2. Check step handlers exist in registry                                 |
|    3. Check instruments are ACTIVE (not DRAFT or DEPRECATED)                |
|    4. Check accounts exist and are active                                   |
|    5. Check referenced sagas are ACTIVE                                     |
|                                                                              |
|  Outcome: Hard failure if any reference invalid; activation blocked         |
+-----------------------------------------------------------------------------+
                                     |
                                     v
+-----------------------------------------------------------------------------+
|                            RUNTIME Phase                                     |
|                                                                              |
|  Trigger: Execute()                                                          |
|                                                                              |
|  Actions:                                                                    |
|    1. Load saga (should be cached and pre-validated)                        |
|    2. On each step, verify handler still registered                         |
|    3. On instrument/account resolution, verify still valid                  |
|                                                                              |
|  Outcome: Fail fast with actionable error message                           |
|           Include: what's missing, where in script, suggested fix           |
+-----------------------------------------------------------------------------+
```

#### Validation Feedback Format

```text
Saga: withdrawal.star (DRAFT)
Tenant: acme-corp

Validation Results:
===================

Starlark Syntax
  [OK] Script parses successfully
  [OK] 3 steps defined
  [OK] All steps have compensation defined

Step Handlers
  [OK] position_keeping.initiate_log .......... exists
  [OK] financial_accounting.post_entries ...... exists
  [X]  notification.send_sms .................. NOT FOUND
       +- Available alternatives: notification.send, notification.send_email

Instrument References
  [OK] GBP .................................. ACTIVE (v1)
  [!]  KWH .................................. DEPRECATED
       +- Successor available: KWH-V2 (ACTIVE)
       +- Consider updating before activation

Account References
  [OK] clearing/GBP ......................... exists
  [OK] tax_withholding/GBP .................. exists

--------------------------------------------------
Status: BLOCKED - Cannot activate
        1 error, 1 warning

Errors (must fix):
  * Step handler 'notification.send_sms' not found (line 47)

Warnings (recommended):
  * Instrument 'KWH' is deprecated; successor 'KWH-V2' available
```

#### Deprecation Cascade Detection

When deprecating an instrument, account, or saga, check dependencies:

```text
Request: Deprecate instrument KWH (version 1)

Impact Analysis:
================

Active sagas referencing KWH:1
------------------------------
  * energy_settlement.star v2 (tenant: ACME_ENERGY)
      +- Line 23: resolve_instrument("KWH")
      +- Line 45: valuation_engine.valuate(instrument="KWH", ...)

  * meter_reconciliation.star v1 (tenant: ACME_ENERGY)
      +- Line 12: ctx.instrument == "KWH"

  * wholesale_settlement.star v3 (SYSTEM)
      +- Line 67: position_keeping.initiate_log(instrument="KWH", ...)

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

##### Saga Activation -> Instrument Schema

When activating a saga, extract attribute key accesses and validate against
the instrument definition:

```text
Saga: energy_settlement.star (DRAFT -> ACTIVE)

Attribute References Extracted:
================================
  * Line 23: ctx.position.attributes["gsp_code"]
  * Line 24: ctx.position.attributes["dno_code"]
  * Line 31: ctx.position.attributes["settlement_period"]

Validating against instrument: KWH
----------------------------------
  [OK] gsp_code ......................... defined (type: string)
  [OK] dno_code ......................... defined (type: string)
  [X]  settlement_period ................ NOT DEFINED
       +- Available attributes: gsp_code, dno_code, meter_type, profile_class
       +- Did you mean: 'settlement_date'?

Status: BLOCKED - Cannot activate
        Saga references attribute 'settlement_period' not defined in instrument KWH
```

##### Instrument Update -> Saga Dependencies

When modifying an instrument's attribute schema, check for dependent sagas:

```text
Request: Remove attribute 'gsp_code' from instrument KWH

Impact Analysis:
================

Active sagas referencing KWH.attributes["gsp_code"]:
----------------------------------------------------
  * energy_settlement.star v2 (tenant: ACME_ENERGY)
      +- Line 23: ctx.position.attributes["gsp_code"]

  * wholesale_reconciliation.star v1 (SYSTEM)
      +- Line 45: ctx.position.attributes["gsp_code"]

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
+-----------------------------------------------------------------------------+
|                        Party Hierarchy Example                               |
|                                                                              |
|                      +---------------------+                                |
|                      |  ACME Corp (ORG)    |                                |
|                      |  party_id: P001     |                                |
|                      +----------+----------+                                |
|                                 |                                           |
|              +------------------+------------------+                       |
|              |                  |                  |                        |
|     +--------v--------+ +------v-------+ +-------v-------+                |
|     | ACME UK (ORG)   | | ACME DE (ORG)| | ACME FR (ORG) |                |
|     | party_id: P002  | | party_id:P003| | party_id: P004|                |
|     +--------+--------+ +--------------+ +---------------+                |
|              |                                                              |
|     +--------+--------+                                                    |
|     |                 |                                                    |
|+----v-----+  +-------v--------+                                           |
|| John Doe |  | Jane Smith     |                                           |
||(INDIV)   |  | (INDIV)        |                                           |
||P005      |  | P006           |                                           |
|+----------+  +----------------+                                           |
+-----------------------------------------------------------------------------+
```

#### Scope Rules by Party Type

| Party Type | Visible Data |
|------------|--------------|
| **Individual** | Own positions, accounts, transactions only |
| **Organisation** | Own + all descendant parties (recursive) |
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

Organisation parties can run sagas that aggregate across their hierarchy:

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

#### Cross-Party Posting Authorisation

**Key distinction**: Read isolation != Write authorisation.

A saga executing under party A may create ledger entries affecting party B
when authorised by relationship:

```text
+-----------------------------------------------------------------------------+
|                    Cross-Party Posting Examples                             |
+-----------------------------------------------------------------------------+
|                                                                             |
|  ENERGY SETTLEMENT                                                          |
|  -----------------                                                          |
|  Market Operator (party A) settles trade:                                   |
|    * Generator (party B) sells 100 MWh                                      |
|    * Retailer (party C) buys 100 MWh                                        |
|                                                                             |
|  Saga executes as: Market Operator (A)                                      |
|  Postings created:                                                          |
|    DEBIT  Generator (B)  position: -100 MWh                                |
|    CREDIT Retailer (C)   position: +100 MWh                                |
|    DEBIT  Retailer (C)   cash: -$5,000                                     |
|    CREDIT Generator (B)  cash: +$5,000                                     |
|                                                                             |
+-----------------------------------------------------------------------------+
|                                                                             |
|  WEALTH MANAGEMENT                                                          |
|  -----------------                                                          |
|  Custodian (party A) executes client transfer:                             |
|    * Client 1 (party B) transfers $10,000                                  |
|    * Client 2 (party C) receives $10,000                                   |
|                                                                             |
|  Saga executes as: Custodian (A)                                           |
|  Postings created:                                                          |
|    DEBIT  Client 1 (B)  cash: -$10,000                                     |
|    CREDIT Client 2 (C)  cash: +$10,000                                     |
|                                                                             |
+-----------------------------------------------------------------------------+
```

#### Read vs Write Authorisation Model

| Operation | Scope Rule |
|-----------|------------|
| **READ positions** | Party hierarchy only (self + descendants) |
| **READ accounts** | Party hierarchy only |
| **READ transactions** | Party hierarchy only |
| **WRITE postings** | Contextual lookup - saga resolves target from input data |
| **WRITE positions** | Contextual lookup - saga resolves target from input data |

#### Cross-Party Authorisation: Contextual Lookup Model

> **Note**: The Party Service currently has `party_association` for personal
> relationships (SPOUSE, DEPENDENT, GUARANTOR). Operational authorisation
> (OPERATOR, CUSTODIAN, BROKER) is **not yet implemented**.

Rather than rigid party-to-party relationship tables, authorisation flows from
**contextual lookup** using the position's flexible attributes:

```text
+-----------------------------------------------------------------------------+
|                    Position Attributes -> Account Resolution                  |
+-----------------------------------------------------------------------------+
|                                                                             |
|  Position (from Position Keeping) with tenant-defined attributes:           |
|    {                                                                        |
|      "id": "pos-123",                                                       |
|      "party_id": "P-CUST-001",                                             |
|      "instrument_code": "KWH",                                              |
|      "quantity": 100,                                                       |
|      "attributes": {                   # Tenant-defined, flexible           |
|        "customer_party_id": "P-CUST-001",                                  |
|        "gsp_code": "P",                # Energy tenant uses this           |
|        "settlement_period": "2026-01-15/HH23",                             |
|      }                                                                      |
|    }                                                                        |
|                                                                             |
|  Saga Context Resolution (using position attributes):                       |
|    1. ctx.position.party_id -> current_account.by_party()                   |
|       Result: Customer's current account                                    |
|                                                                             |
|    2. ctx.position.attributes -> internal_account.by_attributes()      |
|       Result: Account matching those attributes                             |
|                                                                             |
|  Authorisation is IMPLICIT:                                                 |
|    - Saga declares which lookup types it may use                           |
|    - Runtime validates lookups against declaration                          |
|    - Posting targets come from resolved lookups, not arbitrary IDs         |
|                                                                             |
+-----------------------------------------------------------------------------+
```

#### Saga Authorised Lookups

Each saga declares what account resolution patterns it may use. Lookups are
**generic** - attribute keys are tenant-defined:

```python
# settlement.star - works for any tenant's attribute schema
saga(
    name = "settlement",
    version = "1.0.0",
    authorized_lookups = [
        "current_account.by_party",           # Can resolve party -> account
        "internal_account.by_attributes", # Can resolve attributes -> account
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
                        account_id = internal_account.by_attributes(
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
    // Check if saga is authorised for this lookup type
    if !contains(sagaDef.AuthorizedLookups, lookupType) {
        return nil, fmt.Errorf("saga %s not authorised for lookup type %s", sagaDef.Name, lookupType)
    }

    // Perform the lookup - attribute keys are tenant-defined
    switch lookupType {
    case "current_account.by_party":
        return r.currentAccountClient.GetByParty(ctx, key.(uuid.UUID))
    case "internal_account.by_attributes":
        // Generic lookup - matches against attributes JSONB
        attrs := key.(map[string]any)
        return r.internalAccountClient.GetByAttributes(ctx, attrs)
    }
}

// Internal Account: generic attribute matching
func (s *InternalAccountService) GetByAttributes(ctx context.Context, attrs map[string]any) (*Account, error) {
    query := s.db.Model(&InternalAccount{})
    // Build query dynamically from whatever attributes are passed
    for k, v := range attrs {
        query = query.Where("attributes @> ?", map[string]any{k: v})
    }
    var account InternalAccount
    return &account, query.First(&account).Error
}
```

#### Optional: Explicit Party Relationships

For use cases requiring explicit authorisation tracking (audit, compliance), an
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

This is **optional** - the contextual lookup model provides authorisation implicitly through saga definition.

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

### 5.8 Starlark Builtins Reference

Functions available within Starlark scripts:

#### Core Builtins Reference

| Builtin | Signature | Description |
|---------|-----------|-------------|
| `saga()` | `saga(name, version, steps, preconditions=None)` | Define a saga workflow |
| `step()` | `step(name, action, params, compensation=None)` | Define a saga step |
| `posting()` | `posting(account_id, direction, amount, description=None)` | Create ledger posting instruction |
| `cel_eval()` | `cel_eval(expression, context) -> value` | Evaluate CEL expression |
| `resolve_account()` | `resolve_account(purpose, currency) -> account_id` | Lookup internal account by purpose |
| `resolve_instrument()` | `resolve_instrument(code, version=None) -> instrument` | Lookup instrument definition |
| `invoke_saga()` | `invoke_saga(name, version=None, context={}) -> result` | Invoke child saga |
| `valuate()` | `valuate(instrument, quantity, context_type) -> valuation` | Call Valuation Engine (single context) |
| `valuate_batch()` | `valuate_batch(instrument, quantity, context_types[]) -> Dict[context_type, valuation]` | Valuate same basis across multiple contexts; returns dictionary keyed by context_type (e.g., `results["RETAIL"]`, `results["WHOLESALE"]`) |
| `fail()` | `fail(message)` | Abort saga with error message |
| `log()` | `log(level, message, **fields)` | Emit structured log entry |
| `Decimal()` | `Decimal(string) -> Decimal` | Financial-precision decimal type (FR-27). Supports `+`, `-`, `*`, `/`. Backed by `shopspring/decimal` |
| `ctx.new_uuid()` | `ctx.new_uuid() -> UUID` | Deterministic Version 5 UUID (namespace=saga_id, name=step:call). Stable across replays |
| `ctx.emit_progress()` | `ctx.emit_progress(message, percentage)` | Non-blocking progress update (0-100%). Published to Kafka `saga.progress.{tenant_id}` for UI consumption |
| `ctx.suspend()` | `ctx.suspend(idempotency_key, timeout=None) -> void` | Suspend saga waiting for external event. Releases lease, status -> WAITING_FOR_EVENT. Resume via `CompleteSagaStep` gRPC |
| `verify_external_state()` | `verify_external_state(handler, check_fn) -> bool` | Pre-Step Check for non-idempotent external handlers. Required before EXTERNAL_NOT_SUPPORTED calls |

#### Data Access Builtins

| Builtin | Signature | Description |
|---------|-----------|-------------|
| `position_keeping.list()` | `list(party_id=None, instrument=None) -> [Position]` | List positions (party-scoped) |
| `position_keeping.get()` | `get(position_id) -> Position` | Get single position (party-scoped) |
| `market_data.lookup()` | `lookup(dataset, resolution_key, knowledge_at=None) -> Observation` | Get market price |
| `party.get()` | `get(party_id) -> Party` | Get party details (scope-checked) |

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

### 5.9 Example Saga Definition

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

### 5.10 Saga Composition

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
----------------------
Step 1: validate_positions [OK]
Step 2: process_fees (invoke_saga -> fee_calculation)
    +- Child Saga: fee_calculation
       Step 2.1: calculate_fee [OK]
       Step 2.2: record_fee [OK]
Step 3: post_ledger [X] FAILED

Compensation Cascade:
--------------------
Step 3: post_ledger - no compensation (never completed)
Step 2: process_fees - compensate child saga
    +- Child Saga: fee_calculation (compensating)
       Step 2.2: record_fee -> REVERSE [OK]
       Step 2.1: calculate_fee -> REVERSE [OK]
Step 1: validate_positions - compensate [OK]
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
----------------------------------------
saga_a.star -> invoke_saga("saga_b")
saga_b.star -> invoke_saga("saga_c")
saga_c.star -> invoke_saga("saga_a") <- CYCLE

Cannot activate saga_c: would create circular dependency.
```

#### Composition Depth Limit

| Constraint | Value | Rationale |
|------------|-------|-----------|
| Max nesting depth | 5 | Prevent deep call stacks |
| Max total steps | 50 | Limit execution complexity |
| Child saga timeout | Inherited from parent | Prevent runaway children |

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
    // - load()      -> No module imports
    // - print()     -> Replaced with audit-routed version above
    // - time.now()  -> Use ctx.knowledge_at instead
    // - random()    -> Non-deterministic, forbidden
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

### 6.3 Step Handler Authorisation

- Handlers are platform-controlled Go functions
- Starlark cannot invoke arbitrary code
- New handlers require platform deployment and review

---

## 7. Existing Saga Mapping

### Current Go Sagas -> Starlark Definitions

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
| `payment_orchestrator_test.go` | `handlers/payment_test.go` | Step handler behaviour (Go code, unchanged) |
| `withdrawal_orchestrator_test.go` | `handlers/current_account_test.go` | Step handler behaviour (Go code, unchanged) |
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
| **Party Service** | `party` table with PERSON/ORGANIZATION types | Exists | Core party identity |
| **Party Service** | `party_association` for personal relationships | Exists | SPOUSE, DEPENDENT, GUARANTOR, etc. |
| **Party Service** | Party hierarchy (org -> child parties) | Partial | Need to verify recursive query support |
| **Current Account** | `account.party_id` reference | Exists | Links account to party (not FK) |
| **Current Account** | Account lookup by party | Exists | `current_account.by_party()` |
| **Internal Account** | `attributes` JSONB column | Exists | Can store GSP, DNO, etc. |
| **Internal Account** | Lookup by attributes | Partial | May need index/API |
| **Reference Data** | Instrument definitions with lifecycle | Exists | Pattern to follow |
| **Position Keeping** | Position with `party_id` | Exists | Core position model |
| **Market Information** | Bi-temporal observations | Exists | `knowledge_at` support |

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
| **Internal Account** | Lookup by GSP code | P1 | `by_attributes(gsp="P")` |

### Integration Points Requiring Coordination

| Integration | Services Involved | Dependency |
|-------------|-------------------|------------|
| Party scope resolution | Party Service <-> Saga Runtime | Runtime calls Party Service to resolve hierarchy |
| Account lookup | Current Account <-> Saga Runtime | Runtime calls Current Account for party's accounts |
| Internal account lookup | Internal Account <-> Saga Runtime | Runtime calls IBA for GSP/DNO accounts |
| Position access | Position Keeping <-> Saga Runtime | Step handlers query positions with party scope |
| Valuation (future) | Valuation Engine <-> Saga Runtime | `valuate()` step handler |

### Flexible Attribute Model for Account Resolution

Position attributes are **tenant-defined** via the asset class model. Sagas
use these attributes for account resolution without hardcoding attribute keys:

```text
+-----------------------------------------------------------------------------+
|                    Tenant-Defined Attribute Examples                         |
+-----------------------------------------------------------------------------+
|                                                                             |
|  ENERGY TENANT:                                                             |
|    position.attributes = {                                                  |
|      "gsp_code": "P",           # Grid Supply Point                        |
|      "dno_code": "WPD",         # Distribution Network Operator            |
|      "settlement_period": "HH23"                                           |
|    }                                                                        |
|                                                                             |
|  WEALTH TENANT:                                                             |
|    position.attributes = {                                                  |
|      "custodian_id": "CUST-001", # Custodian                               |
|      "sub_account": "TRADING",   # Account classification                  |
|    }                                                                        |
|                                                                             |
|  CARBON TENANT:                                                             |
|    position.attributes = {                                                  |
|      "vintage": "2024",          # Credit vintage year                     |
|      "registry": "VERRA",        # Carbon registry                         |
|      "project_id": "VCS-1234"                                              |
|    }                                                                        |
|                                                                             |
+-----------------------------------------------------------------------------+
```

| Lookup Type | Method | Notes |
|-------------|--------|-------|
| Party -> Account | `current_account.by_party(party_id)` | Standard party lookup |
| Attributes -> Account | `internal_account.by_attributes(attrs)` | Generic JSONB matching |
| Party details | `party.get(party_id)` | Scope-checked party lookup |

**Required**: Internal Account service needs generic attribute-based lookup API:

- Input: `map[string]any` (tenant-defined keys)
- Query: JSONB `@>` containment or key matching
- Index: GIN index on `attributes` column for performance

---

## 9. Parallel Work Streams

This PRD covers Streams 1-6 of the Starlark Saga Orchestration implementation.
The streams can be parallelized with dependencies as shown below.

### Stream Dependency Graph

```text
                    +---------------+
                    |   Stream 1    |
                    |    Schema     |
                    | (SAGA-001,016)|
                    +-------+-------+
                            |
            +---------------+---------------+
            |               |               |
            v               v               v
    +---------------+ +---------------+ +---------------+
    |   Stream 2    | |   Stream 3    | |   Stream 4    |
    |    Runtime    | |   Registry    | |  Validation   |
    | (SAGA-003,004)| | (SAGA-002,005 | | (SAGA-017-021)|
    |               | |  006,007,013) | |    SAGA-048   |
    +-------+-------+ +-------+-------+ +-------+-------+
            |               |               |
            +-------+-------+               |
                    |                       |
                    v                       |
            +---------------+               |
            |   Stream 5    |<--------------+
            | Party Isolat. |
            | (SAGA-022-025)|
            +-------+-------+
                    |
                    v
            +---------------+
            |   Stream 6    |
            |  Composition  |
            | (SAGA-026-030)|
            +---------------+
                    |
                    v
            [Companion PRD: Durable Execution Engine]
            (Streams 7-10)
```

### Stream 1: Schema Foundation

**Dependencies:** None (starting point)

| Task ID | Description | Priority | Owner |
|---------|-------------|----------|-------|
| **SAGA-001** | Create `saga_definitions` table in Reference Data | P0 | TBD |
| **SAGA-016** | Create `saga_references` table | P0 | TBD |

### Stream 2: Starlark Runtime

**Dependencies:** Stream 1 (partial - can start in parallel, needs schema for integration)

| Task ID | Description | Priority | Owner |
|---------|-------------|----------|-------|
| **SAGA-003** | Integrate `go.starlark.net` runtime | P0 | TBD |
| **SAGA-004a** | Implement Starlark Decimal extension (FR-27: operator overloading for financial maths) | P0 | TBD |
| **SAGA-004b** | Implement time-injection logic (strip `time.now()`, inject `ctx.knowledge_at`) | P0 | TBD |
| **SAGA-004c** | Implement core builtins (`cel_eval`, `posting`, `resolve_account`, etc.) | P0 | TBD |
| **SAGA-072** | Implement external lookup result capture for replay safety (FR-34) | P0 | TBD |

### Stream 3: Registry and Caching

**Dependencies:** Stream 1 (SAGA-001)

| Task ID | Description | Priority | Owner |
|---------|-------------|----------|-------|
| **SAGA-002** | Implement `SagaRegistry` interface (CRUD + lifecycle) | P0 | TBD |
| **SAGA-005** | Create step handler registry with default handlers | P0 | TBD |
| **SAGA-006** | Implement Redis caching layer | P1 | TBD |
| **SAGA-007** | Implement tenant default resolution | P1 | TBD |
| **SAGA-013** | Seed platform default sagas | P0 | TBD |

### Stream 4: Reference Validation

**Dependencies:** Stream 1 (SAGA-016), Stream 2 (SAGA-003)

| Task ID | Description | Priority | Owner |
|---------|-------------|----------|-------|
| **SAGA-017** | Implement reference extraction from Starlark AST | P0 | TBD |
| **SAGA-018** | Implement DRAFT phase validation with warnings | P0 | TBD |
| **SAGA-019** | Implement ACTIVATION phase validation (hard fail) | P0 | TBD |
| **SAGA-020** | Implement deprecation impact analysis | P1 | TBD |
| **SAGA-021** | Add validation feedback API endpoint | P1 | TBD |
| **SAGA-048** | Add Logic/Physics Linter (warn on maths in Starlark, enforce Pre-Step Check) | P1 | TBD |
| **SAGA-071** | Enhance Logic/Physics Linter with semantic Decimal arithmetic detection (FR-33) | P1 | TBD |

### Stream 5: Party Isolation

**Dependencies:** Stream 2 (SAGA-003), Stream 4 (for validation integration)

| Task ID | Description | Priority | Owner |
|---------|-------------|----------|-------|
| **SAGA-022** | Implement party scope resolution from Party Service | P0 | TBD |
| **SAGA-023** | Add `ctx.party_scope` injection with immutability enforcement | P0 | TBD |
| **SAGA-024** | Implement `authorized_lookups` declaration and runtime enforcement | P0 | TBD |
| **SAGA-025** | Add `party_id` and `visible_parties` to saga execution audit log | P1 | TBD |
| **SAGA-073** | Implement Visibility Manifest pre-flight check (FR-35) | P0 | TBD |

### Stream 6: Saga Composition

**Dependencies:** Stream 5 (SAGA-023 for scope inheritance)

| Task ID | Description | Priority | Owner |
|---------|-------------|----------|-------|
| **SAGA-026** | Implement `invoke_saga()` builtin with scope inheritance | P1 | TBD |
| **SAGA-027** | Add circular saga reference detection (DRAFT + ACTIVATION) | P1 | TBD |
| **SAGA-028** | Add runtime circular detection via call stack | P1 | TBD |
| **SAGA-029** | Implement compensation cascade for child sagas | P1 | TBD |
| **SAGA-030** | Add composition depth and total steps limits | P1 | TBD |

### Parallelization Strategy

| Phase | Streams | Duration Estimate |
|-------|---------|-------------------|
| **Phase A** | Stream 1 + Stream 2 (start) | 1-2 weeks |
| **Phase B** | Stream 3 + Stream 4 (parallel) | 2-3 weeks |
| **Phase C** | Stream 5 (after 2+4 complete) | 1-2 weeks |
| **Phase D** | Stream 6 (after 5 complete) | 1-2 weeks |

**Total Core Implementation:** 5-9 weeks with parallel execution

---

## 10. Success Criteria

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
| **AC-PI-02** | Organisation party saga CAN read positions of descendant parties | Unit test: assert `position_keeping.list()` returns descendant positions |
| **AC-PI-03** | Saga `ctx.party_scope` is immutable | Unit test: assert mutation throws error |
| **AC-PI-04** | Cross-party posting governed by contextual lookup model (Section 5.7) | Integration test: posting to unrelated party fails |
| **AC-PI-05** | Authorised cross-party posting succeeds via contextual visibility rules | Integration test: contextual lookup resolves counterparty accounts correctly |
| **AC-PI-06** | Saga execution log includes `party_id` and `visible_parties` from contextual lookup | Unit test: verify audit fields reflect contextual visibility at execution time |
| **AC-PI-07** | Child saga inherits parent party scope (cannot escalate) | Unit test: `invoke_saga()` passes same `party_scope` |
| **AC-PI-08** | Query by `visible_parties` returns correct executions | Query test: GIN index query returns expected results |
| **AC-PI-09** | Visibility Manifest generated before saga transitions to RUNNING (FR-35) | Unit test: verify manifest extraction from AST before first step |
| **AC-PI-10** | Saga fails fast if executing party lacks visibility over manifest parties | Unit test: party without recursive visibility over referenced parties triggers pre-flight failure |
| **AC-PI-11** | Pre-flight failure includes actionable error message with missing parties | Unit test: error message contains "Party P002 lacks visibility over parties [P003, P007]" |

### Acceptance Criteria: Runtime & Replay Safety

| ID | Criterion | Test Method |
|----|-----------|-------------|
| **AC-RT-01** | External lookup results (`resolve_account`, `resolve_instrument`) captured for replay (FR-34) | Unit test: replay saga, verify lookup returns cached result not live query |
| **AC-RT-02** | Lookup called outside `step()` block is persisted in `saga_step_results` or `input_snapshot` | Unit test: call `resolve_account()` outside step, verify result in snapshot |
| **AC-RT-03** | Replay with different Reference Data returns original lookup result | Integration test: change account mapping, replay saga, verify original account ID used |

### Acceptance Criteria: Saga Composition

| ID | Criterion | Test Method |
|----|-----------|-------------|
| **AC-SC-01** | `invoke_saga()` executes child saga synchronously | Unit test: verify child completes before parent continues |
| **AC-SC-02** | Parent failure triggers child compensation (LIFO) | Integration test: fail step 3, verify step 2 child compensates |
| **AC-SC-03** | Circular saga references detected at ACTIVATION | Unit test: A->B->C->A fails activation |
| **AC-SC-04** | Circular saga references detected at RUNTIME (defence in depth) | Unit test: call stack check prevents re-entry |
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
| **AC-AV-02** | ACTIVATION fails if attribute not in instrument schema | Unit test: saga refs `["foo"]`, instrument has no `foo` -> blocked |
| **AC-AV-03** | ACTIVATION succeeds if all attributes exist in schema | Unit test: saga refs match instrument attributes -> activated |
| **AC-AV-04** | Instrument attribute removal blocked if saga depends on it | Integration test: remove attr -> error listing dependent sagas |
| **AC-AV-05** | Instrument attribute addition does not affect existing sagas | Unit test: add new attr -> no saga validation errors |
| **AC-AV-06** | `saga_references` tracks attribute refs with instrument code | Query test: find sagas using `KWH.attributes["gsp_code"]` |
| **AC-AV-07** | Validation feedback suggests similar attribute names | Unit test: ref `settlement_period`, suggest `settlement_date` |

### Acceptance Criteria: Type Safety (FR-27)

| ID | Criterion | Test Method |
|----|-----------|-------------|
| **AC-TR-01** | Starlark Decimal type supports `+`, `-`, `*`, `/` operators | Unit test: `Decimal("10.50") + Decimal("3.25")` = `Decimal("13.75")` |
| **AC-TR-02** | Decimal arithmetic matches `shopspring/decimal` precision | Unit test: compare Starlark result with Go `decimal.Decimal` for edge cases |
| **AC-TR-03** | Valuation handlers return Decimal type (not float) | Unit test: call `valuate()`, verify return type is Decimal |
| **AC-TR-04** | Float-to-Decimal conversion rejected (prevent precision loss) | Unit test: `Decimal(3.14)` throws error, must use `Decimal("3.14")` |

### Acceptance Criteria: Semantic Linter (FR-33)

| ID | Criterion | Test Method |
|----|-----------|-------------|
| **AC-SL-01** | Semantic linter warns on Decimal arithmetic (non-counter) | Unit test: script with `Decimal("10") * Decimal("0.05")`, verify WARNING |
| **AC-SL-02** | Semantic linter allows counter arithmetic (`i + 1`) | Unit test: script with loop counter, verify no warning |
| **AC-SL-03** | Linter enforcement configurable (WARNING at DRAFT, ERROR at ACTIVATION) | Config test: set strict mode, verify ACTIVATION blocked on warning |

---

## 11. Risks and Mitigations

| Risk | Impact | Likelihood | Mitigation |
|------|--------|------------|------------|
| Starlark performance insufficient | High | Low | CEL for hot path; Starlark for orchestration only |
| Tenant writes broken saga | Medium | Medium | Simulation mode required before activation |
| Redis cache failure | Medium | Low | Fallback to database; circuit breaker |
| Migration breaks existing flows | High | Medium | Shadow mode comparison; feature flags |
| Party scope resolution latency | Medium | Low | Cache party hierarchy; batch resolution |
| Circular saga references cause stack overflow | High | Low | Static analysis at DRAFT/ACTIVATION + runtime call stack check |

---

## 12. Appendix: Why Starlark?

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

## 13. Links

- [ADR-028: Starlark Saga Orchestration with CEL Valuation](../adr/0028-starlark-saga-cel-valuation.md)
- [ADR-014: Financial Instrument Reference Data](../adr/0014-financial-instrument-reference-data.md)
- [Companion PRD: Durable Execution Engine](./005-durable-execution-engine.md) - Streams 7-9
- [go.starlark.net](https://pkg.go.dev/go.starlark.net/starlark) - Starlark Go implementation
- [Starlark Language Spec](https://github.com/bazelbuild/starlark/blob/master/spec.md)
- [google/cel-go](https://github.com/google/cel-go) - CEL Go implementation
- [Party Service](../adr/0003-party-management.md) - Party hierarchy and relationships (cross-party authorisation)
