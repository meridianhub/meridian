# PRD: Starlark Saga Orchestration (Core)

**Status:** Production-Ready
**Version:** 1.0
**Author:** Architecture Team
**ADR Reference:** [ADR-028](../adr/0028-starlark-saga-cel-valuation.md)
**Companion PRD:** [Durable Execution Engine](./durable-execution-engine.md)

---

## 1. Executive Summary

This PRD defines the **Starlark Saga Orchestration Core** - the "Brain" that enables
runtime-configurable workflow definitions. Saga definitions are stored in Reference
Data, cached in Redis, and executed by a sandboxed Starlark runtime.

### Scope

| In Scope (This PRD) | Out of Scope (See Companion PRD) |
|---------------------|----------------------------------|
| Starlark runtime integration | Durable execution / replay |
| Step handler registry | Lease-based pod claiming |
| Party isolation & composition | Async wait / external events |
| Reference validation & linting | Hot-fixing & zombie recovery |
| Decimal type for financial math | Causation/correlation propagation |

### The Problem Statement

Current saga orchestration is hardcoded in Go, creating operational bottlenecks:

| Pain Point | Business Impact |
|------------|-----------------|
| **1:1 position-to-posting coupling** | All tenants get identical ledger posting patterns |
| **Custom workflows require code changes** | 2-4 week lead time for tenant-specific logic |
| **Platform becomes bottleneck** | Engineering backlog blocks tenant operations |
| **No self-service** | Tenants cannot define operational workflows |

### The Solution

Saga definitions written in **Starlark** (a safe subset of Python):

- **Tenant-configurable**: Each tenant can override platform defaults
- **Hot-reloadable**: No deployment required for workflow changes
- **Auditable**: Versioned definitions with full history
- **Safe**: Guaranteed termination, sandboxed execution

---

## 2. BIAN Alignment

This capability extends multiple BIAN service domains by externalizing their
orchestration logic:

| Service Domain | Current Implementation | With Starlark |
|----------------|----------------------|---------------|
| Payment Order | `payment_orchestrator.go` | `payment_execution.star` |
| Current Account | `withdrawal_orchestrator.go` | `withdrawal.star`, `deposit.star` |
| Internal Bank Account | Clearing operations | `clearing_settlement.star` |
| *NEW* Settlement | N/A | `energy_settlement.star` |

Saga definitions become **Administrative Plan Records** - auditable configuration
that governs workflow execution.

---

## 3. Functional Requirements

### FR-1: Saga Definition Storage

- **Requirement**: Saga definitions MUST be stored in Reference Data service
  with lifecycle management (DRAFT → ACTIVE → DEPRECATED)
- **Pattern**: Follow `InstrumentDefinition` model from ADR-014
- **Constraint**: ACTIVE definitions are immutable; create new version to change

### FR-2: Starlark Runtime Execution

- **Requirement**: The system MUST execute Starlark scripts with guaranteed termination
- **Language**: Starlark (deterministic subset of Python - no while loops, no I/O)
- **Builtins**: Platform provides `cel_eval()`, `posting()`, `resolve_account()`, etc.

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

- **Requirement**: Every saga execution MUST be logged with bi-temporal context
- **Fields**: `saga_id`, `version`, `party_id`, `knowledge_at`, `effective_at`,
  `step_results`, `outcome`
- **Retention**: Follow service-specific audit retention policy

### FR-9: Reference Validation

- **Requirement**: Saga definitions MUST validate all external references
- **DRAFT phase**: Validation warnings (missing handler, unknown instrument)
- **ACTIVATION phase**: Validation errors block activation
- **References tracked**: Step handlers, instruments, accounts, CEL expressions

### FR-10: Deprecation Impact Analysis

- **Requirement**: Before deprecating an instrument or handler, system MUST
  identify dependent sagas
- **Output**: List of affected sagas by name and version
- **Blocking**: Cannot deprecate if ACTIVE sagas depend on it (unless forced)

### FR-11: Party-Level Data Isolation

- **Requirement**: Saga execution MUST be scoped to the initiating party
- **Mechanism**: `ctx.party_scope` injected at runtime, immutable
- **Enforcement**: All data access handlers filter by party scope
- **Audit**: Party scope recorded in execution log

### FR-12: Saga Composition

- **Requirement**: Sagas MUST be able to invoke other sagas via `invoke_saga()`
- **Scope inheritance**: Child saga inherits parent's party scope (cannot escalate)
- **Compensation cascade**: Parent failure triggers child compensation (LIFO order)
- **Circular detection**: Detected at ACTIVATION time and runtime (defense in depth)
- **Limits**: Max nesting depth = 5, max total steps = 50

### FR-13: Starlark Decimal Type

- **Requirement**: The Runtime MUST provide a custom Starlark type for Decimals
- **Problem**: Starlark natively supports `int`, `float`, `string` but financial math
  requires `shopspring/decimal` precision
- **Implementation**: Custom `Decimal` type with operator overloading (`+`, `-`, `*`, `/`)
- **Step handler contract**: All handlers returning monetary values MUST return `Decimal`
- **Prevention**: Eliminates "Rounding Drift" during saga execution

### FR-14: Semantic Logic/Physics Linter

- **Requirement**: The Linter SHALL be semantic, not just syntactic, for Decimal arithmetic
- **Detection**: Warn on arithmetic operators where operands are not simple counters
- **Suggested message**: "Financial math detected. Move to CEL Valuation Strategy."
- **Exemptions**: Counter arithmetic (`i + 1`), list indexing
- **Enforcement level**: WARNING at DRAFT, ERROR at ACTIVATION (configurable)

---

## 4. CEL Valuation: Context and Boundaries

> **Note**: CEL-based valuation is **out of scope** for this PRD but provides
> essential context for the orchestration/valuation boundary.

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
    valuations = valuate_batch(
        instrument = ctx.instrument,
        quantity = ctx.quantity,
        context_types = ["RETAIL", "WHOLESALE"],
    )
```

### 4.2 The Logic/Physics Boundary

| Layer | Responsibility | Language |
|-------|---------------|----------|
| **Saga (Logic)** | What steps to run, in what order, under what conditions | Starlark |
| **Valuation (Physics)** | How to calculate values, rates, conversions | CEL |

---

## 5. Technical Architecture

### 5.1 Component Overview

```text
┌─────────────────────────────────────────────────────────────────────────────┐
│                         SAGA ORCHESTRATION CORE                              │
├─────────────────────────────────────────────────────────────────────────────┤
│                                                                             │
│  ┌─────────────────┐    ┌─────────────────┐    ┌─────────────────┐         │
│  │  Reference Data │    │   Redis Cache   │    │ Step Handler    │         │
│  │  (Definitions)  │───▶│  (Compiled)     │◀───│ Registry        │         │
│  └─────────────────┘    └─────────────────┘    └─────────────────┘         │
│           │                      │                      │                   │
│           ▼                      ▼                      ▼                   │
│  ┌─────────────────────────────────────────────────────────────────────┐   │
│  │                     STARLARK RUNTIME                                 │   │
│  │  ┌─────────────┐  ┌─────────────┐  ┌─────────────┐  ┌────────────┐  │   │
│  │  │ VM Sandbox  │  │  Builtins   │  │ Party Scope │  │  Decimal   │  │   │
│  │  │ (Hardened)  │  │ (Whitelisted)│  │ (Injected)  │  │  Type      │  │   │
│  │  └─────────────┘  └─────────────┘  └─────────────┘  └────────────┘  │   │
│  └─────────────────────────────────────────────────────────────────────┘   │
│                                                                             │
└─────────────────────────────────────────────────────────────────────────────┘
```

### 5.2 Saga Definition Schema

```sql
CREATE TABLE saga_definitions (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),

    -- Identity
    name VARCHAR(64) NOT NULL,
    version INTEGER NOT NULL,
    tenant_id UUID,  -- NULL = platform default

    -- Content
    script TEXT NOT NULL,           -- Starlark source
    compiled_hash VARCHAR(64),      -- For cache invalidation

    -- Lifecycle
    status VARCHAR(16) NOT NULL DEFAULT 'DRAFT',
    -- DRAFT, ACTIVE, DEPRECATED

    -- Metadata
    description TEXT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    created_by VARCHAR(128),
    activated_at TIMESTAMPTZ,
    deprecated_at TIMESTAMPTZ,

    UNIQUE(name, version, tenant_id)
);

CREATE TABLE saga_references (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    saga_definition_id UUID NOT NULL REFERENCES saga_definitions(id),

    reference_type VARCHAR(32) NOT NULL,
    -- STEP_HANDLER, INSTRUMENT, ACCOUNT_PURPOSE, CEL_EXPRESSION, CHILD_SAGA, ATTRIBUTE

    reference_value VARCHAR(256) NOT NULL,
    -- e.g., "position_keeping.initiate_log", "KWH", "SUSPENSE"

    context JSONB,  -- Additional context (e.g., attribute key, instrument code for attributes)
    line_number INTEGER,

    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_saga_refs_by_type ON saga_references(reference_type, reference_value);
CREATE INDEX idx_saga_refs_by_def ON saga_references(saga_definition_id);
```

### 5.3 Starlark Builtins Reference

| Builtin | Signature | Description |
|---------|-----------|-------------|
| `saga()` | `saga(name, version, steps, preconditions=None)` | Define a saga workflow |
| `step()` | `step(name, action, params, compensation=None)` | Define a saga step |
| `posting()` | `posting(account_id, direction, amount, description=None)` | Create ledger posting |
| `cel_eval()` | `cel_eval(expression, context) → value` | Evaluate CEL expression |
| `resolve_account()` | `resolve_account(purpose, currency) → account_id` | Lookup internal bank account |
| `resolve_instrument()` | `resolve_instrument(code, version=None) → instrument` | Lookup instrument definition |
| `invoke_saga()` | `invoke_saga(name, version=None, context={}) → result` | Invoke child saga |
| `valuate()` | `valuate(instrument, quantity, context_type) → Decimal` | Call Valuation Engine |
| `valuate_batch()` | `valuate_batch(instrument, quantity, context_types[]) → Dict` | Multi-context valuation |
| `fail()` | `fail(message)` | Abort saga with error |
| `log()` | `log(level, message, **fields)` | Emit structured log entry |
| `Decimal()` | `Decimal(string) → Decimal` | Financial-precision decimal type |

### 5.4 Party Isolation Model

```text
┌─────────────────────────────────────────────────────────────────────────────┐
│                    PARTY SCOPE INJECTION                                     │
├─────────────────────────────────────────────────────────────────────────────┤
│                                                                             │
│  REQUEST: Execute saga for Party P                                          │
│                                                                             │
│  ┌─────────────────────────────────────────────────────────────────────┐   │
│  │  ctx.party_scope = {                                                 │   │
│  │      party_id: "P",                                                  │   │
│  │      visible_parties: ["P", "P-child-1", "P-child-2"],  // hierarchy │   │
│  │      is_organization: true,                                          │   │
│  │  }                                                                   │   │
│  └─────────────────────────────────────────────────────────────────────┘   │
│                                                                             │
│  ENFORCEMENT:                                                               │
│  - position_keeping.list() → filtered by visible_parties                    │
│  - party.get(X) → fails if X not in visible_parties                         │
│  - posting() → validates target account belongs to visible party            │
│                                                                             │
└─────────────────────────────────────────────────────────────────────────────┘
```

---

## 6. Security & Sandboxing

### 6.1 Implementation Guidance (go.starlark.net)

The Go implementation requires explicit hardening beyond Starlark's language-level safety.

#### Restricted Built-in Environment

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
    "Decimal":            starlark.NewBuiltin("Decimal", decimalBuiltin),
    // ... other builtins
}
```

#### Timeout and Cancellation

```go
func (r *Runtime) ExecuteSaga(ctx context.Context, def *SagaDefinition, input any) (*SagaResult, error) {
    // Enforce 5-second timeout
    ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
    defer cancel()

    thread := NewSagaThread(ctx, r.auditLogger)
    _, err := starlark.ExecFile(thread, def.Name, def.Script, SagaBuiltins)
    if errors.Is(err, context.DeadlineExceeded) {
        return nil, fmt.Errorf("saga exceeded 5s execution limit")
    }
    return result, err
}
```

#### Disabled Functions Reference

| Function | Status | Alternative |
|----------|--------|-------------|
| `load()` | **Blocked** | Use whitelisted builtins only |
| `print()` | **Redirected** | Routes to `AuditLogger` |
| `time.now()` | **Blocked** | Use `ctx.knowledge_at` |
| `random()` | **Blocked** | Use `ctx.new_uuid()` (deterministic) |
| File I/O | **Blocked** | No file access |
| Network | **Blocked** | Use step handlers for external calls |

### 6.2 Starlark Safety Properties

| Property | Mechanism | Guarantee |
|----------|-----------|-----------|
| Guaranteed termination | No `while` loops, recursion limits | Scripts always finish |
| No file I/O | Language design | Sandboxed execution |
| No network access | Language design | No external calls |
| Deterministic | Language design | Reproducible execution |

---

## 7. Parallel Work Streams

This PRD is designed for **6 parallel development streams**. Task Master should
create these as independent tracks with the dependency graph below.

### Stream Dependency Graph

```text
                    ┌─────────────────┐
                    │  Stream 1       │
                    │  SCHEMA         │
                    │  (Foundation)   │
                    └────────┬────────┘
                             │
              ┌──────────────┼──────────────┐
              │              │              │
              ▼              ▼              ▼
    ┌─────────────────┐ ┌─────────────────┐ ┌─────────────────┐
    │  Stream 2       │ │  Stream 3       │ │  Stream 4       │
    │  RUNTIME        │ │  REGISTRY       │ │  VALIDATION     │
    │  (VM/Builtins)  │ │  (CRUD/Cache)   │ │  (AST/Linting)  │
    └────────┬────────┘ └────────┬────────┘ └────────┬────────┘
             │                   │                   │
             └─────────┬─────────┘                   │
                       │                             │
                       ▼                             │
             ┌─────────────────┐                     │
             │  Stream 5       │◀────────────────────┘
             │  PARTY          │
             │  (Isolation)    │
             └────────┬────────┘
                      │
                      ▼
             ┌─────────────────┐
             │  Stream 6       │
             │  COMPOSITION    │
             │  (invoke_saga)  │
             └─────────────────┘
                      │
                      ▼
             ┌─────────────────┐
             │  MIGRATION      │  (See Companion PRD)
             │  (Go → Starlark)│
             └─────────────────┘
```

### Stream 1: Schema (Foundation)

**Owner:** 1 developer
**Blocks:** Streams 3, 5

| Task ID | Description | Priority |
|---------|-------------|----------|
| **SAGA-001** | Create `saga_definitions` table in Reference Data | P0 |
| **SAGA-016** | Create `saga_references` table | P0 |

### Stream 2: Runtime (VM/Builtins)

**Owner:** 2 developers
**Blocks:** Streams 4, 5, 6

| Task ID | Description | Priority |
|---------|-------------|----------|
| **SAGA-003** | Integrate `go.starlark.net` runtime | P0 |
| **SAGA-004a** | Implement Starlark Decimal extension (FR-13) | P0 |
| **SAGA-004b** | Implement time-injection logic (`ctx.knowledge_at`) | P0 |
| **SAGA-004c** | Implement core builtins (`cel_eval`, `posting`, etc.) | P0 |

### Stream 3: Registry (CRUD/Cache)

**Owner:** 1 developer
**Depends on:** Stream 1

| Task ID | Description | Priority |
|---------|-------------|----------|
| **SAGA-002** | Implement `SagaRegistry` interface (CRUD + lifecycle) | P0 |
| **SAGA-005** | Create step handler registry with default handlers | P0 |
| **SAGA-006** | Implement Redis caching layer | P1 |
| **SAGA-007** | Implement tenant default resolution | P1 |
| **SAGA-013** | Seed platform default sagas | P0 |

### Stream 4: Validation (AST/Linting)

**Owner:** 1 developer
**Depends on:** Stream 2

| Task ID | Description | Priority |
|---------|-------------|----------|
| **SAGA-017** | Implement reference extraction from Starlark AST | P0 |
| **SAGA-018** | Implement DRAFT phase validation with warnings | P0 |
| **SAGA-019** | Implement ACTIVATION phase validation (blocking) | P0 |
| **SAGA-020** | Add deprecation impact analysis query | P1 |
| **SAGA-021** | Add RUNTIME validation with actionable errors | P1 |
| **SAGA-048** | Add Logic/Physics Linter (warn on math in Starlark) | P1 |
| **SAGA-071** | Enhance Linter with semantic Decimal detection (FR-14) | P1 |

### Stream 5: Party Isolation

**Owner:** 1 developer
**Depends on:** Streams 1, 2, 3

| Task ID | Description | Priority |
|---------|-------------|----------|
| **SAGA-022** | Implement party hierarchy resolution service | P0 |
| **SAGA-023** | Add `party_scope` injection to saga context | P0 |
| **SAGA-024** | Implement `authorized_lookups` runtime enforcement | P0 |
| **SAGA-025** | Add `party_id` and `visible_parties` to execution log | P1 |

### Stream 6: Saga Composition

**Owner:** 1 developer
**Depends on:** Streams 2, 5

| Task ID | Description | Priority |
|---------|-------------|----------|
| **SAGA-026** | Implement `invoke_saga()` builtin with scope inheritance | P1 |
| **SAGA-027** | Add circular saga reference detection (DRAFT + ACTIVATION) | P1 |
| **SAGA-028** | Add runtime circular detection via call stack | P1 |
| **SAGA-029** | Implement compensation cascade for nested sagas | P1 |
| **SAGA-030** | Add nesting depth and total step limits | P1 |

---

## 8. Acceptance Criteria

### Core Orchestration

| ID | Criterion | Test Method |
|----|-----------|-------------|
| **AC-CO-01** | Starlark script executes within 5-second timeout | Unit test: long-running script terminates |
| **AC-CO-02** | `cel_eval()` returns correct valuation result | Unit test: compare with direct CEL evaluation |
| **AC-CO-03** | `posting()` creates valid ledger instruction | Unit test: validate posting structure |
| **AC-CO-04** | Saga definition cached in Redis after first load | Integration test: verify cache hit |
| **AC-CO-05** | Tenant override takes precedence over platform default | Unit test: create both, verify tenant wins |

### Reference Validation

| ID | Criterion | Test Method |
|----|-----------|-------------|
| **AC-RV-01** | DRAFT saves with warnings for missing references | Unit test: save succeeds, warnings returned |
| **AC-RV-02** | ACTIVATION fails with missing step handler | Unit test: activation rejected |
| **AC-RV-03** | ACTIVATION fails with DEPRECATED instrument | Unit test: activation rejected |
| **AC-RV-04** | Deprecation impact analysis lists dependent sagas | Unit test: deprecate shows dependents |

### Party Isolation

| ID | Criterion | Test Method |
|----|-----------|-------------|
| **AC-PI-01** | Individual party saga CANNOT read sibling positions | Unit test: assert empty result |
| **AC-PI-02** | Organization party saga CAN read descendant positions | Unit test: assert descendant positions returned |
| **AC-PI-03** | Saga `ctx.party_scope` is immutable | Unit test: mutation throws error |
| **AC-PI-04** | Child saga inherits parent party scope | Unit test: verify scope passed |

### Saga Composition

| ID | Criterion | Test Method |
|----|-----------|-------------|
| **AC-SC-01** | `invoke_saga()` executes child synchronously | Unit test: verify child completes before parent |
| **AC-SC-02** | Parent failure triggers child compensation (LIFO) | Integration test: verify compensation order |
| **AC-SC-03** | Circular references detected at ACTIVATION | Unit test: A→B→C→A fails |
| **AC-SC-04** | Nesting depth > 5 rejected | Unit test: 6-level nesting fails |

### Type Safety

| ID | Criterion | Test Method |
|----|-----------|-------------|
| **AC-TS-01** | Decimal type supports `+`, `-`, `*`, `/` | Unit test: arithmetic operations |
| **AC-TS-02** | Decimal matches `shopspring/decimal` precision | Unit test: edge case comparison |
| **AC-TS-03** | Semantic linter warns on Decimal arithmetic | Unit test: script with multiplication |
| **AC-TS-04** | Linter allows counter arithmetic (`i + 1`) | Unit test: no warning |

---

## 9. Risks and Mitigations

| Risk | Impact | Likelihood | Mitigation |
|------|--------|------------|------------|
| Starlark performance insufficient | High | Low | CEL for hot path; Starlark for orchestration |
| Tenant writes broken saga | Medium | Medium | Simulation mode required before activation |
| Redis cache failure | Medium | Low | Fallback to database; circuit breaker |

---

## 10. Appendix: Why Starlark?

### Comparison with Alternatives

| Option | Pros | Cons |
|--------|------|------|
| **Starlark** | Python syntax, guaranteed termination, Google-maintained | Learning curve |
| **CEL** | Fast, already in use | Not expressive enough for orchestration |
| **Lua** | Fast, embeddable | Less familiar syntax, Turing-complete |
| **JavaScript** | Familiar | Turing-complete, security concerns |
| **YAML/JSON** | Simple | Not expressive enough |

### The "Safe Python" Pitch

For tenant communication:

> Saga definitions use Python syntax - specifically, a safe subset designed for
> workflow configuration. If you can write a Python function, you can write a
> saga. The platform guarantees your script will always terminate and cannot
> access files or networks.

---

## 11. Links

- [ADR-028: Starlark Saga Orchestration](../adr/0028-starlark-saga-cel-valuation.md)
- [Companion PRD: Durable Execution Engine](./durable-execution-engine.md)
- [go.starlark.net](https://pkg.go.dev/go.starlark.net/starlark)
- [Starlark Language Spec](https://github.com/bazelbuild/starlark/blob/master/spec.md)
