# Starlark Saga Migration Status

## Overview

This document tracks the migration from Go-defined sagas (using `saga.AddStep()`) to Starlark-defined sagas (using `StarlarkSagaRunner.ExecuteSaga()`).

## Migration Goals

1. **Consolidate saga scripts**: All saga definitions in `services/reference-data/saga/defaults/`
2. **Remove duplicate scripts**: No saga `.star` files in service directories
3. **Migrate orchestrators**: All orchestrators use `StarlarkSagaRunner.ExecuteSaga()`
4. **Implement handlers**: All saga handlers use real service clients (no stubs/NoOps)

## Current Status

### ✅ Completed

#### Script Consolidation (Task 21)

- ✅ Removed `services/current-account/sagas/deposit.star`
- ✅ Removed `services/current-account/sagas/withdrawal.star`
- ✅ Removed `services/payment-order/sagas/payment_execution.star`
- ✅ All canonical scripts in `services/reference-data/saga/defaults/`
  - `deposit/v1.0.0.star`
  - `withdrawal/v1.0.0.star`
  - `payment_execution/v1.0.0.star`

#### Current-Account Migration (Task 20)

- ✅ `deposit_orchestrator.go` uses `StarlarkSagaRunner.ExecuteSaga()`
- ✅ `withdrawal_orchestrator.go` uses `StarlarkSagaRunner.ExecuteSaga()`
- ✅ No `saga.AddStep()` calls in current-account service

#### Handler Implementations (Tasks 9-19)

- ✅ Position-keeping handlers implemented (Task 9)
- ✅ Current-account handlers implemented (Task 10)
- ✅ Financial-accounting handlers implemented (Task 11)
- ✅ Payment-order handlers implemented (Task 12)
- ✅ Missing service handlers audited and implemented (Task 19)

### ❌ Remaining Work

#### Payment-Order Migration (Implicit Dependency)

- ❌ `payment_orchestrator.go` still uses `saga.AddStep()` pattern
- ❌ Lines 184-290: `addReserveFundsStep()` and `addSendToGatewayStep()` use old pattern
- 🎯 **Action Required**: Migrate payment-order to StarlarkSagaRunner similar to Task 20

## Migration Strategy for Payment-Order

The payment-order service needs the same migration approach as Task 20 (current-account):

1. **Add StarlarkSagaRunner field** to `PaymentOrchestrator` struct
2. **Load payment_execution saga script** at orchestrator initialization (from reference-data service or local cache)
3. **Replace `Orchestrate()` method** to use `StarlarkSagaRunner.ExecuteSaga()` instead of manual saga building
4. **Remove AddStep helper methods** (`addReserveFundsStep`, `addSendToGatewayStep`)
5. **Keep PostLedgerEntriesFromParams** - already designed for Starlark handler integration

## Verification

Use the automated verification script to check migration status:

```bash
./scripts/verify-starlark-migration.sh
```

### Expected Output (Current State)

```text
=== Starlark Migration Verification ===

[1/6] Checking script consolidation...
  ✅ PASS: No duplicate saga scripts in current-account/sagas/
  ✅ PASS: No duplicate saga scripts in payment-order/sagas/
  ✅ PASS: Found 3 canonical saga scripts in reference-data/saga/defaults/

[2/6] Checking for saga.AddStep() pattern in production code...
  ❌ FAIL: Found saga.AddStep() in production code (payment-order only)

[3/6] Checking StarlarkSagaRunner usage in orchestrators...
  ✅ deposit_orchestrator.go uses StarlarkSagaRunner
  ✅ withdrawal_orchestrator.go uses StarlarkSagaRunner
  ❌ payment_orchestrator.go does NOT use StarlarkSagaRunner

[4/6] Checking handler implementations for NoOp/stub patterns...
  ✅ PASS: No NoOp or stub handlers found

[5/6] Verifying canonical saga scripts...
  ✅ deposit/v1.0.0.star exists and has content
  ✅ withdrawal/v1.0.0.star exists and has content
  ✅ payment_execution/v1.0.0.star exists and has content

[6/6] Summary
  ❌ SOME VERIFICATIONS FAILED

=== MIGRATION STATUS: PAYMENT-ORDER PENDING ===
```

## Testing Strategy

### Phase 1: Current-Account Validation

- ✅ Test deposit saga via StarlarkSagaRunner
- ✅ Test withdrawal saga via StarlarkSagaRunner
- ✅ Run current-account E2E tests

### Phase 2: Payment-Order Migration (Future Work)

- Migrate payment_orchestrator.go to StarlarkSagaRunner
- Test payment_execution saga via StarlarkSagaRunner
- Run payment-order E2E tests
- Verify no `saga.AddStep()` remains in codebase

### Phase 3: Integration Testing

- Cross-service saga flows
- Multi-tenant scenarios
- Performance benchmarks

## Related Files

- `shared/pkg/saga/starlark_runner.go` - StarlarkSagaRunner implementation
- `services/reference-data/saga/grpc_handler.go` - GetSaga RPC handler
- `services/current-account/service/*orchestrator.go` - Migrated orchestrators (✅)
- `services/payment-order/service/payment_orchestrator.go` - Pending migration (❌)
- `scripts/verify-starlark-migration.sh` - Automated verification tool

## Next Steps

1. **Complete payment-order migration** (similar to Task 20)
2. **Run full verification script** and ensure all checks pass
3. **Execute E2E test suite** for all services
4. **Performance benchmarking** (compare Starlark vs Go baseline)
5. **Update ADRs** to reflect Starlark-first architecture
