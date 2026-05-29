---
name: adr-028-starlark-saga-cel-valuation
description: Tiered logic execution using Starlark for sagas and CEL for valuations
triggers:
  - Implementing tenant-configurable workflow orchestration
  - Adding dynamic ledger posting rules that vary by tenant
  - Defining valuation formulas without code deployment
  - Breaking 1:1 position-to-posting coupling for multi-tenant flexibility
instructions: |
  Use Starlark (go.starlark.net) for multi-step workflow orchestration (sagas, settlement runs).
  Use CEL (google/cel-go) for stateless expressions (validation, valuation, fungibility).
  Starlark scripts MUST call CEL-based valuation strategies for financial calculations.
  Store saga definitions in Reference Data service for tenant-specific versioning.
---

# 28. Starlark Saga Orchestration with CEL Valuation

Date: 2026-01-20

## Status

Accepted (PRD approved for baseline)

Supersedes the hand-written Go saga orchestration approach in
[ADR-0002](0002-microservices-per-bian-domain.md#amendment-saga-orchestration-pattern-2025-11-19) and
[ADR-0012](0012-lien-based-fund-reservation.md): saga step sequencing and LIFO compensation are now defined as
Starlark scripts (stored as saga definitions in reference-data) and executed by `shared/pkg/saga.StarlarkSagaRunner`,
rather than hardcoded in each service's Go domain layer.

## Context

[ADR-0014](0014-financial-instrument-reference-data.md) establishes CEL for validation and fungibility
expressions. However, saga orchestration remains hardcoded in Go, creating limitations:

### Current State: Hardcoded 1:1 Coupling

```go
// withdrawal_orchestrator.go:292-467 - RIGID
// One position log entry always creates exactly two ledger postings
saga.AddStep("post_ledger", func(ctx context.Context) error {
    // DEBIT customer account
    o.finAcctClient.CaptureLedgerPosting(ctx, debitRequest)
    // CREDIT clearing account
    o.finAcctClient.CaptureLedgerPosting(ctx, creditRequest)
    // No flexibility for tenant-specific requirements
})
```

### The Multi-Tenant Challenge

Different tenants require different posting patterns for the same business operation:

| Tenant | Withdrawal Posting Pattern | Use Case |
|--------|---------------------------|----------|
| Basic Bank | 2 postings: Customer DEBIT, Clearing CREDIT | Standard double-entry |
| Tax Jurisdiction | 3 postings: + Withholding Tax CREDIT | Regulatory withholding |
| Fee-Based | 4 postings: + Fee Income CREDIT, adjusted customer amount | Transaction fees |
| Four-Corner | 4 postings: Clearing house settlement model | Interbank clearing |

**Current limitation**: Adding a new posting pattern requires code changes, testing, and deployment.

### Why CEL Alone Is Insufficient

CEL excels at stateless expressions but cannot handle:
- Sequential step execution with compensation
- Conditional branching based on intermediate results
- Loops over dynamic posting lists
- State management across saga steps

### The "Golden Stack" Solution

Use each tool for its strength:

| Tool | Role | Responsibility |
|------|------|----------------|
| **Starlark** | The Conductor | Orchestration, branching, loops, step management |
| **CEL** | The Calculator | Formula execution, validation, numeric precision |

## Decision Drivers

* **Tenant flexibility**: Different posting patterns without code deployment
* **1:N posting support**: One position entry can generate multiple ledger postings
* **Separation of concerns**: Business logic (Starlark) vs financial physics (CEL)
* **Auditability**: Versioned saga definitions with full history
* **Performance**: CEL for high-frequency calculations (~100ns), Starlark for orchestration
* **Safety**: Both languages are sandboxed and deterministic; Starlark guarantees bounded
  execution via recursion depth limits and timeouts (no `while` loops by language design)
* **Bi-temporal consistency**: Valuation replay requires deterministic execution

## Considered Options

1. **Continue hardcoding sagas in Go**
2. **CEL-only with complex expression chaining**
3. **Starlark for sagas + CEL for valuations** (chosen)
4. **Full scripting language (Lua, JavaScript)**

## Decision Outcome

Chosen option: **Starlark for sagas + CEL for valuations**, because it provides the optimal
balance of flexibility, safety, and performance for a multi-tenant financial platform.

### Architecture Overview

```
┌──────────────────────────────────────────────────────────────────────────┐
│                        Reference Data Service                             │
│  ┌────────────────────────────────────────────────────────────────────┐  │
│  │  Saga Definitions (Starlark)                                       │  │
│  │  • Step sequencing              Per-tenant versioned               │  │
│  │  • posting_rules() function     Hot-reloadable                     │  │
│  │  • Compensation mapping         Auditable                          │  │
│  └────────────────────────────────────────────────────────────────────┘  │
│  ┌────────────────────────────────────────────────────────────────────┐  │
│  │  Valuation Rules (CEL)          Already in ADR-014                 │  │
│  │  • validation_expression        ~100ns execution                   │  │
│  │  • fungibility_expression       Compiled bytecode                  │  │
│  │  • valuation_expression (NEW)   Financial calculations             │  │
│  └────────────────────────────────────────────────────────────────────┘  │
└──────────────────────────────────────────────────────────────────────────┘
                                    │
                                    ▼
┌──────────────────────────────────────────────────────────────────────────┐
│                          Saga Runtime (Go)                                │
│  ┌─────────────────┐   ┌─────────────────┐   ┌─────────────────────────┐ │
│  │ Starlark VM     │   │ CEL Evaluator   │   │ Step Handler Registry   │ │
│  │ (go.starlark)   │   │ (existing)      │   │                         │ │
│  │                 │   │                 │   │ position_keeping.*      │ │
│  │ Executes:       │   │ Evaluates:      │   │ financial_accounting.*  │ │
│  │ • posting_rules │   │ • tax calc      │   │ repository.*            │ │
│  │ • step sequence │   │ • fee calc      │   │                         │ │
│  └─────────────────┘   └─────────────────┘   └─────────────────────────┘ │
└──────────────────────────────────────────────────────────────────────────┘
```

### Saga Definition Schema

> **Note**: Tenant isolation is achieved via PostgreSQL schema-per-tenant (search_path).
> No `tenant_id` column needed - each tenant's schema contains only their data.

```sql
-- Lives in Reference Data service (each tenant's schema)
CREATE TABLE saga_definitions (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),

    -- Identification
    name VARCHAR(64) NOT NULL,           -- "withdrawal", "deposit", "settlement"
    version INTEGER NOT NULL DEFAULT 1,

    -- Starlark script (the orchestration logic)
    script TEXT NOT NULL,
    script_hash VARCHAR(64) NOT NULL,    -- SHA-256 for cache invalidation

    -- Lifecycle
    status VARCHAR(16) NOT NULL DEFAULT 'DRAFT',  -- DRAFT, ACTIVE, DEPRECATED

    -- Validation tracking
    last_validated_at TIMESTAMPTZ,
    validation_errors JSONB,

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
    CHECK (script <> '')
);

CREATE INDEX idx_saga_definitions_lookup
    ON saga_definitions(name, status);

CREATE INDEX idx_saga_definitions_bitemporal
    ON saga_definitions(name, valid_from, valid_to);
```

### Validation Phases

Saga definitions pass through three validation phases with increasing strictness:

| Phase | Trigger | Strictness | Failures |
|-------|---------|------------|----------|
| **DRAFT** | On save | Warnings only | Store with warnings |
| **ACTIVATION** | Status → ACTIVE | Hard errors | Reject activation |
| **DEPRECATION** | Status → DEPRECATED | Impact analysis | Warn of dependents |

**DRAFT validation** (warnings):
- Syntax check (Starlark parses)
- Unknown step handler references
- Unreachable code paths
- Missing compensation handlers

**ACTIVATION validation** (errors):
- All step handlers must exist and be registered
- Attribute references must match instrument schema
- External handlers without idempotency support must have Pre-Step Check
- Circular saga references prohibited
- CEL expressions must compile

**DEPRECATION validation**:
- List all sagas that `invoke_saga()` this definition
- Warn operator of downstream impact

### Starlark Saga Example: Tenant-Configurable Withdrawal

```python
# sagas/withdrawal/tenant-with-tax.star
# Stored in reference-data service, loaded at runtime

def posting_rules(ctx):
    """Generate ledger postings for a withdrawal.

    Args:
        ctx: Execution context with amount, account, tenant config

    Returns:
        List of posting instructions for Financial Accounting service
    """
    postings = []
    remaining_amount = ctx.amount

    # 1. Withholding tax (if applicable)
    if ctx.tenant.requires_withholding_tax:
        # CEL for calculation - fast, auditable
        tax_amount = cel_eval(ctx.tenant.withholding_tax_expr, {
            "amount": ctx.amount,
            "account_type": ctx.account.type,
        })

        if tax_amount > 0:
            postings.append(posting(
                account_id = resolve_account("tax_withholding", ctx.currency),
                direction = "CREDIT",
                amount = tax_amount,
                description = "Withholding tax",
            ))
            remaining_amount = remaining_amount - tax_amount

    # 2. Transaction fee (if configured)
    if ctx.tenant.transaction_fee_expr:
        # CEL for fee calculation
        fee = cel_eval(ctx.tenant.transaction_fee_expr, {
            "amount": ctx.amount,
            "account_tier": ctx.account.tier,
        })

        if fee > 0:
            postings.append(posting(
                account_id = resolve_account("fee_income", ctx.currency),
                direction = "CREDIT",
                amount = fee,
                description = "Transaction fee",
            ))
            remaining_amount = remaining_amount - fee

    # 3. Customer account debit (net amount after deductions)
    postings.append(posting(
        account_id = ctx.account.id,
        direction = "DEBIT",
        amount = remaining_amount,
        description = "Withdrawal from account",
    ))

    # 4. Clearing account credit (always full original amount)
    postings.append(posting(
        account_id = resolve_account("clearing", ctx.currency),
        direction = "CREDIT",
        amount = ctx.amount,
        description = "Clearing credit",
    ))

    return postings


def compensation_rules(ctx, completed_postings):
    """Generate compensation postings to reverse completed work.

    Args:
        ctx: Original execution context
        completed_postings: List of postings that were successfully created

    Returns:
        List of compensating posting instructions (reversed directions)
    """
    compensations = []
    for p in completed_postings:
        compensations.append(posting(
            account_id = p.account_id,
            direction = "CREDIT" if p.direction == "DEBIT" else "DEBIT",
            amount = p.amount,
            description = "COMP: " + p.description,
        ))
    return compensations


# Saga definition
saga(
    name = "withdrawal",
    version = "2.0.0",

    steps = [
        step(
            name = "log_position",
            action = "position_keeping.initiate_log",
            params = lambda ctx: {
                "direction": "DEBIT",
                "amount": ctx.amount,
                "account_id": ctx.account.id,
            },
            compensation = "position_keeping.cancel_log",
        ),
        step(
            name = "post_ledger",
            action = "financial_accounting.post_entries",
            params = lambda ctx: {
                "postings": posting_rules(ctx),  # Dynamic 1:N posting generation
            },
            compensation = lambda ctx, result: {
                "postings": compensation_rules(ctx, result.completed_postings),
            },
        ),
        step(
            name = "save_account",
            action = "repository.save",
            compensation = None,  # No-op: last step
        ),
    ],

    # CEL preconditions (fast validation before saga starts)
    preconditions = [
        "ctx.amount > 0",
        "ctx.account.status == 'ACTIVE'",
        "ctx.account.available_balance >= ctx.amount",
    ],
)
```

### CEL Valuation Expressions

Extend the existing CEL compiler to support valuation expressions:

```sql
-- Add valuation_expression to instrument_definitions (extends ADR-014)
ALTER TABLE instrument_definitions
ADD COLUMN valuation_expression TEXT;

-- Example: Energy pricing with time-of-use tariffs
UPDATE instrument_definitions
SET valuation_expression = 'qty * lookup_tariff(attrs.tou_period, attrs.tariff_zone)'
WHERE code = 'KWH';

-- Example: Transaction fee calculation
-- Stored per-tenant in tenant_config
INSERT INTO tenant_valuation_rules (tenant_id, rule_name, cel_expression)
VALUES
    ('tenant-uuid', 'withholding_tax', 'amount * 0.15'),
    ('tenant-uuid', 'transaction_fee', 'max(1.00, amount * 0.001)');
```

### Go Runtime Implementation

```go
package saga

import (
    "context"

    "go.starlark.net/starlark"
    "github.com/google/cel-go/cel"
)

// SagaRuntime executes Starlark saga definitions with CEL valuation support.
type SagaRuntime struct {
    refDataClient  ReferenceDataClient
    celCompiler    *cel.Compiler
    stepHandlers   StepHandlerRegistry
    logger         *slog.Logger
}

// StepHandlerRegistry maps Starlark action names to Go implementations.
type StepHandlerRegistry struct {
    handlers map[string]StepHandler
}

// StepHandler is the Go function that executes a saga step.
type StepHandler func(ctx context.Context, params map[string]any) (StepResult, error)

// Execute runs a saga definition for a given tenant and input.
func (r *SagaRuntime) Execute(
    ctx context.Context,
    tenantID uuid.UUID,
    sagaName string,
    input ExecutionContext,
) (*SagaResult, error) {
    // 1. Load saga definition from reference data
    def, err := r.refDataClient.GetSagaDefinition(ctx, tenantID, sagaName)
    if err != nil {
        return nil, fmt.Errorf("load saga %s: %w", sagaName, err)
    }

    // 2. Create Starlark thread with CEL builtins
    thread := &starlark.Thread{
        Name: fmt.Sprintf("saga-%s-%s", sagaName, uuid.New().String()),
    }

    // 3. Register built-in functions
    builtins := r.createBuiltins(ctx, tenantID, input)
    // Available: cel_eval, posting, resolve_account, valuate, valuate_batch,
    //            ctx.new_uuid, invoke_saga, fail, log, position_keeping.*,
    //            market_data.*, party.*

    // 4. Execute Starlark script
    globals, err := starlark.ExecFile(thread, sagaName+".star", def.Script, builtins)
    if err != nil {
        return nil, fmt.Errorf("parse saga script: %w", err)
    }

    // 5. Extract saga definition from globals
    sagaDef := globals["saga"]

    // 6. Execute steps with compensation support
    return r.executeSteps(ctx, sagaDef, input)
}

// createBuiltins registers Go functions callable from Starlark.
// NOTE: Production implementation MUST add type assertion error handling:
//   str, ok := args[0].(starlark.String)
//   if !ok { return nil, fmt.Errorf("cel_eval: expected string, got %T", args[0]) }
func (r *SagaRuntime) createBuiltins(
    ctx context.Context,
    tenantID uuid.UUID,
    input ExecutionContext,
) starlark.StringDict {
    return starlark.StringDict{
        // CEL evaluation for calculations
        "cel_eval": starlark.NewBuiltin("cel_eval", func(
            thread *starlark.Thread,
            fn *starlark.Builtin,
            args starlark.Tuple,
            kwargs []starlark.Tuple,
        ) (starlark.Value, error) {
            expr := args[0].(starlark.String).GoString()
            params := toGoMap(args[1])

            result, err := r.celCompiler.Evaluate(ctx, expr, params)
            if err != nil {
                return starlark.None, err
            }
            return toStarlarkValue(result), nil
        }),

        // Create a posting instruction
        "posting": starlark.NewBuiltin("posting", createPostingBuiltin),

        // Resolve internal account
        "resolve_account": starlark.NewBuiltin("resolve_account", func(
            thread *starlark.Thread,
            fn *starlark.Builtin,
            args starlark.Tuple,
            kwargs []starlark.Tuple,
        ) (starlark.Value, error) {
            purpose := args[0].(starlark.String).GoString()
            currency := args[1].(starlark.String).GoString()

            accountID, err := r.resolveAccount(ctx, tenantID, purpose, currency)
            if err != nil {
                return starlark.None, err
            }
            return starlark.String(accountID), nil
        }),
    }
}
```

### Test Strategy

| Test Category | Coverage | Tools |
|---------------|----------|-------|
| **Starlark unit tests** | posting_rules(), compensation_rules() | Go test + Starlark interpreter |
| **CEL valuation tests** | Expression evaluation | Existing CEL test framework |
| **Saga orchestration** | Step execution, compensation order | Existing saga_test.go patterns |
| **Integration tests** | End-to-end with mocked services | Testcontainers |
| **Multi-tenant scenarios** | Different posting patterns per tenant | Parameterized tests |
| **Golden file tests** | Saga definition regression detection | Snapshot testing |

```go
// Example: Multi-tenant posting variant tests
func TestSagaExecution_MultiTenantPostingVariants(t *testing.T) {
    testCases := []struct {
        tenant           string
        sagaVersion      string
        expectedPostings int
        description      string
    }{
        {"default", "1.0.0", 2, "standard double-entry"},
        {"with-tax", "2.0.0", 3, "with withholding tax"},
        {"with-tax-and-fee", "2.1.0", 4, "with tax and fee"},
        {"four-corner", "3.0.0", 4, "clearing house model"},
    }

    for _, tc := range testCases {
        t.Run(tc.tenant, func(t *testing.T) {
            runtime := NewSagaRuntime(mockRefData, mockCEL, mockHandlers)

            result, err := runtime.Execute(ctx, tc.tenant, "withdrawal", input)

            require.NoError(t, err)
            assert.True(t, result.Success)
            assert.Len(t, capturedPostings, tc.expectedPostings)
        })
    }
}
```

### Security Constraints

Both Starlark and CEL are designed for safe execution of untrusted code:

| Constraint | Starlark | CEL |
|------------|----------|-----|
| Turing completeness | No (no while loops) | No |
| Infinite loops | Impossible | Impossible |
| File system access | None | None |
| Network access | None | None |
| Memory limits | Configurable | Configurable |
| Execution timeout | Configurable | Configurable |
| Determinism | 100% | 100% |

**Additional Meridian constraints:**

```go
const (
    MaxStarlarkScriptSize   = 64 * 1024  // 64KB
    MaxStarlarkExecutionMs  = 5000       // 5 seconds
    MaxCELExpressionLength  = 4096       // 4KB (from ADR-014)
    MaxCELExpressionDepth   = 10         // Nesting limit
    MaxCELCostLimit         = 10000      // Evaluation cost units
)
```

### Party Isolation

Sagas execute within a party scope, ensuring data isolation across organizational boundaries:

```python
# Runtime injects immutable party scope
ctx.party_scope = PartyScope(
    party_id = "P001",                    # Executing party
    party_type = "ORGANIZATION",          # INDIVIDUAL, ORGANIZATION
    visible_parties = ["P001", "P002"],   # Party + authorized children
)

# All lookups are automatically scoped
positions = position_keeping.list(party_scope=ctx.party_scope)  # Only visible positions
```

**Enforcement rules:**
- Runtime resolves party tree from Party Service before execution
- `ctx.party_scope` is immutable (attempts to modify raise error)
- Cross-party lookups require explicit `authorized_lookups` declaration in saga definition
- Audit log captures `party_id` and `visible_parties` for every execution

### Durable Execution via Replay

Sagas survive pod death through checkpoint-and-replay (mini-Temporal pattern):

```sql
-- Service-local tables (not in Reference Data)
CREATE TABLE saga_instances (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    saga_definition_id UUID NOT NULL,
    saga_name VARCHAR(64) NOT NULL,
    saga_version INTEGER NOT NULL,
    input_snapshot JSONB NOT NULL,
    party_id UUID NOT NULL,
    knowledge_at TIMESTAMPTZ,

    -- Lease-based claiming (race condition prevention)
    claimed_by_pod VARCHAR(128),
    claimed_at TIMESTAMPTZ,
    lease_expires_at TIMESTAMPTZ,

    -- Progress
    current_step_index INTEGER NOT NULL DEFAULT 0,
    replay_count INTEGER NOT NULL DEFAULT 0,
    status VARCHAR(32) NOT NULL DEFAULT 'PENDING',
    -- PENDING, RUNNING, COMPLETED, COMPENSATING, COMPENSATED, FAILED, FAILED_MANUAL_INTERVENTION

    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    completed_at TIMESTAMPTZ,
    error_message TEXT
);

-- Index for efficient orphaned saga detection (frequent query in multi-pod deployments)
CREATE INDEX idx_saga_instances_lease_expires
    ON saga_instances(lease_expires_at)
    WHERE status IN ('RUNNING', 'PENDING', 'COMPENSATING');

CREATE TABLE saga_step_results (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    saga_instance_id UUID NOT NULL REFERENCES saga_instances(id),
    step_index INTEGER NOT NULL,
    step_name VARCHAR(64) NOT NULL,
    idempotency_key VARCHAR(128) NOT NULL,  -- "saga_{id}_step_{index}"
    output_snapshot JSONB,
    status VARCHAR(16) NOT NULL,
    causation_id UUID NOT NULL,
    UNIQUE(saga_instance_id, step_index),
    UNIQUE(idempotency_key)
);
```

**Recovery flow:**
1. Pod B detects orphaned sagas: `WHERE lease_expires_at < NOW()`
2. Claims with `FOR UPDATE SKIP LOCKED` (no race conditions)
3. Replays Starlark from start; completed steps return cached results
4. Continues from first incomplete step

**Transaction affinity:** Step result INSERT and `current_step_index` UPDATE must be atomic.

### Determinism Requirements

Saga execution must be 100% deterministic for replay safety:

| Constraint | Enforcement |
|------------|-------------|
| No `time.now()` | Runtime blocks; use `ctx.knowledge_at` |
| No random numbers | Runtime blocks; use `ctx.new_uuid()` |
| No external I/O | Only via registered step handlers |
| Idempotent handlers | External integrations must declare capability |

```python
# WRONG: Non-deterministic
timestamp = time.now()  # Error: time access not allowed

# RIGHT: Deterministic
timestamp = ctx.knowledge_at  # Injected, stable across replays
ref_id = ctx.new_uuid()       # Version 5 UUID (namespace=saga_id, name=step:call)
```

### External Integration Guardrails

**Side-Effect Idempotency**: External integrations (payment gateways, etc.) must declare idempotency support:

```go
type StepHandler struct {
    Name        string
    Idempotency IdempotencyCapability  // INTERNAL, EXTERNAL_SUPPORTED, EXTERNAL_NOT_SUPPORTED
    Execute     func(ctx, params) (result, error)
}
```

- `EXTERNAL_NOT_SUPPORTED` handlers require "Pre-Step Check" pattern (query before mutation)
- Runtime provides `verify_external_state(handler, check_fn)` builtin
- ACTIVATION fails if non-idempotent handler used without Pre-Step Check
- Logic/Physics Linter enforces Pre-Step Check pattern

**Zombie Saga Detection & Hot-Fixing**: Sagas stuck in retry loops are detected and escalated:

- `replay_count` incremented on each replay attempt
- If `replay_count > MAX_REPLAYS` (default: 5) → status = `FAILED_MANUAL_INTERVENTION`
- High-severity (P1) alert triggered for operator intervention
- **Hot-fix via definition re-pointing** (bi-temporal compatible):
  1. Deploy fixed definition as new version (v2)
  2. Update instance's `saga_definition_id` to v2, reset `replay_count`
  3. On resume: cached step results are respected, failed step executes with v2 logic
  4. Audit trail preserves: which version used, when each step executed

**Dry-Run Testing**: Validate Starlark logic before deployment:

```go
// ExecuteDryRun runs saga with mocked handlers, returns execution plan
func (r *Runtime) ExecuteDryRun(ctx, sagaName, input) (*ExecutionPlan, error)
```

- No database writes
- Returns intended step sequence with parameters
- Validates attribute references against instrument schema

## Real Service Integration

### Service Binding Architecture

Starlark handlers bridge the dynamic scripting interface with strongly-typed gRPC services. Each service provides
a `RegisterStarlarkHandlers` function that registers its operations with the saga runtime.

**Pattern**: Service bindings live in `services/{service-name}/client/starlark.go` alongside the gRPC client:

```go
// services/current-account/client/starlark.go
func RegisterStarlarkHandlers(registry *saga.HandlerRegistry, client *Client) error {
    handlers := map[string]struct {
        handler  saga.Handler
        metadata saga.HandlerMetadata
    }{
        "current_account.create_lien": {
            handler: createLienHandler(client),
            metadata: saga.HandlerMetadata{
                Category:            saga.HandlerCategorySettlement,
                ProducesInstruments: []string{"USD", "EUR", "GBP", "NZD"},
            },
        },
    }

    for name, h := range handlers {
        if err := registry.RegisterWithMetadata(name, h.handler, &h.metadata); err != nil {
            return fmt.Errorf("failed to register %s: %w", name, err)
        }
    }
    return nil
}

func createLienHandler(client *Client) saga.Handler {
    return func(ctx *saga.StarlarkContext, params map[string]any) (any, error) {
        // 1. Parse Starlark params (map[string]any)
        accountID, err := saga.RequireStringParam(params, "account_id")
        amount, err := saga.RequireDecimalParam(params, "amount")

        // 2. Prepare context with saga metadata (idempotency, tracing)
        clientCtx := prepareClientContext(ctx)

        // 3. Build gRPC request
        req := &currentaccountv1.InitiateLienRequest{...}

        // 4. Call REAL gRPC client (not a mock!)
        resp, err := client.InitiateLien(clientCtx, req)

        // 5. Convert response to Starlark format
        return map[string]any{
            "lien_id": resp.GetLien().GetLienId(),
            "status":  "ACTIVE",
        }, nil
    }
}
```

### Dependency Injection Over Global Registry

Services explicitly declare their saga handler dependencies during initialization:

**Before (global registry - problematic)**:

```go
// OLD PATTERN - DEPRECATED
executor := saga.NewExecutor(saga.ExecutorConfig{
    Handlers: saga.DefaultRegistry(), // ❌ Global state, hidden dependencies
})
```

**After (explicit registration - current)**:

```go
// NEW PATTERN - RECOMMENDED
func main() {
    // Initialize service clients
    currentAccountClient, cleanup, _ := currentaccountclient.New(...)
    defer cleanup()

    posKeepingClient, cleanup2, _ := positionkeepingclient.New(...)
    defer cleanup2()

    // Create handler registry
    handlerRegistry := saga.NewHandlerRegistry()

    // Explicitly register service bindings
    currentaccountclient.RegisterStarlarkHandlers(handlerRegistry, currentAccountClient)
    positionkeepingclient.RegisterStarlarkHandlers(handlerRegistry, posKeepingClient)

    // Create saga runner with explicit dependencies
    sagaRunner := saga.NewStarlarkSagaRunner(saga.StarlarkSagaRunnerConfig{
        Handlers: handlerRegistry, // ✅ Explicit dependencies
        Logger:   logger,
    })
}
```

**Benefits**:

- Clear dependency graph (this service orchestrates these services)
- Easy to test (inject mock clients)
- No global state
- Graceful degradation (warn on missing services, don't fail)

### Conservation of Dimension Rule

The **Conservation of Dimension Rule** enforces type safety for financial instruments at the handler level:

> Handlers must declare `ProducesInstruments` metadata matching the instrument types they actually create
> in position-keeping. A handler that produces USD positions cannot declare EUR, preventing runtime type mismatches.

**Rationale**: Financial systems must enforce dimensional consistency. Just as you cannot add meters to kilograms
in physics, you cannot mix currencies or asset types without explicit conversion. This rule catches type errors
at handler registration time rather than in production.

**Example validation failure**:

```go
// BAD - Declaration doesn't match implementation
metadata: saga.HandlerMetadata{
    ProducesInstruments: []string{"USD"},  // Declared: USD only
}

// In handler implementation:
req := &positionkeepingv1.InitiateLogRequest{
    Currency: "EUR",  // MISMATCH! Creates EUR but declared USD
}

// Result: Validation error at registration time:
// "handler current_account.create_lien produced EUR but only declared [USD]"
```

**Handler categories**:

- `HandlerCategoryIngestion` - Creates positions from external data (meter readings, market prices)
  - Example: `ProducesInstruments: []string{"KWH", "GAS", "WATER"}`
- `HandlerCategoryValuation` - Computes derived values (mark-to-market, accruals)
  - Example: `ProducesInstruments: []string{}` (usually empty, updates existing)
- `HandlerCategorySettlement` - Executes financial operations (debits, credits, transfers)
  - Example: `ProducesInstruments: []string{"USD", "EUR", "GBP"}`

The validator enforces:

1. Declared instruments must match what's actually created in position-keeping
2. No instrument type mismatches between declaration and implementation
3. Multi-currency handlers must declare all supported currencies upfront

This ensures **financial instrument type safety** at compile time (handler registration), not at runtime when
data corruption could occur.

### Positive Consequences

* **Tenant flexibility**: Different posting patterns without code deployment
* **1:N posting support**: One position entry generates dynamic number of postings
* **Separation of concerns**: Business logic in Starlark, financial math in CEL
* **Hot-reloadable**: Saga changes without service restart
* **Auditability**: Versioned definitions with full history in reference data
* **Testability**: Unit test posting rules independently; dry-run API for validation
* **Performance**: CEL (~100ns) for high-frequency calculations
* **Safety**: Both languages sandboxed, deterministic, guaranteed termination
* **Bi-temporal replay**: Deterministic execution enables valuation replay
* **Durable execution**: Sagas survive pod death via checkpoint-and-replay
* **Party isolation**: Cross-party data access enforced at runtime
* **Financial grade**: External idempotency enforcement, zombie detection, transaction affinity

### Negative Consequences

* **Learning curve**: Teams must learn both Starlark and CEL syntax
* **Debugging complexity**: Two-language stack requires tooling investment
* **Runtime dependency**: Saga execution requires reference data service availability
* **Migration effort**: Existing Go orchestrators must be ported to Starlark
* **Version management**: Saga versions add operational complexity

## Links

### Implementation PRDs (Parallel Work Streams)

* [PRD: Starlark Saga Orchestration (Core)](../prd/006-starlark-saga-orchestration-core.md) - Runtime,
  builtins, party isolation, composition (Streams 1-6)
* [PRD: Starlark Service Bindings](../prd/008-starlark-service-bindings.md) - Real service integration via gRPC
* [PRD: Durable Execution Engine](../prd/005-durable-execution-engine.md) - Persistence, replay,
  async wait, hot-fixing (Streams 7-9)

### Implementation Guides

* [Adding Starlark Service Bindings](../guides/adding-starlark-service-bindings.md) - Step-by-step guide
  for implementing service handlers
* [Starlark Saga Architecture](../architecture/starlark-saga-architecture.md) - Architecture overview and
  service binding patterns

### Related ADRs

* [ADR-0014: Financial Instrument Reference Data](0014-financial-instrument-reference-data.md) - CEL foundation
* [ADR-0013: Generic Asset Quantity Types](0013-generic-asset-quantity-types.md) - Type system

### External References

* [go.starlark.net](https://pkg.go.dev/go.starlark.net/starlark) - Starlark Go implementation
* [google/cel-go](https://github.com/google/cel-go) - CEL Go implementation
* [Starlark Language Spec](https://github.com/bazelbuild/starlark/blob/master/spec.md) - Language specification
* [CEL Specification](https://github.com/google/cel-spec) - Expression language specification

## Notes

### Migration Path

Phase dispositions assessed 2026-03-06 against current implementation in `shared/pkg/saga/`.

1. **Phase 1: Foundation** — COMPLETED. Registry (`handlers.go`), runtime (`runtime.go`, `starlark_runner.go`),
   schema (`schema/`), CEL evaluator (`cel_evaluator.go`), caching (`validation/cache.go`).
2. **Phase 2: Prove the Pattern** — PLANNED. No Go orchestrators have been migrated to Starlark yet.
   The runtime and service bindings are ready, but no production saga scripts exist.
3. **Phase 3: Expand** — PLANNED. Depends on Phase 2 completion.
4. **Phase 4: Enable Tenants** — PLANNED. Admin handler exists (`admin_handler.go`) but tenant
   self-service saga authoring is not yet exposed.
5. **Phase 5: Party Isolation** — COMPLETED. `party_scope.go` implements `PartyScope`,
   `PartyScopeResolver`, `PartyHierarchyClient` with immutable scope enforcement.
6. **Phase 6: Bi-Temporal** — PARTIALLY COMPLETED. Replay implemented (`replay.go`) with
   deterministic UUID generation (`uuid_generator.go`). `verify_execution()` audit builtin
   not yet implemented.
7. **Phase 7: Attribute Validation** — COMPLETED. Validator (`validator.go`) with AST extraction,
   handler schema validation (`validation/validator.go`), cached validation (`validation/cached_validator.go`).
8. **Phase 8: Durable Execution** — COMPLETED. Saga instances with lease-based claiming (`claiming.go`),
   orphan detection (`orphan_watcher.go`), step execution persistence (`step_execution.go`),
   migrations (`migrations.go`), lease renewal (`lease_renewer.go`), suspend/resume (`suspend.go`).
9. **Phase 9: Hardening** — PARTIALLY COMPLETED. Logic/Physics Linter implemented (`linter.go`).
   `valuate_batch()` builtin not yet implemented. External integration guardrails
   (Pre-Step Check pattern) enforced via linter.

See the Implementation PRDs above for detailed tasks organized into parallel work streams.

### Ken's "Policy and Procedure" Framing

For non-technical stakeholders:

* **CEL is the "Policy"**: "This is the formula for how we calculate the price." Policies are rigid, fast, and auditable.
* **Starlark is the "Procedure"**: "This is the sequence of steps we take to settle a transaction." Procedures are flexible and handle real-world complexity.

**Value proposition**: The system is **Flexible** (procedures can change without deployment) but **Rock-Solid** (financial math is enforced by a mathematical engine with guaranteed termination).

### Reconsidering This Decision

Revisit if:
- Starlark execution becomes a performance bottleneck (>5ms per saga)
- Debugging complexity outweighs flexibility benefits
- Tenant-specific saga customization proves unnecessary
- A superior embedded scripting solution emerges
