# Valuation Engine Core Library - Implementation Specification

## Objective

Implement `shared/pkg/valuation` package with CEL Policy runtime, Starlark VM, and read-only security sandbox.

## Architecture

```text
shared/pkg/valuation/
├── engine.go                 # Core Engine interface
├── types.go                  # Request/Response/Analysis types
├── policy_runtime.go         # CEL compiler wrapper
├── starlark_runtime.go       # Starlark VM with sandbox
├── cache.go                  # L1 in-memory cache
├── engine_test.go            # Integration tests
├── policy_runtime_test.go    # CEL tests
├── starlark_runtime_test.go  # Starlark sandbox tests
└── internal/
    └── builtins/             # Read-only builtins (enforced by Go module isolation)
        ├── builtins.go       # Registry
        ├── market_data.go    # market_data() builtin
        ├── run_policy.go     # run_policy() builtin
        ├── quantity.go       # quantity() builtin
        └── decimal.go        # Decimal() builtin
```

## Critical Requirements

### 1. NO inline CEL strings - only named Policies

**FORBIDDEN:**

```python
# ❌ WRONG - inline CEL string
result = cel_eval("amount * 1.5", {"amount": 100})
```

**REQUIRED:**

```python
# ✅ CORRECT - named Policy from Reference Data
result = run_policy("retail_energy_tariff", {"kwh": 100, "tier": "Standard"})
```

### 2. CEL Cost Validation

- Policies MUST be validated at creation: reject if estimated cost > 10,000 units
- Runtime MUST enforce cost limit during evaluation
- Reference: `services/reference-data/cel/compiler.go` (CostLimit = 10000)

### 3. Starlark Security Sandbox

- NO filesystem access
- NO network access
- 5 second timeout
- 64MB memory limit
- NO `while` loops (language guarantee)
- NO recursion (language guarantee)

Reference patterns from `shared/pkg/saga/starlark_runner.go`.

### 4. Read-Only Builtins Enforcement

The `internal/builtins/` package isolation pattern prevents write-capable imports:

```go
// shared/pkg/valuation/internal/builtins/builtins.go
package builtins

// This package can ONLY import:
// - stdlib
// - shared/pkg/types (read-only types)
// - third-party read-only libraries

// ❌ FORBIDDEN imports (will fail CI):
// - position-keeping client (write capability)
// - financial-accounting client (write capability)
// - any gRPC client with mutation RPCs
```

CI verification script (`scripts/verify-valuation-readonly.sh`) enforces this.

### 5. run_policy() Builtin Behavior

```python
# Signature
result = run_policy(name, inputs)

# Implementation flow:
# 1. Resolve Policy by name from Reference Data (via cache)
# 2. Validate Policy cost < 10,000 units
# 3. Compile CEL expression
# 4. Validate inputs match Policy's input schema
# 5. Execute CEL with cost tracking
# 6. Return result
# 7. If output_instrument declared, validate result matches
```

Error handling:

- PolicyNotFoundError: named policy doesn't exist
- PolicyCostExceededError: policy cost > limit
- InputValidationError: inputs don't match schema
- ValuationOutputMismatchError: result instrument != declared output_instrument

### 6. record_path() Builtin

Tracks calculation audit trail with limits:

```python
# Starlark usage
record_path("Step 1: Retrieved spot price", {"price": 45.50})
record_path("Step 2: Applied GSP factor", {"gsp": "P", "factor": 1.05})
# ... up to 20 entries

# If > 20 entries, logs warning and truncates
```

Purpose: Audit trail for "Why was this value $X?" questions.

## Reference Implementation Patterns

### CEL Compiler Pattern

See `services/reference-data/cel/compiler.go`:

- `NewCompiler()` creates environments
- `CompileValidation()` / `CompileBucketKey()` with cost limits
- `validateExpressionConstraints()` for security

### Starlark Runtime Pattern

See `shared/pkg/saga/starlark_runner.go`:

- Thread creation with timeouts
- Builtin registration via `starlark.StringDict`
- Deterministic execution (no `time.now()`, no `random()`)

### Cache Pattern

See `services/reference-data/cache/tiered_cache.go`:

- L1 in-memory with TTL (5 minutes)
- Graceful stale serving (if fetch fails, serve expired entry with warning)
- Separate caches for Methods and Policies

## TDD Approach

Write tests FIRST for each component:

1. `types_test.go` - Request/Response validation
2. `policy_runtime_test.go` - CEL compilation, cost validation
3. `starlark_runtime_test.go` - Sandbox enforcement (filesystem block, network block, timeout, memory limit)
4. `builtins_test.go` - run_policy(), market_data(), record_path()
5. `cache_test.go` - TTL, stale serving, eviction
6. `engine_test.go` - End-to-end integration

Then implement to make tests pass (Red → Green → Refactor).

## Performance Target

- <5ms in-process execution (excluding network I/O for market data / reference data)
- Cache hit rate >95% in steady state
- Memory: <10MB per concurrent valuation request

## Type Definitions

### Core Types (types.go)

```go
package valuation

import (
    "time"
    "github.com/google/uuid"
    "github.com/shopspring/decimal"
)

// Request represents a valuation request
type Request struct {
    // Identifier for idempotency
    RequestID uuid.UUID

    // Valuation method to use (from Reference Data)
    MethodID uuid.UUID
    MethodVersion *int // nil = latest active

    // Input quantity to value
    Quantity Quantity

    // Context for valuation
    AccountID uuid.UUID
    PartyID uuid.UUID
    KnowledgeAt time.Time // Bi-temporal point

    // Parameters specific to the method
    Parameters map[string]interface{}
}

// Response represents valuation result
type Response struct {
    // Valued amount (output instrument)
    ValuedAmount Quantity

    // Audit trail
    Analysis *Analysis

    // Cache metadata
    CacheHit bool
    ComputedAt time.Time
}

// Analysis contains audit trail for valuation
type Analysis struct {
    // Calculation steps (max 20 entries)
    CalculationPath []PathEntry

    // Policies executed
    PoliciesExecuted []PolicyExecution

    // Market data sources used
    MarketDataSources []string

    // Warnings (e.g., stale cache, truncated path)
    Warnings []string
}

// PathEntry represents one step in calculation audit trail
type PathEntry struct {
    Description string
    Data map[string]interface{}
    Timestamp time.Time
}

// PolicyExecution captures policy invocation details
type PolicyExecution struct {
    PolicyName string
    PolicyVersion int
    Inputs map[string]interface{}
    Output interface{}
    CostUnits uint64
}

// Quantity represents a dimensional value
type Quantity struct {
    Amount decimal.Decimal
    InstrumentCode string // "KWH", "USD", "GBP"
    Attributes map[string]string
}
```

### Engine Interface (engine.go)

```go
package valuation

import "context"

// Engine executes valuation methods
type Engine interface {
    // Valuate executes a valuation method
    Valuate(ctx context.Context, req *Request) (*Response, error)
}

// Config for engine initialization
type Config struct {
    // CEL compiler for policy execution
    PolicyRuntime PolicyRuntime

    // Starlark runtime for method execution
    StarlarkRuntime StarlarkRuntime

    // Cache for methods and policies
    Cache Cache

    // Maximum calculation path entries
    MaxPathEntries int // default: 20
}
```

## Error Types

```go
package valuation

import "errors"

var (
    ErrPolicyNotFound = errors.New("policy not found")
    ErrPolicyCostExceeded = errors.New("policy cost exceeds limit")
    ErrInputValidationFailed = errors.New("input validation failed")
    ErrOutputMismatch = errors.New("valuation output instrument mismatch")
    ErrStarlarkTimeout = errors.New("starlark execution timeout")
    ErrStarlarkMemoryLimit = errors.New("starlark memory limit exceeded")
    ErrStarlarkSandboxViolation = errors.New("starlark sandbox violation")
)
```

## Implementation Order

1. ✅ Create spec (this file)
2. Create `types.go` with core types
3. Create `types_test.go` (validation tests)
4. Create `policy_runtime.go` interface and stub
5. Create `policy_runtime_test.go` (cost validation, compilation)
6. Implement `policy_runtime.go` (make tests pass)
7. Create `starlark_runtime.go` interface and stub
8. Create `starlark_runtime_test.go` (sandbox tests)
9. Implement `starlark_runtime.go` (make tests pass)
10. Create `internal/builtins/` package structure
11. Create `builtins_test.go`
12. Implement builtins (run_policy, market_data, record_path, etc.)
13. Create `cache.go` interface and stub
14. Create `cache_test.go`
15. Implement cache (make tests pass)
16. Create `engine.go` implementation
17. Create `engine_test.go` (integration tests)
18. Implement engine (make tests pass)
19. Create CI verification script
20. Document usage examples

## Success Criteria

- [ ] All tests passing
- [ ] Code coverage >80%
- [ ] No write-capable imports in valuation package
- [ ] CI verification script passes
- [ ] Performance: <5ms execution (verified via benchmark tests)
- [ ] Security: Sandbox violations properly blocked (verified via tests)
- [ ] Documentation: Package godoc with usage examples

## References

- ADR: `/Users/ben/dev/github.com/meridianhub/meridian/worktree/valuation-engine/3--create-valuation-library/docs/adr/0028-starlark-saga-cel-valuation.md`
- PRD: `/Users/ben/dev/github.com/meridianhub/meridian/worktree/valuation-engine/3--create-valuation-library/docs/prd/valuation-service.md`
- CEL Compiler: `services/reference-data/cel/compiler.go`
- Starlark Runner: `shared/pkg/saga/starlark_runner.go`
- Saga Models: `shared/pkg/saga/models.go`
