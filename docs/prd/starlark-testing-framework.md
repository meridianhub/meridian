# PRD: Starlark Testing & Validation Framework

**Status:** Draft → **Architect Review: LGTM - Essential for Production**
**Author:** Platform Team
**Date:** 2026-02-05
**Updated:** 2026-02-05 (Architect feedback incorporated)
**Related:** Valuation Service (PR #742), Saga Orchestration, ADR-028

**Architect's Verdict:**

> "This framework transforms Meridian from a 'programmable ledger' into a
> **Verified Financial Kernel**. It allows us to offload the risk of custom
> logic to tenants while providing guardrails to ensure they don't break the
> system. **Essential for move to production.**"

---

## Starting Point

**Question:** Should we start by expanding the `ScriptValidator` to handle the `handlers.yaml` type inference?

**Answer:** **Yes.** Phase 1 (Compile-Time Validation) should extend
`shared/pkg/saga/validator.go` to:

1. Load `handlers.yaml` schema into a `SchemaRegistry`
2. Parse Starlark AST and extract service calls
3. Validate each call against schema (handler existence, param types, required fields)
4. Enforce Conservation of Dimension rules (Settlement/Physics/Financial boundaries)

This creates the foundation. Phases 2-3 (Unit Testing + Replay Safety) build on top of
this validator.

---

## Executive Summary

**Context:** If we give tenants the power to write **"Procedures" (Starlark)** and
**"Policies" (CEL)**, we must give them tools to verify them. Without this, the
"Time-to-Market" advantage of Starlark is wiped out by "Time-to-Debug" in production.

As Meridian expands Starlark usage from saga orchestration to valuation strategies
(PR #742) and beyond, we need a comprehensive testing framework:

1. **Compile-time validation** - Type safety from `handlers.yaml` schema
2. **Replay determinism testing** - Critical for ADR-028 durable execution
3. **Tenant self-service testing** - Users write tests with mocking framework
4. **Conservation of Dimension** - Enforce Physics vs Money boundaries
5. **Side-Effect Audit** - Visibility into saga blast radius before activation

**Why This Is Essential:**

- **Production safety gate:** Testing becomes a precondition for saga activation (not optional)
- **Tenant confidence:** Deploy scripts with green test report, not "hope and pray"
- **Regulatory compliance:** Auditable, testable financial transformations
- **Platform reliability:** Catch undefined handlers, type errors, non-deterministic logic
  pre-production

**Impact:** Transforms Meridian from "programmable ledger" to **"Verified Financial Kernel"**
where tenant-written logic is subject to the same rigor as platform code.

---

## Problem Statement

### Current Gaps

**1. Validation is syntax-only:**

```python
# ✅ Compiles
result = position_keeping.nonexistent_handler(...)

# ❌ Fails at runtime (handler doesn't exist)
```

**2. No test framework for script authors:**

- Developers test via Go integration tests
- Tenants have no way to test their scripts
- No assertion library for Starlark

**3. Behavior verification requires manual execution:**

- No automated happy/unhappy path testing
- No regression detection
- No performance benchmarking

### Why This Matters Now

**Valuation use case (PR #742):**

```python
# Tenant-written valuation strategy
def valuate(input_quantity, params, knowledge_at):
    spot_price = market_data.get_price("EPEX_SPOT_GBP", knowledge_at)

    # What if spot_price is None?
    # What if params missing required keys?
    # How to test different market conditions?

    final_rate = cel_eval("spot * coeff * markup", {...})
    return {"amount": input_quantity.amount * final_rate, ...}
```

**Without testing framework:**

- Tenant deploys script
- Production hits edge case
- Valuation fails, breaking settlement
- Platform team investigates (slow feedback loop)

**With testing framework:**

- Tenant writes tests locally
- Platform validates before deployment
- Edge cases caught pre-production
- Tenant iterates rapidly

---

## Proposed Solution

### Three-Tier Validation

```text
┌──────────────────────────────────────────────────────────┐
│ Tier 1: Compile-Time Validation (Pre-Deployment)        │
│ • Syntax checking (already exists)                       │
│ • Type checking against handlers.yaml schema            │
│ • Service module existence validation                    │
│ • Required parameter validation                          │
│ • Return type validation                                 │
└──────────────────────────────────────────────────────────┘
                           ↓
┌──────────────────────────────────────────────────────────┐
│ Tier 2: Static Analysis (Pre-Deployment)                │
│ • Unused variables detection                             │
│ • Unreachable code detection                             │
│ • Complexity metrics (cyclomatic complexity)             │
│ • Step coverage (are all steps reachable?)              │
│ • Compensation coverage (every step has compensation?)   │
└──────────────────────────────────────────────────────────┘
                           ↓
┌──────────────────────────────────────────────────────────┐
│ Tier 3: Runtime Testing (Tenant-Written)                │
│ • Unit tests with mocked service modules                 │
│ • Integration tests with real services                   │
│ • Property-based testing (QuickCheck style)              │
│ • Performance benchmarks                                 │
└──────────────────────────────────────────────────────────┘
```

---

## Tier 1: Compile-Time Validation

### Type Checking Against Schema

**Goal:** Catch errors before script executes.

**Implementation:**

```go
// shared/pkg/saga/validator.go
type ScriptValidator struct {
    schemaRegistry *schema.Registry
}

func (v *ScriptValidator) Validate(script string) (*ValidationReport, error) {
    // 1. Parse Starlark AST
    file, err := syntax.Parse("script.star", script, 0)
    if err != nil {
        return nil, err // Syntax error
    }

    // 2. Walk AST, find service module calls
    calls := extractServiceCalls(file)

    // 3. Validate each call against handlers.yaml
    var errors []ValidationError
    for _, call := range calls {
        handler := v.schemaRegistry.GetHandler(call.Module, call.Handler)
        if handler == nil {
            errors = append(errors, ValidationError{
                Line: call.Line,
                Message: fmt.Sprintf("undefined handler: %s.%s", call.Module, call.Handler),
            })
            continue
        }

        // Check required parameters
        for _, param := range handler.Params {
            if param.Required && !call.HasParam(param.Name) {
                errors = append(errors, ValidationError{
                    Line: call.Line,
                    Message: fmt.Sprintf("missing required parameter: %s", param.Name),
                })
            }
        }

        // Check parameter types (where statically determinable)
        for paramName, paramValue := range call.Params {
            expectedType := handler.Params[paramName].Type
            actualType := inferType(paramValue)
            if actualType != nil && actualType != expectedType {
                errors = append(errors, ValidationError{
                    Line: call.Line,
                    Message: fmt.Sprintf("type mismatch: %s expected %s, got %s",
                        paramName, expectedType, actualType),
                })
            }
        }
    }

    return &ValidationReport{Errors: errors}, nil
}
```

**CLI usage:**

```bash
# Validate saga script
meridian-cli saga validate withdrawal.star

# Output:
# ✅ Syntax: OK
# ✅ Type checking: OK
# ✅ Service modules: OK
# ⚠️  Warnings:
#   - Line 42: Unused variable 'temp_result'
#   - Line 89: Unreachable code after fail()
```

---

## Tier 2: Static Analysis

### Linting Rules

**Goal:** Catch code smells and anti-patterns.

**Implemented checks:**

1. **Unused variables**

   ```python
   # ❌ LINT ERROR
   temp = Decimal("100")
   amount = Decimal("200")  # 'temp' never used
   ```

2. **Unreachable code**

   ```python
   # ❌ LINT ERROR
   fail("Critical error")
   log("This never executes")  # Unreachable
   ```

3. **Missing step() calls**

   ```python
   # ❌ LINT WARNING
   result = position_keeping.initiate_log(...)  # No step() before handler
   ```

4. **Compensation coverage**

   ```python
   # ✅ GOOD - Handler has compensation defined
   step(name="log_position")
   result = position_keeping.initiate_log(...)  # Compensation: cancel_log

   # ⚠️  WARNING - Handler has no compensation
   step(name="send_email")
   send_notification.email(...)  # No compensation handler in schema
   ```

5. **Cyclomatic complexity**

   ```python
   # ⚠️  COMPLEXITY WARNING: Cyclomatic complexity = 15 (threshold: 10)
   # Consider refactoring into smaller functions
   def execute_complex_saga():
       if condition1:
           if condition2:
               for item in items:
                   if item.valid:
                       ...  # Deep nesting
   ```

6. **Conservation of Dimension (Physics vs Money)**

   **Goal:** Enforce category boundaries established in Valuation PRD v2.6.

   ```python
   # ❌ VALIDATION ERROR: Category violation
   # Settlement saga calling Physics write handler
   saga(category="Settlement")
   step(name="log_energy")
   result = position_keeping.initiate_log(...)  # Error: Settlement cannot write to Physics dimension

   # ✅ VALID: Settlement reads Physics, writes Financial
   saga(category="Settlement")
   energy_balance = position_keeping.get_balance(...)  # Read OK
   financial_accounting.post_journal_entry(...)  # Write to own dimension OK
   ```

   **Validation rules:**
   - **Settlement** sagas: Can READ Physics, can only WRITE Financial
   - **Physics** sagas: Can only operate on Physics dimension
   - **Financial** sagas: Can only operate on Financial dimension

7. **Side-Effect Audit Manifest**

   **Goal:** Generate manifest of all external services touched by script for human review.

   ```bash
   $ meridian-cli saga validate energy_settlement.star

   ✅ Validation passed

   📋 Side-Effect Manifest:
      - position_keeping.get_balance (READ)
      - market_information.get_price (READ)
      - financial_accounting.post_journal_entry (WRITE)
      - current_account.create_booking (WRITE)

   ⚠️  This script performs 2 WRITE operations.
      Review compensation handlers before activation.
   ```

   **Use case:** Manifest shown to tenant before saga activation. Provides clarity on blast radius if saga fails.

---

## Tier 3: Runtime Testing Framework

### Test DSL for Starlark Scripts

**Goal:** Tenants write tests alongside their scripts.

**Example test file:**

```python
# withdrawal_test.star - Tests for withdrawal.star

# Import testing framework
test = require("meridian.testing")

# Test: Successful withdrawal
@test.case("successful_withdrawal")
def test_successful_withdrawal():
    # Arrange: Mock input
    input_data = {
        "account_id": "ACC-001",
        "amount": "100.50",
        "currency": "GBP",
    }

    # Arrange: Mock service responses
    test.mock("position_keeping.initiate_log", returns={
        "log_id": "LOG-001",
        "status": "INITIATED",
    })

    test.mock("financial_accounting.capture_posting", returns={
        "posting_id": "POST-001",
        "status": "POSTED",
    })

    # Act: Execute saga
    result = execute_saga("withdrawal", input_data)

    # Assert: Verify result
    test.assert_equal(result["status"], "COMPLETED")
    test.assert_not_nil(result["transaction_id"])

    # Assert: Verify service calls
    test.assert_called("position_keeping.initiate_log", {
        "position_id": "ACC-001",
        "amount": Decimal("100.50"),
        "direction": "DEBIT",
    })

# Test: Insufficient balance
@test.case("insufficient_balance")
def test_insufficient_balance():
    input_data = {"account_id": "ACC-002", "amount": "1000000.00"}

    # Mock balance check failure
    test.mock("position_keeping.get_balance", returns={
        "balance": Decimal("100.00"),
    })

    # Act & Assert: Saga should fail
    test.assert_fails(
        lambda: execute_saga("withdrawal", input_data),
        message_contains="Insufficient balance"
    )

# Test: Invalid amount
@test.case("negative_amount")
def test_negative_amount():
    input_data = {"account_id": "ACC-003", "amount": "-50.00"}

    # Should fail before any service calls
    test.assert_fails(
        lambda: execute_saga("withdrawal", input_data),
        message_contains="Amount must be positive"
    )

    # Verify no service calls made
    test.assert_not_called("position_keeping.initiate_log")

# Run all tests
test.run_all()
```

**CLI execution:**

```bash
# Run tests
meridian-cli saga test withdrawal.star --test-file withdrawal_test.star

# Output:
# Running tests for withdrawal.star...
#
# ✅ test_successful_withdrawal (42ms)
# ✅ test_insufficient_balance (18ms)
# ✅ test_negative_amount (5ms)
#
# 3 passed, 0 failed (65ms total)
# Coverage: 87% of saga steps executed in tests
```

---

### Testing Framework API

**Core functions:**

```python
# Test definition
@test.case(name)              # Decorator to define test case
test.run_all()                # Execute all test cases

# Assertions
test.assert_equal(a, b)       # Assert equality
test.assert_not_equal(a, b)   # Assert inequality
test.assert_true(condition)   # Assert truthy
test.assert_false(condition)  # Assert falsy
test.assert_nil(value)        # Assert None/null
test.assert_not_nil(value)    # Assert not None
test.assert_fails(func, message_contains="...")  # Assert raises error

# Mocking
test.mock(handler, returns={...})  # Mock service module handler
test.assert_called(handler, params={...})  # Assert handler was called
test.assert_not_called(handler)    # Assert handler was not called
test.assert_call_count(handler, n) # Assert call count

# Test execution
execute_saga(name, input_data)     # Execute saga in test environment

# Property-based testing
test.property(gen, func)          # QuickCheck-style property testing
```

---

### Property-Based Testing

**Goal:** Generate many test cases automatically.

**Example:**

```python
# Property: Withdrawal + deposit = original balance
@test.property(
    generators={
        "account_id": test.gen_uuid(),
        "amount": test.gen_decimal(min=0.01, max=1000.00),
    }
)
def prop_withdrawal_deposit_inverse(account_id, amount):
    # Setup: Initial balance
    initial_balance = Decimal("1000.00")
    test.mock("position_keeping.get_balance", returns={"balance": initial_balance})

    # Withdraw
    withdraw_result = execute_saga("withdrawal", {
        "account_id": account_id,
        "amount": str(amount),
    })

    # Deposit same amount
    deposit_result = execute_saga("deposit", {
        "account_id": account_id,
        "amount": str(amount),
    })

    # Property: Final balance = initial balance
    final_balance = test.get_mock_result("position_keeping.get_balance")["balance"]
    test.assert_equal(final_balance, initial_balance)

# Runs 100 random test cases
test.run_property(prop_withdrawal_deposit_inverse, iterations=100)
```

---

### Replay Determinism Testing (The "Survival" Test)

**Goal:** Verify sagas are replay-safe for Durable Execution Engine.

**Critical requirement:** ADR-028 mandates that sagas must produce identical results when
replayed. Non-deterministic sagas break durable execution recovery.

**Implementation:**

```python
# Test replay determinism
@test.case("verify_replay_determinism")
def test_withdrawal_replay_safety():
    input_data = {
        "account_id": "ACC-001",
        "amount": "100.50",
    }

    # Mock responses (same for both runs)
    test.mock("position_keeping.initiate_log", returns={
        "log_id": "LOG-001",
        "status": "INITIATED",
    })

    # First execution
    result1 = execute_saga("withdrawal", input_data)

    # Capture execution trace
    trace1 = test.get_execution_trace()  # Records: calls, new_uuid() sequences, step order

    # Second execution (simulated replay)
    result2 = execute_saga("withdrawal", input_data)
    trace2 = test.get_execution_trace()

    # Assert: Execution paths must be identical
    test.assert_replay_deterministic(trace1, trace2)
    # Checks:
    # - Same sequence of handler calls
    # - Same new_uuid() results (seeded from saga_execution_id)
    # - Same step names and order
    # - Same branching decisions
```

**What it catches:**

```python
# ❌ NON-DETERMINISTIC - Breaks replay
def execute_saga():
    if random.random() > 0.5:  # Different path on replay!
        ...

# ❌ NON-DETERMINISTIC - UUID generation
def execute_saga():
    # Without seeded UUID generator, replay generates different IDs
    booking_id = uuid.uuid4()  # WRONG - not replay-safe

# ✅ DETERMINISTIC - Replay-safe
def execute_saga():
    booking_id = new_uuid()  # Seeded from saga_execution_id
```

**Framework requirement:** The `shared/pkg/saga` runtime needs a `TestMode` flag. When enabled:

- Seed `new_uuid()` generator with deterministic value
- Capture execution trace for comparison
- Fail test if traces diverge

**Integration with Validation Pipeline:**

```bash
$ meridian-cli saga validate --check-replay withdrawal.star

✅ Replay Determinism: PASS
   - Executed 10 replay cycles
   - All traces identical
   - UUID sequences reproducible
```

---

### Integration Testing with Real Services

**Goal:** Test against actual services (not mocks).

**Example:**

```python
# withdrawal_integration_test.star

test = require("meridian.testing")
test.integration_mode()  # Use real services

@test.case("integration_successful_withdrawal")
def test_integration_withdrawal():
    # Setup: Create real test account
    account_id = test.create_test_account({
        "balance": Decimal("1000.00"),
        "currency": "GBP",
    })

    # Act: Execute saga against real services
    result = execute_saga("withdrawal", {
        "account_id": account_id,
        "amount": "100.50",
    })

    # Assert: Check real database state
    balance = test.query_balance(account_id)
    test.assert_equal(balance, Decimal("899.50"))

    # Cleanup: Delete test account
    test.cleanup_test_account(account_id)

test.run_all()
```

#### Production Safety (Critical)

`test.integration_mode()` MUST NEVER run against production environments.

**Enforcement:**

```go
// shared/pkg/saga/runtime.go
func (r *Runtime) EnableIntegrationMode(ctx *StarlarkContext) error {
    if ctx.EnvironmentType == PRODUCTION {
        return fmt.Errorf("FATAL: integration_mode() blocked - cannot run integration tests against PRODUCTION")
    }
    ctx.IntegrationMode = true
    return nil
}
```

**Environment detection:**

```go
type EnvironmentType string

const (
    PRODUCTION  EnvironmentType = "production"
    STAGING     EnvironmentType = "staging"
    DEV         EnvironmentType = "dev"
    TEST        EnvironmentType = "test"
)

// SagaContext includes environment
type StarlarkContext struct {
    // ...existing fields...
    EnvironmentType EnvironmentType  // Set by platform based on namespace/cluster
}
```

**Result:** Integration tests can only run in `DEV`, `TEST`, or `STAGING` environments. Any
attempt to call `test.integration_mode()` in production will fail immediately with a fatal
error.

**Benefits:**

- Catches integration issues mocks would miss
- Validates schema compatibility
- Tests actual compensation flows

**Tradeoffs:**

- Slower (real database/service calls)
- Requires test environment setup
- Needs cleanup logic

---

## Implementation Roadmap

### Phase 1: Compile-Time Validation (2 weeks, 8 SP)

**Goal:** Type checking and dimension safety against handlers.yaml schema

**Tasks:**

1. AST parser for Starlark scripts
2. Service call extractor
3. Schema validator with type checking
4. **Conservation of Dimension validator** (Physics vs Money boundaries)
5. CLI command: `meridian-cli saga validate`

**Acceptance criteria:**

- Detects undefined handlers
- Detects missing required parameters
- Detects type mismatches (where statically determinable)
- **Enforces category boundaries (Settlement/Physics/Financial)**
- Blocks invalid cross-dimension writes

**Rationale:** Foundation for all other validation. Type safety from handlers.yaml schema
prevents entire classes of runtime errors.

---

### Phase 2: Unit Testing DSL (3 weeks, 13 SP)

**Goal:** Tenant-writable unit tests with mocking

**Priority:** HIGH - Unblocks tenant self-service testing

**Tasks:**

1. Test framework built-ins (@test.case, test.mock(), assertions)
2. MockRegistry for intercepting service calls
3. Test execution engine with TestMode flag
4. Coverage reporting
5. CLI command: `meridian-cli saga test`

**Acceptance criteria:**

- Tenants can write `.star_test` files
- Mocks service module handlers defined in handlers.yaml
- Reports pass/fail and coverage metrics
- **TestMode flag isolates mocks from real HandlerRegistry**

**Critical implementation detail:**

```go
// shared/pkg/saga/starlark_runner.go
type StarlarkSagaRunner struct {
    runtime        *Runtime
    registry       *HandlerRegistry
    mockRegistry   *MockRegistry  // NEW: For test mode
    testMode       bool            // NEW: Flag to enable mocking
}

// When testMode=true, invoke_handler checks mockRegistry first
```

**Rationale:** Architect feedback - "Most important part of the framework" due to move to
real gRPC service bindings (no more in-process mocks).

---

### Phase 3: Replay Determinism Testing (1 week, 5 SP)

**Goal:** Verify sagas are replay-safe for Durable Execution Engine

**Priority:** CRITICAL - ADR-028 requirement, blocks production use

**Tasks:**

1. Execution trace capture (handler calls, UUIDs, step order)
2. `test.verify_replay_determinism()` built-in
3. Seeded UUID generator for deterministic replay
4. Trace comparison and diff reporting

**Acceptance criteria:**

- Detects non-deterministic branching (e.g., `random.random()` usage)
- Verifies `new_uuid()` produces identical sequences on replay
- Validates same handler call order on replay
- CLI flag: `meridian-cli saga validate --check-replay`

**Rationale:** Architect feedback - "Missing feature" essential for durable execution. Catches replay bugs before production.

---

### Phase 4: Static Analysis & Side-Effect Audit (1 week, 5 SP)

**Goal:** Linting, code quality, and blast radius visibility

**Tasks:**

1. Unused variable detection
2. Unreachable code detection
3. Cyclomatic complexity metrics
4. Compensation coverage analysis
5. **Side-Effect Audit Manifest** (READ vs WRITE operations)
6. CLI commands: `meridian-cli saga lint`, `meridian-cli saga audit`

**Acceptance criteria:**

- Produces actionable warnings
- Configurable severity levels
- **Generates manifest of all service calls with READ/WRITE classification**
- Integrates with CI/CD

**Rationale:** Side-Effect Audit provides human-readable summary of saga blast radius before activation.

---

### Phase 5: Integration Testing (2 weeks, 8 SP)

**Goal:** Test against real services (non-production only)

**Tasks:**

1. EnvironmentType detection (production/staging/dev/test)
2. **Production blocking for `test.integration_mode()`**
3. Test environment provisioning
4. Test data fixtures
5. Cleanup automation
6. Performance benchmarking

**Acceptance criteria:**

- **BLOCKS `test.integration_mode()` in PRODUCTION environments (fatal error)**
- Tests run against test/dev/staging clusters only
- Automatic cleanup after test run
- Performance regression detection

**Safety requirement:** Runtime must detect environment and refuse integration mode in production.

---

### Phase 6: Property-Based Testing (1 week, 5 SP)

**Goal:** Generative testing for edge case coverage

**Tasks:**

1. Value generators (UUID, Decimal, etc.)
2. Property test executor
3. Shrinking (minimal failing case)

**Acceptance criteria:**

- 100+ random test cases per property
- Shrinks to minimal failure
- Integrates with unit testing

**Use case:** Testing tiered pricing, complex CEL formulas, boundary conditions.

---

## Implementation Priority

**Architect's recommended order:**

1. **Phase 1** (Validation) - Foundation, type safety from handlers.yaml
2. **Phase 2** (Unit Testing) - Unblocks tenant self-service
3. **Phase 3** (Replay Safety) - Critical for ADR-028 compliance

Phases 4-6 can proceed in parallel or be deferred based on demand.

---

## Alternative: Python-Style `doctest`

**Concept:** Embed tests in script comments.

**Example:**

```python
# withdrawal.star

def calculate_fee(amount):
    """Calculate withdrawal fee.

    >>> calculate_fee(Decimal("100.00"))
    Decimal("1.00")

    >>> calculate_fee(Decimal("0.50"))
    Decimal("0.01")
    """
    return amount * Decimal("0.01")

# meridian-cli saga doctest withdrawal.star
# ✅ All doctests passed
```

**Pros:**

- Documentation and tests in one place
- Low friction (no separate test files)

**Cons:**

- Limited assertions
- No mocking support
- Not suitable for integration tests

**Recommendation:** Support doctests for simple functions, full testing DSL for sagas.

---

## Open Questions

### 1. Where do tests live?

#### Option A: Co-located with scripts

```text
reference-data/saga/defaults/withdrawal/
├── v1.0.0.star
└── v1.0.0_test.star
```

**Pros:** Easy to find, versioned together
**Cons:** Test files deployed to production?

#### Option B: Separate test directory

```text
reference-data/saga/defaults/withdrawal/
└── v1.0.0.star

reference-data/saga/tests/withdrawal/
└── v1.0.0_test.star
```

**Pros:** Clean separation
**Cons:** Harder to keep in sync

**Decision:** **Option A** - `.star_test` extension alongside source scripts.

**Rationale:**

- Storing tests in Reference Data service ensures version coupling (saga v1 → tests v1)
- When saga is versioned (v1.0.0 → v2.0.0), tests travel with the script
- The `ActivateSaga` RPC should require `run_tests=true` flag - green test report is a **precondition** for activation
- `.star_test` files excluded from production runtime (filtered by file extension)

**Integration with ActivateSaga:**

```go
// services/reference-data/api/saga_activation.go
func (s *Service) ActivateSaga(ctx context.Context, req *ActivateSagaRequest) error {
    // 1. Fetch saga script
    script := s.repository.GetSagaScript(req.SagaName, req.Version)

    // 2. Fetch test script (.star_test)
    testScript := s.repository.GetSagaTestScript(req.SagaName, req.Version)

    // 3. Run tests (if run_tests=true flag set)
    if req.RunTests && testScript != nil {
        testReport := s.testRunner.RunTests(testScript)
        if !testReport.AllPassed {
            return fmt.Errorf("activation blocked: %d tests failed", testReport.FailedCount)
        }
    }

    // 4. Activate saga only if tests pass
    return s.activateSaga(script)
}
```

This approach makes testing a **gating mechanism** for production deployment, not an optional add-on.

---

### 2. How to test CEL expressions?

CEL expressions are embedded in Starlark scripts:

```python
rate = cel_eval("spot * coefficient * markup", {
    "spot": Decimal("50.00"),
    "coefficient": Decimal("1.02"),
    "markup": Decimal("1.05")
})
```

**Testing approach:**

```python
# Test CEL expression directly
@test.case("cel_expression_pricing")
def test_cel_pricing():
    result = cel_eval("spot * coefficient * markup", {
        "spot": Decimal("50.00"),
        "coefficient": Decimal("1.02"),
        "markup": Decimal("1.05")
    })

    test.assert_equal(result, Decimal("53.55"))
```

**Alternative:** Extract CEL expressions to reference data, test separately.

---

### 3. How to test bi-temporal behavior?

Sagas use `knowledge_at` for deterministic lookups:

```python
price = market_data.get_price("EPEX_SPOT_GBP", knowledge_at)
```

**Testing approach:**

```python
# Mock with specific knowledge_at
@test.case("price_lookup_at_timestamp")
def test_price_lookup():
    test.set_knowledge_at("2026-02-05T10:00:00Z")

    test.mock("market_data.get_price", returns={
        "value": Decimal("50.00"),
        "timestamp": "2026-02-05T09:59:00Z",  # Before knowledge_at
    })

    result = execute_saga("valuation", {...})
    test.assert_equal(result["price"], Decimal("50.00"))
```

---

## Success Metrics

1. **95% of saga scripts have tests** before production deployment
2. **Zero undefined handler errors** in production (caught by validation)
3. **80% code coverage** across saga test suites
4. **10x faster tenant iteration** (local testing vs production debugging)
5. **100% compensation coverage** (every step has compensation path tested)

---

## Risks & Mitigation

| Risk | Likelihood | Impact | Mitigation |
|------|------------|--------|------------|
| Test framework too complex | Medium | High | Start simple (assertions + mocks), iterate |
| Tenants don't write tests | High | High | Make testing part of deployment gate |
| Mocks diverge from reality | Medium | High | Integration tests catch divergence |
| Performance overhead | Low | Medium | Profile, optimize test execution |

---

## Future Enhancements

1. **Mutation testing** - Verify tests catch bugs
2. **Fuzz testing** - Random input generation
3. **Visual coverage reports** - HTML coverage viewer
4. **Test marketplace** - Share test suites across tenants
5. **AI test generation** - LLM generates tests from script

---

## Related Work

- **Python `unittest`** - Assertion library inspiration
- **Go `testing`** - Table-driven test pattern
- **Rust QuickCheck** - Property-based testing
- **Starlark test framework** (Bazel) - Limited, no mocking

---

## Questions for Review

1. Should tests be mandatory for saga deployment?
2. What's the right balance between unit tests (fast) and integration tests (realistic)?
3. Should we support Python-style `doctest` for simple functions?
4. How to prevent test flakiness in distributed saga testing?
5. Should tenants be able to test against production data (read-only snapshots)?

---

## Appendix: Example Validation Report

```bash
$ meridian-cli saga validate withdrawal.star

Validation Report for withdrawal.star
=====================================

✅ Syntax: OK

✅ Type Checking: OK

✅ Service Modules:
  • position_keeping.initiate_log ✓
  • financial_accounting.capture_posting ✓
  • current_account.save ✓

⚠️  Warnings (3):
  • Line 42: Unused variable 'temp_result'
  • Line 76: Use '!= None' instead of 'is not None' (Starlark compatibility)
  • Line 89: Step 'capture_credit_posting' has no compensation handler

❌ Errors (1):
  • Line 102: Missing required parameter 'currency' in position_keeping.initiate_log()

Static Analysis:
  • Cyclomatic Complexity: 8 (threshold: 10) ✓
  • Compensation Coverage: 83% (5/6 steps) ⚠️
  • Reachable Steps: 100% (6/6) ✓

Recommendation: Fix errors before deployment. Address warnings for production readiness.
```
