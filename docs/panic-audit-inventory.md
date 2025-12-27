# Panic Usage Audit Inventory

**Date**: 2025-12-27
**Task**: tech-debt-cleanup.25 - Audit remaining production panic usage

## Executive Summary

This document catalogs all `panic()` occurrences across the Meridian codebase
and categorizes them for review. The goal is to identify which panics are
acceptable and which need refactoring to error returns.

**Total panics found**: 48

- **Production code panics**: 32
- **Test file panics**: 16

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

These panics occur during service initialization and follow the fail-fast
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
`Must` prefix to indicate the behavior. Callers choose to use these when
they know the operation cannot fail (e.g., with compile-time constants).

| # | File | Line | Function | Notes |
|---|------|------|----------|-------|
| 14 | `shared/domain/money/money.go` | 111 | `MustNew` | For tests/compile-time constants |
| 15 | `internal/audit-consumer/domain/measurement.go` | 48 | `MustPeriod` | For tests/initialization |
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

These are in `*_test.go` files and are expected behavior for test setup or
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
| **Total** | **48** | |

## Conclusion

All 48 panic occurrences fall into acceptable categories:

1. **Startup/constructor validation** (13): These follow the fail-fast
   pattern for catching configuration errors before the service starts.
   This is a widely accepted Go pattern for dependency injection constructors.

2. **Must* function variants** (6): These are explicitly named to indicate
   panic behavior and are documented for use with compile-time constants or
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
