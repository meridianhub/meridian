# PRD: Tech Debt Remediation Q1 2026

**Author:** Engineering
**Status:** Draft
**Created:** 2025-12-30
**Target:** Q1 2026

---

## Executive Summary

This PRD defines the technical debt remediation work identified during a comprehensive codebase audit. The work is prioritized by production impact and grouped into logical work streams. Completing this work will improve system reliability, maintainability, and prepare the platform for production deployment.

---

## Goals

1. **Production Readiness**: Eliminate blocking issues that prevent production deployment
2. **Code Quality**: Reduce maintenance burden through DRY improvements and standardization
3. **Observability**: Complete alerting and event infrastructure for operational visibility
4. **Feature Completeness**: Close implementation gaps in core banking operations

---

## Non-Goals

- New feature development (covered in separate PRDs)
- Performance optimization (separate initiative)
- ADR-0013/0014/0017 implementation (separate PRDs)

---

## Work Streams

### Stream 1: Critical Production Blockers (P0)

#### 1.1 Withdrawal Persistence Implementation

**Problem:** Withdrawal operations are not persisted to the database. Five TODO markers indicate missing implementation.

**Files Affected:**
- `services/current-account/service/grpc_service.go:667` - Pending withdrawal lookup
- `services/current-account/service/grpc_service.go:1009` - Persist withdrawal to database
- `services/current-account/service/grpc_service.go:1039` - Withdrawal lookup and update
- `services/current-account/service/grpc_service.go:1057` - Withdrawal lookup from database
- `services/current-account/service/grpc_service.go:1075` - Withdrawal listing from database

**Acceptance Criteria:**
- [ ] Withdrawal entity persisted to database on initiation
- [ ] Withdrawal status updates persisted
- [ ] Withdrawal retrieval by ID implemented
- [ ] Withdrawal listing with pagination implemented
- [ ] Integration tests cover all persistence operations

**Estimated Effort:** 3-5 days

---

#### 1.2 Idempotency Gap Fix (ledger-integrity#15)

**Problem:** In financial-accounting service, if an error occurs between `MarkPending` and `StoreResult`, the idempotency state can become inconsistent.

**Files Affected:**
- `services/financial-accounting/service/financial_accounting_service.go:644`
- `services/financial-accounting/service/financial_accounting_service.go:1050`

**Acceptance Criteria:**
- [ ] Atomic idempotency state transitions
- [ ] Recovery mechanism for orphaned pending states
- [ ] Cleanup job for stale pending records
- [ ] Unit tests for failure scenarios

**Estimated Effort:** 2-3 days

---

### Stream 2: Event Infrastructure (P1)

#### 2.1 Account Lifecycle Events

**Problem:** Account control operations (freeze, unfreeze, close) do not emit Kafka events or webhook notifications.

**Files Affected:**
- `services/current-account/service/grpc_service.go:1470` - Kafka events
- `services/current-account/service/grpc_service.go:1477` - Webhook notifications

**Acceptance Criteria:**
- [ ] `AccountFrozen` event emitted on freeze
- [ ] `AccountUnfrozen` event emitted on unfreeze
- [ ] `AccountClosed` event emitted on close
- [ ] Webhook notifications for FREEZE and CLOSE (regulatory compliance)
- [ ] Event schema documented in `api/proto/`
- [ ] Integration tests verify event emission

**Estimated Effort:** 2-3 days

---

#### 2.2 Utilization Metering Endpoint

**Problem:** `RecordMeasurement` endpoint missing in PositionKeeping service. Utilization metering consumer runs in simulation mode.

**Files Affected:**
- `services/utilization-metering-consumer/cmd/main.go:153` - SimulationMode flag
- `services/utilization-metering-consumer/adapters/grpc/position_keeping_client.go:118-166`

**Acceptance Criteria:**
- [ ] `RecordMeasurement` RPC added to PositionKeeping proto
- [ ] Endpoint implementation in PositionKeeping service
- [ ] Consumer updated to use real endpoint
- [ ] SimulationMode removed or made opt-in for testing
- [ ] End-to-end integration test

**Estimated Effort:** 3-4 days

---

### Stream 3: Code Consolidation (P2)

#### 3.1 PartyClient Consolidation

**Problem:** Duplicate PartyClient implementations exist despite shared package.

**Files Affected:**
- `services/current-account/clients/party_client.go` (duplicate)
- `services/tenant/clients/party_client.go` (duplicate)
- `shared/pkg/clients/party.go` (canonical)

**Acceptance Criteria:**
- [ ] Both services use `shared/pkg/clients/party.go`
- [ ] Service-specific error types extracted to interfaces
- [ ] Duplicate files removed
- [ ] No breaking changes to existing functionality

**Estimated Effort:** 1 day

---

#### 3.2 Timeout Constants Standardization

**Problem:** Timeout values (30s, 5s, 60s) are duplicated across 15+ locations.

**Proposed Solution:** Create `shared/platform/defaults/timeouts.go`:

```go
package defaults

import "time"

const (
    DefaultRPCTimeout           = 30 * time.Second
    DefaultHealthCheckTimeout   = 5 * time.Second
    DefaultCircuitBreakerTimeout = 60 * time.Second
    DefaultGracefulShutdown     = 30 * time.Second
)
```

**Acceptance Criteria:**
- [ ] Constants package created
- [ ] All services migrated to use constants
- [ ] Documentation added explaining each timeout's purpose

**Estimated Effort:** 1 day

---

#### 3.3 Port Number Centralization

**Problem:** Port numbers (50051-50056, 8080) scattered across configs and tests.

**Proposed Solution:** Create `shared/platform/ports/ports.go`:

```go
package ports

const (
    CurrentAccount       = 50051
    FinancialAccounting  = 50052
    PositionKeeping      = 50053
    PaymentOrder         = 50054
    Party                = 50055
    Tenant               = 50056
    HTTPMetrics          = 8080
)
```

**Acceptance Criteria:**
- [ ] Ports package created
- [ ] All Kubernetes manifests reference constants
- [ ] Test fixtures use constants
- [ ] Documentation updated

**Estimated Effort:** 0.5 days

---

### Stream 4: External Integrations (P2)

#### 4.1 Alerting Integration

**Problem:** PagerDuty and Slack alerting are stubs.

**Files Affected:**
- `services/tenant/worker/alerting.go:67` - PagerDuty stub
- `services/tenant/worker/alerting.go:70` - Slack stub

**Acceptance Criteria:**
- [ ] PagerDuty API integration (configurable)
- [ ] Slack webhook integration (configurable)
- [ ] Alert severity mapping
- [ ] Rate limiting to prevent alert storms
- [ ] Configuration via environment variables
- [ ] Mock implementations for testing

**Estimated Effort:** 2-3 days

---

#### 4.2 KYC/AML Provider Interface

**Problem:** External verification service integration is a stub.

**Files Affected:**
- `services/party/service/grpc_service.go:710`

**Proposed Solution:** Define interface for provider abstraction:

```go
type VerificationProvider interface {
    VerifyIdentity(ctx context.Context, party Party) (VerificationResult, error)
    CheckSanctions(ctx context.Context, party Party) (SanctionsResult, error)
}
```

**Acceptance Criteria:**
- [ ] Provider interface defined
- [ ] Mock implementation for development/testing
- [ ] Configuration for provider selection
- [ ] Async verification flow documented
- [ ] ADR documenting verification architecture

**Estimated Effort:** 3-4 days

---

### Stream 5: Bug Fixes (P3)

#### 5.1 Double-Wrapped Error

**Problem:** Error wrapping uses `%w: %w` which can cause issues with `errors.Is()`.

**File:** `services/gateway/config.go:84`

```go
// Before
return nil, fmt.Errorf("%w: %w", ErrInvalidBackendsJSON, err)

// After
return nil, fmt.Errorf("%w: %v", ErrInvalidBackendsJSON, err)
```

**Estimated Effort:** 0.5 hours

---

#### 5.2 Auth Context Extraction

**Problem:** `UpdatedBy` and `ControlledBy` fields hardcoded to "system".

**Files Affected:**
- `services/financial-accounting/service/financial_accounting_service.go:1204`
- `services/financial-accounting/service/financial_accounting_service.go:1465`

**Acceptance Criteria:**
- [ ] Extract user identity from auth context
- [ ] Fallback to "system" when no auth context
- [ ] Audit trail correctly attributes changes

**Estimated Effort:** 0.5 days

---

## Summary Table

| ID | Work Item | Priority | Effort | Dependencies |
|----|-----------|----------|--------|--------------|
| 1.1 | Withdrawal Persistence | P0 | 3-5d | None |
| 1.2 | Idempotency Gap Fix | P0 | 2-3d | None |
| 2.1 | Account Lifecycle Events | P1 | 2-3d | None |
| 2.2 | Utilization Metering Endpoint | P1 | 3-4d | None |
| 3.1 | PartyClient Consolidation | P2 | 1d | None |
| 3.2 | Timeout Constants | P2 | 1d | None |
| 3.3 | Port Centralization | P2 | 0.5d | None |
| 4.1 | Alerting Integration | P2 | 2-3d | None |
| 4.2 | KYC/AML Interface | P2 | 3-4d | ADR required |
| 5.1 | Double-Wrapped Error | P3 | 0.5h | None |
| 5.2 | Auth Context Extraction | P3 | 0.5d | None |

**Total Estimated Effort:** 19-26 days

---

## Success Metrics

1. **P0 Complete:** All critical production blockers resolved
2. **Test Coverage:** No regression in test coverage
3. **Build Clean:** Zero new linter warnings
4. **Documentation:** All new patterns documented

---

## Rollout Plan

**Phase 1 (Week 1-2):** P0 items (1.1, 1.2)
**Phase 2 (Week 3-4):** P1 items (2.1, 2.2)
**Phase 3 (Week 5-6):** P2 items (3.x, 4.x)
**Phase 4 (Ongoing):** P3 items as capacity allows

---

## Appendix: Files Reference

### Files with TODOs requiring action:
```
services/current-account/service/grpc_service.go (7 TODOs)
services/financial-accounting/service/financial_accounting_service.go (3 TODOs)
services/utilization-metering-consumer/cmd/main.go (2 TODOs)
services/utilization-metering-consumer/adapters/grpc/position_keeping_client.go (4 TODOs)
services/tenant/worker/alerting.go (2 TODOs)
services/tenant/worker/provisioning_worker.go (1 TODO)
services/party/service/grpc_service.go (1 TODO)
services/position-keeping/service/update.go (1 TODO)
services/position-keeping/cmd/main.go (2 TODOs)
services/gateway/proxy.go (1 TODO)
services/gateway/server.go (1 TODO)
```

### Duplicate code locations:
```
services/current-account/clients/party_client.go
services/tenant/clients/party_client.go
→ Consolidate to: shared/pkg/clients/party.go
```
