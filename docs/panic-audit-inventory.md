# Panic Usage Audit Inventory

**Date**: 2025-12-27
**Task**: tech-debt-cleanup.25 - Audit remaining production panic usage
**Status**: COMPLETE - No refactoring required

## Executive Summary

### Audit Conclusion

**All 47 panic occurrences in the Meridian codebase follow Go best practices.
No refactoring is required.**

This audit was conducted to identify any runtime panics that should be
refactored to return errors instead. After thorough analysis, we found:

| Finding | Count | Action Required |
|---------|-------|-----------------|
| Startup/initialisation panics | 19 | None - follows fail-fast pattern |
| Bug detection invariants | 1 | None - defensive programming |
| Panic propagation (defer cleanup) | 1 | None - standard Go pattern |
| Test fixture panics | 11 | None - test code only |
| Test file panics | 15 | None - test code only |
| **Runtime panics needing refactor** | **0** | **None** |

### Key Findings

1. **No runtime panics require refactoring.** Unlike the earlier audit (Tasks
   20-24) which identified and fixed actual problematic panics, this audit
   confirms the remaining panics are all legitimate uses.

2. **Startup panics are appropriate.** The 19 constructor/initialisation panics
   follow the Go fail-fast pattern for dependency validation. They fire before
   the service handles any requests.

3. **Must* functions follow Go conventions.** Functions with `Must` prefix
   (like `regexp.MustCompile`) explicitly signal panic behaviour and have
   non-panicking alternatives.

4. **The codebase is healthy.** The previous refactoring work (Tasks 20-24)
   successfully eliminated all problematic panic usage.

### Audit Statistics

**Total panics found**: 47

- **Production code panics**: 32
- **Test file panics**: 15

## Category Definitions

| Category | Description | Action |
|----------|-------------|--------|
| **A - Startup/Init** | Constructor validation at startup. Fail-fast pattern. | OK |
| **B - Must Functions** | Functions with `Must` prefix that panic on error. | OK |
| **C - Bug Detection** | Panic indicates impossible state (invariant violation). | OK |
| **D - Propagation** | Re-panics after cleanup in defer blocks. | OK |
| **E - Needs Refactor** | Panics that should return errors instead. | Fix |
| **F - Test/Fixture** | Panics in test files or test fixture packages. | OK |

---

## Production Code Panics (Non-Test Files)

### Category A: Startup/Constructor Panics (Acceptable)

These panics occur during service initialisation and follow the fail-fast
pattern for dependency validation.

| # | File | Line | Function | Message |
|---|------|------|----------|---------|
| 1 | `shared/pkg/health/http.go` | 16 | `NewHTTPHandler` | aggregator nil |
| 2 | `shared/pkg/health/checkers.go` | 20 | `NewDatabaseChecker` | pool nil |
| 3 | `shared/pkg/health/checkers.go` | 65 | `NewRedisChecker` | client nil |
| 4 | `shared/pkg/health/checkers.go` | 115 | `NewKafkaChecker` | checkFunc nil |
| 5 | `services/party/service/health.go` | 45 | `NewHealthChecker` | repository nil |
| 6 | `services/position-keeping/observability/redis_health.go` | 20 | `NewRedisChecker` | client nil |
| 7 | `services/position-keeping/observability/health.go` | 21 | `NewPgxPoolChecker` | pool nil |
| 8 | `shared/platform/events/publisher.go` | 24 | `NewOutboxPublisher` | empty service name |
| 9 | `shared/platform/events/outbox_pgx.go` | 329 | `NewPgxOutboxPublisher` | empty service name |
| 10 | `services/current-account/service/withdrawal_orchestrator.go` | 54 | `NewWithdrawalOrchestrator` | logger nil |
| 11 | `services/current-account/service/withdrawal_orchestrator.go` | 57 | `NewWithdrawalOrchestrator` | repository nil |
| 12 | `services/current-account/service/withdrawal_orchestrator.go` | 60 | `NewWithdrawalOrchestrator` | pos keeping nil |
| 13 | `services/current-account/service/withdrawal_orchestrator.go` | 63 | `NewWithdrawalOrchestrator` | fin acct nil |

**Subtotal**: 13 panics

---

### Category B: Must* Functions (Acceptable)

These are convenience functions that panic on error, explicitly named with
`Must` prefix to indicate the behaviour. Callers choose to use these when
they know the operation cannot fail (e.g., with compile-time constants).

| # | File | Line | Function | Notes |
|---|------|------|----------|-------|
| 14 | `shared/domain/money/money.go` | 111 | `MustNew` | For tests/compile-time constants |
| 15 | `services/audit-worker/domain/measurement.go` | 48 | `MustPeriod` | For tests/initialisation |
| 16 | `shared/platform/tenant/context.go` | 33 | `MustFromContext` | Programming error detection |
| 17 | `shared/platform/tenant/tenant_id.go` | 32 | `MustNewTenantID` | For compile-time constants |
| 18 | `shared/platform/db/gorm_tenant_scope.go` | 81 | `MustWithGormTenantScope` | After middleware validation |
| 19 | `shared/platform/db/tenant_scope.go` | 62 | `MustWithTenantScope` | After middleware validation |

**Subtotal**: 6 panics

---

### Category C: Bug Detection Panics (Acceptable)

These panics detect impossible states that indicate programming bugs
(invariant violations).

| # | File | Line | Function | Notes |
|---|------|------|----------|-------|
| 20 | `services/current-account/domain/account.go` | 212 | `calculateAvailableBalance` | Currency/overflow bug |

**Subtotal**: 1 panic

---

### Category D: Panic Propagation (Acceptable)

These re-panic after cleanup (e.g., transaction rollback) to preserve the
original panic.

| # | File | Line | Function | Notes |
|---|------|------|----------|-------|
| 21 | `shared/platform/db/transaction.go` | 84 | `WithTransaction` | Re-panic after rollback |

**Subtotal**: 1 panic

---

### Category F: Test Fixture Panics (Acceptable)

These are in test fixture packages, not production code.

| # | File | Line | Function | Notes |
|---|------|------|----------|-------|
| 22 | `services/financial-accounting/domain/testfixtures/fixtures.go` | 100 | `Build` | Invalid money |
| 23 | `services/financial-accounting/domain/testfixtures/fixtures.go` | 134 | `Build` | Invalid cents |
| 24 | `services/financial-accounting/domain/testfixtures/fixtures.go` | 176 | `Build` | Posting failed |
| 25 | `services/financial-accounting/domain/testfixtures/fixtures.go` | 193 | `GBP` | Invalid amount |
| 26 | `services/financial-accounting/domain/testfixtures/fixtures.go` | 197 | `GBP` | Construction failed |
| 27 | `services/financial-accounting/domain/testfixtures/fixtures.go` | 206 | `USD` | Invalid amount |
| 28 | `services/financial-accounting/domain/testfixtures/fixtures.go` | 210 | `USD` | Construction failed |
| 29 | `services/financial-accounting/domain/testfixtures/fixtures.go` | 219 | `EUR` | Invalid amount |
| 30 | `services/financial-accounting/domain/testfixtures/fixtures.go` | 223 | `EUR` | Construction failed |
| 31 | `services/financial-accounting/domain/testfixtures/fixtures.go` | 232 | `GBPCents` | Construction failed |
| 32 | `services/financial-accounting/domain/testfixtures/fixtures.go` | 241 | `USDCents` | Construction failed |

**Subtotal**: 11 panics

---

## Test File Panics (Acceptable)

These are in `*_test.go` files and are expected behaviour for test setup or
intentional panic testing.

| # | File | Line | Function | Notes |
|---|------|------|----------|-------|
| 33 | `tests/proto/validation_test.go` | 26 | Test setup | Validator creation |
| 34 | `services/payment-order/service/grpc_service_test.go` | 682 | `testOrchestrator` | Helper |
| 35 | `shared/pkg/interceptors/recovery_test.go` | 43 | Test function | Recovery test |
| 36 | `shared/pkg/interceptors/recovery_test.go` | 67 | Test function | Recovery test |
| 37 | `shared/pkg/interceptors/recovery_test.go` | 90 | Test function | Recovery test |
| 38 | `shared/pkg/interceptors/recovery_test.go` | 153 | Test function | Recovery test |
| 39 | `shared/pkg/interceptors/recovery_test.go` | 233 | Test function | Recovery test |
| 40 | `shared/pkg/interceptors/recovery_test.go` | 240 | Test function | Recovery test |
| 41 | `services/position-keeping/service/adapters_test.go` | 1036 | Test helper | Setup |
| 42 | `services/position-keeping/service/adapters_test.go` | 1040 | Test helper | Setup |
| 43 | `services/position-keeping/service/adapters_test.go` | 1060 | Test helper | Setup |
| 44 | `services/payment-order/adapters/http/server_test.go` | 417 | Test function | Panic test |
| 45 | `services/current-account/service/grpc_service_test.go` | 176 | Test helper | Setup |
| 46 | `services/current-account/service/grpc_service_integration_test.go` | 43 | Test helper | Setup |
| 47 | `services/current-account/service/grpc_service_integration_test.go` | 391 | Test helper | Setup |

**Subtotal**: 15 panics

---

## Summary by Category

| Category | Count | Status |
|----------|-------|--------|
| A - Startup/Init Panics | 13 | Acceptable |
| B - Must* Functions | 6 | Acceptable |
| C - Bug Detection | 1 | Acceptable |
| D - Panic Propagation | 1 | Acceptable |
| E - Needs Refactoring | 0 | N/A |
| F - Test Fixtures | 11 | Acceptable |
| Test Files | 15 | Acceptable |
| **Total** | **47** | |

## Conclusion

All 47 panic occurrences fall into acceptable categories:

1. **Startup/constructor validation** (13): These follow the fail-fast
   pattern for catching configuration errors before the service starts.
   This is a widely accepted Go pattern for dependency injection constructors.

2. **Must* function variants** (6): These are explicitly named to indicate
   panic behaviour and are documented for use with compile-time constants or
   well-validated input.

3. **Bug detection** (1): The overdraft calculation panic indicates an
   invariant violation that should never occur if validations are working
   correctly. This is appropriate defensive programming.

4. **Panic propagation** (1): Re-panicking after transaction rollback is
   standard Go error handling in defer blocks.

5. **Test fixtures** (11): Panics in `testfixtures` package are acceptable
   as they simplify test code and fail fast on test setup errors.

6. **Test files** (15): Panics in test files are acceptable - many are
   intentional for testing panic recovery.

**No refactoring is required.** The codebase follows Go best practices for
panic usage.

### Follow-up Tasks

**None required.** This audit confirms the panic cleanup work from Tasks 20-24
was successful. No additional Task Master tasks have been created because:

- All production panics are appropriately placed (startup or Must* functions)
- No runtime panics need refactoring to error returns
- Test panics are acceptable for test code

---

## Approval Status: Startup and Initialisation Panics

**Reviewed**: 2025-12-27
**Subtask**: tech-debt-cleanup.25.2

This section provides detailed approval rationale for all startup/initialisation
panics and Must* function variants identified in the inventory.

### Approval Criteria

Startup panics are **APPROVED** when they meet ALL of the following criteria:

1. **Fail-fast timing**: Panic occurs during service initialisation, before handling any requests
2. **Critical dependency**: The nil/empty value would make the service non-functional
3. **No recovery possible**: Returning an error would just propagate up to main() anyway
4. **Clear messaging**: Panic message identifies which dependency is missing

### Category A: Startup/Constructor Panics - APPROVED

All 13 startup panics are **APPROVED** with the following rationale:

#### Health Check Constructors (4 panics)

| # | Location | Rationale |
|---|----------|-----------|
| 1 | `NewHTTPHandler(aggregator)` | Without aggregator, health endpoints cannot report component status. Service would appear healthy when it cannot verify dependencies. **APPROVED - STARTUP** |
| 2 | `NewDatabaseChecker(pool)` | Database health checker is useless without a connection pool. Called once at startup when wiring dependencies. **APPROVED - STARTUP** |
| 3 | `NewRedisChecker(client)` | Redis health checker is useless without a client. Called once at startup when wiring dependencies. **APPROVED - STARTUP** |
| 4 | `NewKafkaChecker(checkFunc)` | Kafka health checker is useless without a check function. Called once at startup when wiring dependencies. **APPROVED - STARTUP** |

**Pattern justification**: Health checkers are instantiated once during service startup in main.go
or dependency injection setup. A nil pool/client/function indicates a wiring bug in the startup
code - the service should not start in this broken state.

#### Service-Specific Health Checkers (2 panics)

| # | Location | Rationale |
|---|----------|-----------|
| 5 | `party/NewHealthChecker(repository)` | Party service health check requires repository to verify data layer. **APPROVED - STARTUP** |
| 6 | `position-keeping/NewRedisChecker(client)` | Position-keeping specific Redis checker. **APPROVED - STARTUP** |
| 7 | `position-keeping/NewPgxPoolChecker(pool)` | Position-keeping specific Postgres checker. **APPROVED - STARTUP** |

**Pattern justification**: Service-specific health checkers follow the same pattern as shared ones.
They are constructed during dependency injection and a nil dependency indicates a wiring bug.

#### Event Publisher Constructors (2 panics)

| # | Location | Rationale |
|---|----------|-----------|
| 8 | `NewOutboxPublisher(serviceName)` | Empty service name would cause malformed event metadata, making event correlation impossible. Service name is a compile-time constant. **APPROVED - STARTUP** |
| 9 | `NewPgxOutboxPublisher(serviceName, ...)` | Same rationale as above - service name is required for event tracing. **APPROVED - STARTUP** |

**Pattern justification**: Service name is passed from main.go and should be a non-empty constant.
An empty service name indicates a configuration bug that should fail deployment, not silently
produce broken events.

#### Saga Orchestrator Constructors (4 panics)

| # | Location | Rationale |
|---|----------|-----------|
| 10 | `NewWithdrawalOrchestrator(cfg.Logger)` | Logger is required for audit trail of financial operations. **APPROVED - STARTUP** |
| 11 | `NewWithdrawalOrchestrator(cfg.Repo)` | Repository is required to persist account state changes. **APPROVED - STARTUP** |
| 12 | `NewWithdrawalOrchestrator(cfg.PosKeepingClient)` | Position-keeping client is required for the saga workflow. **APPROVED - STARTUP** |
| 13 | `NewWithdrawalOrchestrator(cfg.FinAcctClient)` | Financial accounting client is required for the saga workflow. **APPROVED - STARTUP** |

**Pattern justification**: The withdrawal orchestrator implements a financial saga that coordinates
multiple services. All four dependencies are mandatory for the saga to function. These are
validated at service startup when the gRPC server is being wired. A nil dependency indicates
incomplete dependency injection.

### Category B: Must* Function Variants - APPROVED

All 6 Must* functions are **APPROVED** with the following rationale:

| # | Function | Usage Pattern | Rationale |
|---|----------|---------------|-----------|
| 14 | `money.MustNew` | Tests and compile-time constants | Explicitly named with `Must` prefix. Alternative `New` exists for runtime use. Used in test fixtures and constant initialisation. **APPROVED - MUST PATTERN** |
| 15 | `measurement.MustPeriod` | Tests and initialisation | Same Must pattern. Used when period validity is known at compile time. **APPROVED - MUST PATTERN** |
| 16 | `tenant.MustFromContext` | After middleware validation | Called only in code paths where tenant middleware has already validated context. Comment in source documents this is a "programming error" detector. **APPROVED - MUST PATTERN** |
| 17 | `tenant.MustNewTenantID` | Compile-time tenant constants | Used for system-level tenant IDs that are compile-time constants. Alternative `NewTenantID` exists. **APPROVED - MUST PATTERN** |
| 18 | `db.MustWithGormTenantScope` | After middleware validation | Called in code paths where tenant context is guaranteed by middleware. **APPROVED - MUST PATTERN** |
| 19 | `db.MustWithTenantScope` | After middleware validation | Same pattern as above. **APPROVED - MUST PATTERN** |

**Pattern justification**: The `Must` prefix is a Go convention (see `regexp.MustCompile`,
`template.Must`) that clearly signals "this function panics on error". Each `Must` function has
a corresponding non-panicking variant. Usage is appropriate:

- Test code: Panics are acceptable to fail fast on test setup errors
- Compile-time constants: Values are known-valid at code review time
- Post-middleware: Middleware has already validated the precondition

### Summary of Approvals

| Category | Count | Status |
|----------|-------|--------|
| Health check constructors | 7 | APPROVED - STARTUP |
| Event publisher constructors | 2 | APPROVED - STARTUP |
| Saga orchestrator constructors | 4 | APPROVED - STARTUP |
| Must* function variants | 6 | APPROVED - MUST PATTERN |
| **Total reviewed** | **19** | **All APPROVED** |

All startup/initialisation panics follow established Go patterns for fail-fast
initialisation and are acceptable. No refactoring is required.

---

## Runtime Panic Analysis

**Reviewed**: 2025-12-27
**Subtask**: tech-debt-cleanup.25.3

This section analyzes the remaining non-startup, non-Must* panics to determine if any
occur during runtime request handling and require refactoring.

### Scope

From the 47 total panics:

- **Startup panics (Category A)**: 13 - Already approved in subtask 25.2
- **Must* functions (Category B)**: 6 - Already approved in subtask 25.2
- **Test fixtures (Category F)**: 11 - Acceptable for test code
- **Test files**: 15 - Acceptable for test code
- **Remaining to analyze**: 2 (Categories C and D)

### Category C: Bug Detection Panic - APPROVED

**Location**: `services/current-account/domain/account.go:212`
**Function**: `calculateAvailableBalance`

```go
func calculateAvailableBalance(balance, overdraftLimit Money, overdraftEnabled bool) Money {
    if overdraftEnabled {
        newAvail, err := balance.Add(overdraftLimit)
        if err != nil {
            // This indicates a bug: either currency mismatch or overflow that bypassed validation
            panic("BUG: OverdraftLimit currency mismatch or overflow detected...")
        }
        return newAvail
    }
    return balance
}
```

**Analysis**:

| Criterion | Assessment |
|-----------|------------|
| **Why it panics** | `Money.Add()` fails due to currency mismatch or integer overflow |
| **When it would trigger** | Only if upstream validation is broken (balance and overdraftLimit have different currencies, or sum exceeds int64 bounds) |
| **Is this a runtime panic?** | Technically yes - it could be called during a request. However... |
| **Can it actually be triggered?** | No - `SetOverdraftLimit` validates currency match and reasonable limits at account creation/update |
| **Proper approach** | The panic is appropriate defensive programming |
| **Impact if triggered** | Request fails (panic recovered by gRPC interceptor), indicates serious data corruption bug |
| **Priority** | N/A - no refactoring needed |

**Verdict**: **APPROVED - BUG DETECTION**

This is an *invariant assertion*, not error handling. The panic:

1. Detects impossible states that indicate a programming bug
2. Provides clear diagnostic message with "BUG:" prefix
3. Would only trigger if account validation is broken elsewhere
4. Is a valid use of panic per Go philosophy: "Don't use panic for normal error handling"

The correct fix if this ever triggers is to fix the upstream validation bug, not to change
this to an error return. Returning an error would hide the bug and potentially allow
corrupted data to propagate.

### Category D: Panic Propagation - APPROVED

**Location**: `shared/platform/db/transaction.go:84`
**Function**: `WithTransaction` (defer block)

```go
defer func() {
    if p := recover(); p != nil {
        // Panic occurred, rollback and re-panic
        _ = txWrapper.Rollback()
        panic(p)
    } else if err != nil {
        // Function returned error, rollback
        if rbErr := txWrapper.Rollback(); rbErr != nil {
            err = fmt.Errorf("%w (rollback failed: %w)", err, rbErr)
        }
    }
}()
```

**Analysis**:

| Criterion | Assessment |
|-----------|------------|
| **Why it panics** | Re-throws original panic after transaction cleanup |
| **When it would trigger** | When code inside the transaction panics |
| **Is this a runtime panic?** | Yes, but it's re-propagating an existing panic, not creating one |
| **Proper approach** | This IS the proper approach |
| **Impact if triggered** | Original panic continues up the stack with transaction rolled back |
| **Priority** | N/A - no refactoring needed |

**Verdict**: **APPROVED - PANIC PROPAGATION**

This is the standard Go pattern for cleanup during panic recovery:

1. Catch panic with `recover()`
2. Perform cleanup (rollback transaction)
3. Re-panic with original value to preserve stack trace

This pattern is documented in Go's official blog post "Defer, Panic, and Recover" and
is necessary to ensure database transactions are properly rolled back even when code panics.
The gRPC interceptor will ultimately catch and convert this to an error response.

### Summary: No Runtime Panics Requiring Refactoring

| Category | Count | Status | Rationale |
|----------|-------|--------|-----------|
| Bug Detection (C) | 1 | APPROVED | Invariant assertion for impossible states |
| Panic Propagation (D) | 1 | APPROVED | Standard cleanup-and-rethrow pattern |

**Conclusion**: Neither of the remaining panics are "runtime panics requiring refactoring"
in the sense described in Tasks 20-23 (where panics were used for error conditions that
should return errors instead).

- The **bug detection panic** is a defensive assertion that should never trigger in
  correctly validated code. It detects data corruption, not handle business errors.
- The **panic propagation** is infrastructure code that preserves panic semantics
  while ensuring proper cleanup.

**No refactoring is required.** All 47 panics in the codebase follow Go best practices.

---

## Files by Panic Count

| File | Count | Cat |
|------|-------|-----|
| `services/financial-accounting/domain/testfixtures/fixtures.go` | 11 | F |
| `shared/pkg/interceptors/recovery_test.go` | 6 | Test |
| `services/current-account/service/withdrawal_orchestrator.go` | 4 | A |
| `shared/pkg/health/checkers.go` | 3 | A |
| `services/position-keeping/service/adapters_test.go` | 3 | Test |
| `shared/platform/db/` (3 files) | 3 | B, D |
| `shared/platform/tenant/` (2 files) | 2 | B |
| `shared/platform/events/` (2 files) | 2 | A |
| `services/current-account/service/grpc_service_integration_test.go` | 2 | Test |
| Other files | 12 | Mixed |
