# PRD: Starlark Saga Orchestration

**Status:** Draft
**Version:** 1.0
**Author:** Architecture Team
**ADR Reference:** [ADR-028](../adr/0028-starlark-saga-cel-valuation.md)

## 1. Executive Summary

The Starlark Saga Orchestration system migrates hardcoded Go saga logic to runtime-configurable workflow definitions. Saga definitions are stored in the Reference Data service (alongside instrument definitions), cached in Redis for performance, and executed by a shared runtime.

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

**Key insight**: Starlark provides Python syntax familiarity while guaranteeing the safety properties required for financial workflows.

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

- **Requirement**: Saga definitions MUST be stored in Reference Data service with lifecycle management (DRAFT → ACTIVE → DEPRECATED)
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

---

## 4. Technical Architecture

### 4.1 System Context

```
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

### 4.2 Saga Definition Schema

```sql
CREATE TABLE saga_definitions (
    -- Identity
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id UUID NOT NULL,

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

    -- Timestamps
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    activated_at TIMESTAMPTZ,
    deprecated_at TIMESTAMPTZ,

    -- Successor for deprecation lineage
    successor_id UUID REFERENCES saga_definitions(id),

    -- Constraints
    UNIQUE(tenant_id, name, version),
    CHECK (status IN ('DRAFT', 'ACTIVE', 'DEPRECATED')),
    CHECK (char_length(script) <= 65536),  -- 64KB max
    CHECK (char_length(preconditions_expression) <= 4096)
);

CREATE INDEX idx_saga_definitions_lookup
    ON saga_definitions(tenant_id, name, status);

CREATE INDEX idx_saga_definitions_active
    ON saga_definitions(tenant_id, name)
    WHERE status = 'ACTIVE';
```

### 4.3 Redis Caching Strategy

**Cache Key Format:**
```
saga:compiled:{tenant_id}:{name}:{version}
```

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

```
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
| Definition updated | Delete `saga:compiled:{tenant}:{name}:*` |
| Definition activated | Delete and repopulate |
| Definition deprecated | Delete from cache |
| TTL expiry | Automatic eviction |

### 4.4 Tenant Default Resolution

```
┌─────────────────────────────────────────────────────────────┐
│               Saga Resolution Order                          │
│                                                              │
│  1. Tenant Override    saga_definitions WHERE               │
│                        tenant_id = :tenant AND              │
│                        name = :saga_name AND                │
│                        status = 'ACTIVE'                    │
│                        ORDER BY version DESC                │
│                        LIMIT 1                              │
│                                                              │
│  2. Platform Default   saga_definitions WHERE               │
│                        tenant_id = SYSTEM_TENANT AND        │
│                        name = :saga_name AND                │
│                        status = 'ACTIVE'                    │
│                        ORDER BY version DESC                │
│                        LIMIT 1                              │
│                                                              │
│  3. Not Found          Return error                         │
└─────────────────────────────────────────────────────────────┘
```

**System Tenant ID:** `00000000-0000-0000-0000-000000000000`

Platform-provided sagas use `is_system = true` and are seeded to each tenant's schema during provisioning (same pattern as system instruments).

### 4.5 Step Handler Registry

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

### 4.6 Starlark Builtins

Functions available within Starlark scripts:

| Builtin | Signature | Purpose |
|---------|-----------|---------|
| `cel_eval` | `cel_eval(expr, context) → value` | Evaluate CEL expression |
| `posting` | `posting(account_id, direction, amount, description) → Posting` | Create posting instruction |
| `resolve_account` | `resolve_account(purpose, currency) → account_id` | Lookup internal bank account |
| `step` | `step(name, action, params, compensation) → Step` | Define saga step |
| `saga` | `saga(name, version, steps, preconditions) → SagaDefinition` | Define saga |

### 4.7 Example Saga Definition

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

---

## 5. Security Constraints

### 5.1 Starlark Sandbox

| Constraint | Value | Rationale |
|------------|-------|-----------|
| Max script size | 64 KB | Prevent memory exhaustion |
| Max execution time | 5 seconds | Prevent runaway scripts |
| No `while` loops | Language design | Guaranteed termination |
| No recursion depth > 50 | Runtime limit | Prevent stack overflow |
| No file I/O | Language design | Sandboxed execution |
| No network access | Language design | No external calls |
| Deterministic | Language design | Reproducible execution |

### 5.2 CEL Constraints (from ADR-014)

| Constraint | Value |
|------------|-------|
| Max expression length | 4 KB |
| Max expression depth | 10 levels |
| Cost limit | 10,000 units |

### 5.3 Step Handler Authorization

- Handlers are platform-controlled Go functions
- Starlark cannot invoke arbitrary code
- New handlers require platform deployment and review

---

## 6. Implementation Tasks

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

---

## 7. Migration Strategy

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

---

## 8. Success Criteria

| Metric | Target | Measurement |
|--------|--------|-------------|
| **Correctness** | 100% parity with Go sagas | Shadow mode comparison |
| **Performance** | < 50ms saga load time (cached) | P99 latency |
| **Cache hit rate** | > 95% | Redis metrics |
| **Tenant adoption** | 3+ custom sagas within 90 days | Usage tracking |
| **Deployment reduction** | 0 deployments for tenant workflow changes | Release tracking |

---

## 9. Risks and Mitigations

| Risk | Impact | Likelihood | Mitigation |
|------|--------|------------|------------|
| Starlark performance insufficient | High | Low | CEL for hot path; Starlark for orchestration only |
| Tenant writes broken saga | Medium | Medium | Simulation mode required before activation |
| Redis cache failure | Medium | Low | Fallback to database; circuit breaker |
| Migration breaks existing flows | High | Medium | Shadow mode comparison; feature flags |

---

## 10. Appendix: Why Starlark?

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

> Saga definitions use Python syntax - specifically, a safe subset designed for workflow configuration. If you can write a Python function, you can write a saga. The platform guarantees your script will always terminate and cannot access files or networks.

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

## 11. Links

- [ADR-028: Starlark Saga Orchestration with CEL Valuation](../adr/0028-starlark-saga-cel-valuation.md)
- [ADR-014: Financial Instrument Reference Data](../adr/0014-financial-instrument-reference-data.md)
- [go.starlark.net](https://pkg.go.dev/go.starlark.net/starlark) - Starlark Go implementation
- [Starlark Language Spec](https://github.com/bazelbuild/starlark/blob/master/spec.md)
- [google/cel-go](https://github.com/google/cel-go) - CEL Go implementation
