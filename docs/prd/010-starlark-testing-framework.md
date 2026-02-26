# PRD: Starlark Automatic Dry-Run Validation

**Status:** Draft
**Author:** Platform Team
**Date:** 2026-02-05
**Related:** Valuation Service (PR #742), Saga Orchestration, Service Bindings (handlers.yaml)

---

## Executive Summary

**Problem:** Starlark scripts can fail at runtime due to undefined handlers, wrong parameter types,
or logic errors. Tenants have no way to validate scripts before deployment.

**Solution:** Automatically execute Starlark scripts with **auto-generated mocks** from
`handlers.yaml` before deployment. Catch errors at "compile-time" (script upload) instead of
runtime (production).

**Core Benefit:** **Zero tenant effort** - scripts are automatically validated. No test files to
write, no manual testing needed.

**Bonus:** Execution provides **complexity metrics** (operations count, estimated CPU cycles) for
capacity planning.

---

## The Problem

### Current State: Runtime Failures

```python
# withdrawal.star - uploaded to Reference Data service
step(name="create_lien")
result = payment_order.create_lien(...)  # ❌ Typo: should be position_keeping.create_lien

# This script compiles (syntax is valid)
# But fails in PRODUCTION when executed:
# Error: module 'payment_order' has no attribute 'create_lien'
```

**Consequences:**

- Production saga failures
- Customer-facing errors
- Platform team investigating tenant scripts
- Slow feedback loop (deploy → fail → debug → redeploy)

### Missing: Pre-Deployment Validation

What if we could **run the script with mocks** before accepting it?

```bash
# At deployment time:
$ meridian-cli saga validate withdrawal.star --dry-run

❌ Validation Failed:
   Line 42: Undefined handler 'payment_order.create_lien'
   Available: position_keeping.create_lien

Script rejected. Fix errors and resubmit.
```

---

## Proposed Solution: Automatic Dry-Run

### Architecture

```text
┌──────────────────────────────────────────────────────────┐
│ 1. Tenant uploads saga.star to Reference Data service   │
└────────────────┬─────────────────────────────────────────┘
                 │
                 ▼
┌──────────────────────────────────────────────────────────┐
│ 2. Auto-generate mocks from handlers.yaml schema        │
│    • position_keeping.initiate_log → mock returns {}    │
│    • financial_accounting.post_entry → mock returns {}  │
│    • All handlers defined in schema get mock impls      │
└────────────────┬─────────────────────────────────────────┘
                 │
                 ▼
┌──────────────────────────────────────────────────────────┐
│ 3. Execute script with mocks (sandboxed)                │
│    • Bounded execution (no while loops)                 │
│    • Timeouts enforced                                  │
│    • Capture: handler calls, errors, metrics            │
└────────────────┬─────────────────────────────────────────┘
                 │
                 ▼
┌──────────────────────────────────────────────────────────┐
│ 4. Generate Validation Report                           │
│    ✅ Pass: Script executes without errors              │
│    ❌ Fail: Undefined handlers, wrong types, etc.       │
│    📊 Metrics: Operations count, complexity score       │
└────────────────┬─────────────────────────────────────────┘
                 │
                 ▼
┌──────────────────────────────────────────────────────────┐
│ 5. Accept or Reject                                     │
│    • Pass → Store script, mark active                   │
│    • Fail → Return errors to tenant, block activation   │
└──────────────────────────────────────────────────────────┘
```

### Key Insight: Zero Tenant Work

**Traditional approach:** Tenant writes tests manually
**Our approach:** Platform auto-validates using schema

The schema (`handlers.yaml`) is the single source of truth. Mocks are generated automatically.

---

## Existing Validation Infrastructure

**This PRD builds on existing validation.** We already have substantial static analysis in place.

### What Already Exists

**Location:** `shared/pkg/saga/validator.go` and `shared/pkg/saga/linter.go`

#### 1. Static Validation (`ValidateSagaScript`)

```go
// shared/pkg/saga/validator.go:84-127
func ValidateSagaScript(script string) error
```

**Checks:**

- ✅ Script size (max 64KB)
- ✅ Syntax parsing (catches Python syntax errors like `is not None`)
- ✅ Blocked functions (`load`, `exec`, `compile`, `open`, `eval`, `setattr`)
- ✅ Loop nesting depth (max 3 levels)
- ✅ Security violations via AST walking

#### 2. Semantic Linting (`SemanticLinter`)

```go
// shared/pkg/saga/linter.go:97-onwards
type SemanticLinter struct
```

**Checks:**

- ✅ Decimal arithmetic (should use CEL instead)
- ✅ Magic numbers (hardcoded numeric literals)
- ✅ Nested conditionals (excessive if/else nesting)
- ✅ Hardcoded codes (instrument/account codes should be parameterized)
- ✅ Missing pre-checks (external steps need `verify_external_state`)

#### 3. Visibility Validation (`visibility_validator.go`)

- ✅ Party scope validation (tenant isolation)
- ✅ Prevents scripts from accessing data outside their party scope

### What's Missing: Runtime Validation

**Current gap:** Static checks can't catch:

- Undefined handler calls (typos in module/handler names)
- Missing required parameters
- Logic errors that cause script failure
- Runaway scripts (badly-written loops)

**This PRD fills the gap:** Execute scripts with mocks to catch runtime errors.

---

## How Dry-Run Extends Existing Validation

```text
┌─────────────────────────────────────────────────────────┐
│ Layer 1: Static Validation (EXISTING)                  │
│ shared/pkg/saga/validator.go                           │
│ • Syntax, security, blocked functions, loop depth      │
└────────────────┬────────────────────────────────────────┘
                 ↓ Pass
┌─────────────────────────────────────────────────────────┐
│ Layer 2: Semantic Linting (EXISTING)                   │
│ shared/pkg/saga/linter.go                              │
│ • Decimal maths, magic numbers, hardcoded codes         │
└────────────────┬────────────────────────────────────────┘
                 ↓ Pass
┌─────────────────────────────────────────────────────────┐
│ Layer 3: Dry-Run Validation (NEW - THIS PRD)          │
│ • Execute with auto-generated mocks                    │
│ • Catch undefined handlers, missing params, timeouts  │
│ • Provide complexity metrics                           │
└─────────────────────────────────────────────────────────┘
```

### Integration Point

```go
// shared/pkg/saga/validator.go (NEW function to add)
func ValidateWithDryRun(script string, mockRegistry *MockRegistry) (*ValidationResult, error) {
    result := &ValidationResult{}

    // 1. EXISTING: Static validation
    if err := ValidateSagaScript(script); err != nil {
        result.Errors = append(result.Errors, err)
        return result, nil  // Don't continue if syntax fails
    }

    // 2. EXISTING: Semantic linting
    linter := NewSemanticLinter()
    lintIssues, err := linter.Analyze(script)
    if err != nil {
        result.Errors = append(result.Errors, err)
    }
    result.LintIssues = lintIssues

    // 3. NEW: Dry-run execution with mocks
    dryRunReport, err := ExecuteDryRun(script, mockRegistry)
    if err != nil {
        result.Errors = append(result.Errors, err)
    } else {
        result.ComplexityMetrics = dryRunReport.Metrics
    }

    return result, nil
}
```

**Key insight:** We extend `ValidationResult` (already exists at line 12-38 in `validator.go`) with
dry-run findings and complexity metrics. No need to reinvent the validation pipeline.

---

## What Gets Validated (By Dry-Run)

### 1. Handler Existence

```python
# ❌ ERROR: Undefined handler
result = position_keeping.nonexistent_handler(...)
# Report: Handler 'nonexistent_handler' not found in schema
```

### 2. Required Parameters

```python
# ❌ ERROR: Missing required parameter
result = position_keeping.initiate_log(
    amount="100.00"
    # Missing: direction (required in schema)
)
```

### 3. Type Checking (Where Possible)

```python
# ❌ ERROR: Type mismatch
result = position_keeping.initiate_log(
    amount=100,  # Should be Decimal("100.00")
    direction="DEBIT"
)
```

### 4. Script Executes to Completion

```python
# ❌ ERROR: Logic error
if account_balance is None:
    fail("Account not found")  # Script fails - validation catches this

# ✅ PASS: Script completes
if account_balance is None:
    account_balance = Decimal("0.00")  # Handles None case
```

### 5. Performance / Runaway Scripts

```python
# ❌ ERROR: Timeout exceeded
for item in range(10000):  # Badly written - too many iterations
    for nested in range(10000):  # Nested loop = 100M iterations!
        ...

# Dry-run times out after 5 seconds
# Report: Script exceeded timeout - likely infinite loop or excessive complexity
```

**Benefit:** Catches badly-written scripts that would hang production. Forces tenant to refactor
before deployment.

---

## Complexity Metrics (Bonus Feature)

During mock execution, track:

```text
Complexity Report:
  • Handler calls: 8
  • Conditional branches: 3
  • Loop iterations: 12 (over 4 items × 3 loops)
  • CEL evaluations: 2
  • Total operations: ~45
  • Estimated execution time: <50ms (based on operation count)

Complexity Score: 6/10 (Medium)
```

**Use cases:**

- Capacity planning (estimate load per saga execution)
- Identify overly complex scripts (suggest refactoring)
- SLA estimation (95th percentile execution time)

**Implementation:** Instrument mock handlers to count operations.

---

## Implementation Plan

### Phase 1: Schema-Based Mock Generation (2 weeks, 8 SP)

**Goal:** Auto-generate mocks from `handlers.yaml`

**Builds on:**

- `shared/pkg/saga/schema/` - Handler schema definitions (already exists)
- `shared/pkg/saga/starlark_runner.go:33-38` - `HandlerRegistry` pattern to mirror

**Tasks:**

1. Parse `handlers.yaml` schema (leverage existing `schema` package)
2. Generate mock function for each handler:

   ```go
   // NEW: shared/pkg/saga/mock_registry.go
   type MockRegistry struct {
       mocks map[string]HandlerFunc  // mirrors HandlerRegistry
   }

   // Auto-generated mock
   func MockPositionKeepingInitiateLog(params map[string]any) (map[string]any, error) {
       // Validate required params against schema
       // Return type-correct response from schema
       return map[string]any{"log_id": "MOCK-001", "status": "INITIATED"}, nil
   }
   ```

3. Build MockRegistry (parallel to existing `HandlerRegistry`)

**Acceptance criteria:**

- Every handler in `handlers.yaml` has a mock
- Mocks validate required parameters using existing schema types
- Mocks return type-correct responses (strings, Decimals, enums)

---

### Phase 2: Sandboxed Execution Engine (2 weeks, 8 SP)

**Goal:** Execute scripts with mocks safely

**Builds on:**

- `shared/pkg/saga/runtime.go:124-249` - `ExecuteSaga` pattern (already has timeout, sandboxing)
- `shared/pkg/saga/starlark_runner.go:141-248` - `StarlarkSagaRunner` execution logic
- Service module injection already implemented (lines 179-182)

**Tasks:**

1. Extend `shared/pkg/saga/runtime.go`:

   ```go
   // NEW: Add dry-run method to existing Runtime struct
   func (r *Runtime) DryRunWithMocks(script string, mockRegistry *MockRegistry) (*DryRunReport, error) {
       // Reuse existing ExecuteSagaWithInput but with mockRegistry instead of real handlers
       // Timeout, sandboxing, context cancellation already implemented
   }
   ```

2. Inject mocks as Starlark globals (mirrors existing service module injection pattern)
3. Execute script in sandboxed thread (reuse existing timeout logic - lines 140-142)
4. Capture execution trace (handler calls, errors, metrics)

**Acceptance criteria:**

- Scripts execute with mocks
- Timeouts enforced (reuse existing `DefaultTimeout = 5s` from line 18)
- Errors captured and reported
- No access to real services (mocks only)

---

### Phase 3: Validation Report & Blocking (1 week, 5 SP)

**Goal:** Human-readable reports, deployment blocking

**Builds on:**

- `shared/pkg/saga/validator.go:12-59` - `ValidationResult` struct (already exists)
- `ValidateActivation()` at line 386 - Strict enforcement pattern

**Tasks:**

1. Extend `ValidationResult` struct with complexity metrics:

   ```go
   // shared/pkg/saga/validator.go (extend existing struct)
   type ValidationResult struct {
       Errors       []error       // Already exists
       LintIssues   []LintIssue   // Already exists
       ComplexityMetrics *ComplexityMetrics  // NEW
   }

   type ComplexityMetrics struct {
       HandlerCalls    int
       Operations      int
       ComplexityScore int  // 1-10 scale
   }
   ```

2. Generate validation report:

   ```text
   ✅ withdrawal.star - Validation Passed

   Handler Calls:
     • position_keeping.initiate_log ✓
     • financial_accounting.post_entry ✓
     • current_account.save ✓

   Complexity:
     • Operations: 42
     • Score: 5/10 (Low-Medium)

   Recommendation: Safe to activate
   ```

3. Integrate with Reference Data service `ActivateSaga` RPC
4. Block activation if validation fails

**Acceptance criteria:**

- Clear error messages with line numbers
- Complexity metrics included
- Failed scripts rejected at upload

---

### Phase 4: CLI & Developer Experience (1 week, 3 SP)

**Goal:** Local validation for rapid iteration

**Tasks:**

1. CLI command: `meridian-cli saga validate withdrawal.star --dry-run`
2. Show validation report in terminal
3. Exit code 0 (pass) or 1 (fail) for CI/CD integration

**Acceptance criteria:**

- Developers can validate locally before upload
- Fast feedback (<2 seconds for typical saga)
- Works offline (uses local handlers.yaml)

---

## Total: 6 weeks, 24 story points

---

## Example: End-to-End Flow

### Developer Experience

```bash
# 1. Write saga script locally
$ cat withdrawal.star
saga(name="withdrawal", version="1.0.0")
step(name="create_lien")
result = position_keeping.initiate_log(
    amount=Decimal("100.00"),
    direction="DEBIT",
    account_id=input_data["account_id"]
)

# 2. Validate locally (instant feedback)
$ meridian-cli saga validate withdrawal.star --dry-run

✅ Validation Passed
   • 3 handlers called
   • Complexity: 4/10 (Low)
   • Estimated execution: <30ms

# 3. Upload to platform
$ meridian-cli saga upload withdrawal.star

✅ Script uploaded and activated
   (Platform re-ran validation automatically)
```

### Platform Experience

```go
// Reference Data service
func (s *Service) ActivateSaga(ctx context.Context, req *ActivateSagaRequest) error {
    script := req.Script

    // AUTO-VALIDATE (zero config)
    report, err := s.dryRunValidator.Validate(script)
    if err != nil || !report.Passed {
        return fmt.Errorf("validation failed: %v", report.Errors)
    }

    // Validation passed → store and activate
    return s.repository.SaveSaga(script, req.Name, req.Version)
}
```

---

## Success Metrics

1. **Zero runtime errors** from undefined handlers (caught by dry-run)
2. **90% of scripts pass validation** on first upload (good UX)
3. **<2s validation time** for typical saga (fast feedback)
4. **Complexity scores** used for capacity planning (bonus)

---

## Open Questions

### 1. What mock responses should return?

**Option A:** Empty success responses

```python
# Mock returns: {"log_id": "MOCK-001", "status": "INITIATED"}
```

**Option B:** Schema-defined examples

```yaml
# handlers.yaml
position_keeping.initiate_log:
  example_response:
    log_id: "LOG-12345"
    status: "INITIATED"
```

**Recommendation:** Option A for MVP (simpler). Option B for richer validation.

### 2. How to handle conditional logic?

```python
if account_type == "SAVINGS":
    rate = Decimal("0.05")
else:
    rate = Decimal("0.03")
```

**Challenge:** Dry-run only executes one path.

**Solution:** Accept this limitation for MVP. Both paths have valid syntax, dry-run validates
the executed path. (Future: property-based testing could explore multiple paths.)

### 3. Should this replace tenant-written tests?

**No.** This is **automatic validation**, not a testing framework.

**Dry-run catches:** Syntax errors, undefined handlers, missing params
**Tests catch:** Business logic bugs, edge cases, unhappy paths

They complement each other. Dry-run is **zero effort**, tests are **optional but recommended**.

---

## Future Enhancements (Post-MVP)

1. **Tenant-written tests** (`.star_test` files) for business logic validation
2. **Replay determinism testing** (ADR-028 requirement for durable execution)
3. **Property-based testing** (explore multiple code paths automatically)
4. **Integration testing** (run against real services in test environment)

These are valuable but NOT essential for the core value proposition: **"Catch errors before
deployment with zero tenant effort."**

---

## Related Work

- **Bazel Starlark validation** - Validates build files before execution
- **TypeScript type checking** - Catches errors at "compile time" for dynamic language
- **AWS CloudFormation validation** - Validates templates before stack creation

**Meridian's advantage:** We have a complete schema (`handlers.yaml`) that makes mock generation
trivial. No manual mocking needed.

---

## Conclusion

**The core insight:** Auto-generated mocks from `handlers.yaml` + sandboxed execution = **cheap,
automatic validation** that catches 80% of runtime errors with zero tenant effort.

**The complexity bonus:** Execution trace gives us operation counts for capacity planning.

**The simplicity:** 6 weeks, 24 SP, no dependencies on testing frameworks or tenant-written tests.

This is the foundation. Everything else (tenant tests, replay safety, property-based testing) can
be layered on top later.
