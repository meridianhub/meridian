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

Date: 2025-01-20

## Status

Proposed

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
* **Safety**: Both languages are sandboxed, deterministic, non-Turing complete (no infinite loops)
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

```sql
-- Extends reference-data for saga definitions
CREATE TABLE saga_definitions (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id UUID NOT NULL,

    -- Identification
    name VARCHAR(64) NOT NULL,           -- "withdrawal", "deposit", "settlement"
    version INTEGER NOT NULL DEFAULT 1,

    -- Starlark script (the orchestration logic)
    script TEXT NOT NULL,

    -- Lifecycle
    status VARCHAR(16) NOT NULL DEFAULT 'DRAFT',  -- DRAFT, ACTIVE, DEPRECATED

    -- Metadata
    description TEXT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    activated_at TIMESTAMPTZ,

    UNIQUE(tenant_id, name, version),
    CHECK (status IN ('DRAFT', 'ACTIVE', 'DEPRECATED')),
    CHECK (script <> '')
);

CREATE INDEX idx_saga_definitions_lookup
    ON saga_definitions(tenant_id, name, status);
```

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

    // 3. Register built-in functions (cel_eval, posting, resolve_account)
    builtins := r.createBuiltins(ctx, tenantID, input)

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

        // Resolve internal bank account
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

### Positive Consequences

* **Tenant flexibility**: Different posting patterns without code deployment
* **1:N posting support**: One position entry generates dynamic number of postings
* **Separation of concerns**: Business logic in Starlark, financial math in CEL
* **Hot-reloadable**: Saga changes without service restart
* **Auditability**: Versioned definitions with full history in reference data
* **Testability**: Unit test posting rules independently of orchestration
* **Performance**: CEL (~100ns) for high-frequency calculations
* **Safety**: Both languages sandboxed, deterministic, guaranteed termination
* **Bi-temporal replay**: Deterministic execution enables valuation replay

### Negative Consequences

* **Learning curve**: Teams must learn both Starlark and CEL syntax
* **Debugging complexity**: Two-language stack requires tooling investment
* **Runtime dependency**: Saga execution requires reference data service availability
* **Migration effort**: Existing Go orchestrators must be ported to Starlark
* **Version management**: Saga versions add operational complexity

## Links

* [ADR-0014: Financial Instrument Reference Data](0014-financial-instrument-reference-data.md) - CEL foundation
* [ADR-0013: Generic Asset Quantity Types](0013-generic-asset-quantity-types.md) - Type system
* [go.starlark.net](https://pkg.go.dev/go.starlark.net/starlark) - Starlark Go implementation
* [google/cel-go](https://github.com/google/cel-go) - CEL Go implementation
* [Starlark Language Spec](https://github.com/bazelbuild/starlark/blob/master/spec.md) - Language specification
* [CEL Specification](https://github.com/google/cel-spec) - Expression language specification

## Notes

### Migration Path

1. **Phase 1**: Add Starlark runtime to shared library, create saga definition schema
2. **Phase 2**: Port withdrawal saga as proof-of-concept, maintain Go fallback
3. **Phase 3**: Port deposit saga, add CEL valuation expressions
4. **Phase 4**: Port payment-order saga with 4-posting clearing model
5. **Phase 5**: Deprecate Go orchestrators, full Starlark operation

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
