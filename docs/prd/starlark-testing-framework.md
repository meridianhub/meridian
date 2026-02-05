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

## What Gets Validated

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

**Tasks:**
1. Parse `handlers.yaml` schema
2. Generate mock function for each handler:
   ```go
   // Auto-generated mock
   func MockPositionKeepingInitiateLog(params map[string]any) (map[string]any, error) {
       // Validate required params against schema
       // Return empty map (success)
       return map[string]any{"log_id": "MOCK-001", "status": "INITIATED"}, nil
   }
   ```
3. Build MockRegistry (parallel to HandlerRegistry)

**Acceptance criteria:**
- Every handler in schema has a mock
- Mocks validate required parameters
- Mocks return type-correct responses

---

### Phase 2: Sandboxed Execution Engine (2 weeks, 8 SP)

**Goal:** Execute scripts with mocks safely

**Tasks:**
1. Extend `shared/pkg/saga/runtime.go`:
   ```go
   func (r *Runtime) DryRunWithMocks(script string, mockRegistry *MockRegistry) (*DryRunReport, error)
   ```
2. Inject mocks as Starlark globals (position_keeping, financial_accounting, etc.)
3. Execute script in sandboxed thread (timeout enforced)
4. Capture execution trace (handler calls, errors, metrics)

**Acceptance criteria:**
- Scripts execute with mocks
- Timeouts enforced (5s default)
- Errors captured and reported
- No access to real services

---

### Phase 3: Validation Report & Blocking (1 week, 5 SP)

**Goal:** Human-readable reports, deployment blocking

**Tasks:**
1. Generate validation report:
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
2. Integrate with Reference Data service `ActivateSaga` RPC
3. Block activation if validation fails

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
