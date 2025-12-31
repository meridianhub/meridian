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

#### 1.3 LienRepository Tenant Isolation Bug (CRITICAL)

**Problem:** Several `LienRepository` methods do not accept `context.Context` and therefore cannot enforce tenant isolation. These methods query without setting the PostgreSQL `search_path`, potentially accessing data across tenant schemas.

**Root Cause:** Transactions in `lien_service.go` are started with `s.repo.DB().Transaction()` without first setting tenant scope. Methods without context cannot call `withTenantTransaction()`.

**Files Affected:**
- `services/current-account/adapters/persistence/lien_repository.go`
  - `Create(lien *domain.Lien)` - Line 47
  - `FindByIDForUpdate(id uuid.UUID)` - Line 78
  - `FindByAccountID(accountID uuid.UUID)` - Line 96
  - `FindActiveByAccountID(accountID uuid.UUID)` - Line 118
  - `Update(lien *domain.Lien)` - Line 166

- `services/current-account/service/lien_service.go`
  - Transaction without tenant scope - Lines 118, 339, 532

**Risk:** Cross-tenant data leakage in lien operations. A request from Tenant A could potentially read/write Tenant B's liens.

**Acceptance Criteria:**
- [ ] All `LienRepository` methods accept `context.Context` as first parameter
- [ ] All methods use `withTenantTransaction()` for tenant-scoped queries
- [ ] Transactions in `lien_service.go` use `db.WithGormTenantTransaction()`
- [ ] Integration tests verify tenant isolation (query from Tenant A returns no Tenant B data)
- [ ] Audit existing data for any cross-tenant contamination

**Estimated Effort:** 2-3 days

---

#### 1.4 Gateway Authentication Gap

**Problem:** The gateway service forwards HTTP requests to backend gRPC services without any authentication or authorization. The tenant middleware resolves tenant identity from subdomain, but there's no verification that the caller is authorized.

**Files Affected:**
- `services/gateway/proxy.go` - `ServeHTTP` method just forwards requests
- `services/gateway/server.go` - No auth middleware in chain

**Current Flow:**
```
Client → Gateway → Backend (no auth check)
```

**Required Flow:**
```
Client → Gateway (JWT/API key validation) → Backend (with verified identity)
```

**Acceptance Criteria:**
- [ ] JWT validation middleware in gateway
- [ ] API key authentication as alternative for service-to-service
- [ ] Reject unauthenticated requests with 401
- [ ] Pass verified identity to backends via headers/metadata
- [ ] Rate limiting per authenticated identity
- [ ] Integration tests for auth flows

**Estimated Effort:** 3-4 days

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

#### 3.1 Standardized Service Client Library

**Problem:** Inter-service gRPC clients are owned by consumers, not by the services they call. This creates duplication and inconsistent patterns.

**Current State (Anti-pattern):**
```
services/current-account/clients/
  ├── party_client.go           # Consumer owns Party client (wrong)
  ├── positionkeeping_client.go # Consumer owns PK client (wrong)
  └── financialaccounting_client.go

services/payment-order/clients/
  ├── current_account_client.go # Consumer owns CA client (wrong)
  └── financialaccounting_client.go  # Duplicate!

services/tenant/clients/
  └── party_client.go           # Another duplicate Party client!
```

**Idiomatic Pattern:** Each service exports its own client package. The service being called owns the client, not the consumer.

```
api/proto/meridian/<service>/v1/       # Generated gRPC stubs (low-level)
services/<service>/client/              # High-level client (service-owned)
```

**Proposed Structure:**
```
services/party/
  ├── client/                          # NEW: Party exports its client
  │   ├── client.go                    # PartyClient with config, resilience
  │   ├── client_test.go
  │   └── doc.go
  ├── service/                         # Existing server implementation
  └── ...

services/position-keeping/
  ├── client/                          # NEW: PositionKeeping exports its client
  │   ├── client.go
  │   └── client_test.go
  └── ...

services/financial-accounting/
  ├── client/                          # NEW: FinancialAccounting exports its client
  │   ├── client.go
  │   └── client_test.go
  └── ...

services/current-account/
  ├── client/                          # NEW: CurrentAccount exports its client
  │   ├── client.go
  │   └── client_test.go
  ├── clients/                         # DEPRECATED: Remove after migration
  └── ...

shared/pkg/clients/
  ├── circuitbreaker.go               # Keep: Shared resilience patterns
  ├── retry.go
  ├── resilient.go
  └── saga.go
```

**Standard Client Pattern:**

```go
// services/party/client/client.go
package client

import (
    partyv1 "github.com/meridianhub/meridian/api/proto/meridian/party/v1"
    "github.com/meridianhub/meridian/shared/pkg/clients"
)

// Config holds configuration for the Party service client.
type Config struct {
    ServiceName string        // Required: k8s service name (e.g., "party")
    Namespace   string        // Defaults to "default"
    Port        int           // Defaults to 50055 (Party's port)
    Timeout     time.Duration // Defaults to 30s
    Tracer      *observability.Tracer
    Resilience  *clients.ResilientClientConfig // Optional
}

// Client provides access to the Party service.
type Client struct {
    conn      *grpc.ClientConn
    party     partyv1.PartyServiceClient
    resilient *clients.ResilientClient
    timeout   time.Duration
}

// New creates a new Party service client.
// Returns the client and a cleanup function.
func New(cfg Config) (*Client, func(), error)

// ValidateParty checks if a party exists and is active.
func (c *Client) ValidateParty(ctx context.Context, partyID string) error

// GetParty retrieves full party details by ID.
func (c *Client) GetParty(ctx context.Context, partyID string) (*partyv1.Party, error)
```

**Consumer Usage:**
```go
// services/current-account/cmd/main.go
import (
    partyclient "github.com/meridianhub/meridian/services/party/client"
)

partyClient, cleanup, err := partyclient.New(partyclient.Config{
    ServiceName: "party",
    Namespace:   namespace,
})
defer cleanup()
```

**Migration Plan:**

| Step | Action |
|------|--------|
| 1 | Create `services/<service>/client/` for each service |
| 2 | Move client logic from consumers into service-owned packages |
| 3 | Update consumers to import `services/<service>/client` |
| 4 | Deprecate `services/*/clients/` directories |
| 5 | Remove deprecated directories after migration |

**Files to Create:**
| Service | New Client Package |
|---------|-------------------|
| Party | `services/party/client/` |
| PositionKeeping | `services/position-keeping/client/` |
| FinancialAccounting | `services/financial-accounting/client/` |
| CurrentAccount | `services/current-account/client/` |
| Tenant | `services/tenant/client/` (if needed externally) |

**Files to Remove (after migration):**
```
services/current-account/clients/party_client.go
services/current-account/clients/positionkeeping_client.go
services/current-account/clients/financialaccounting_client.go
services/payment-order/clients/current_account_client.go
services/payment-order/clients/financialaccounting_client.go
services/tenant/clients/party_client.go
```

**Acceptance Criteria:**
- [ ] Each service exports its own client in `services/<service>/client/`
- [ ] Consistent config pattern across all clients
- [ ] Built-in resilience (circuit breaker + retry) via composition with `shared/pkg/clients`
- [ ] DNS-based load balancing for all clients
- [ ] Trace context propagation standard
- [ ] Service-specific interfaces remain in consuming services (for mocking)
- [ ] Consumer `clients/` directories removed
- [ ] 100% test coverage for service client packages

**Estimated Effort:** 4-5 days

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
| 1.3 | **LienRepository Tenant Isolation Bug** | P0 | 2-3d | None |
| 1.4 | **Gateway Authentication** | P0 | 3-4d | None |
| 2.1 | Account Lifecycle Events | P1 | 2-3d | None |
| 2.2 | Utilization Metering Endpoint | P1 | 3-4d | None |
| 3.1 | Standardized Service Client Library | P2 | 4-5d | None |
| 3.2 | Timeout Constants | P2 | 1d | None |
| 3.3 | Port Centralization | P2 | 0.5d | None |
| 4.1 | Alerting Integration | P2 | 2-3d | None |
| 4.2 | KYC/AML Interface | P2 | 3-4d | ADR required |
| 5.1 | Double-Wrapped Error | P3 | 0.5h | None |
| 5.2 | Auth Context Extraction | P3 | 0.5d | None |

**Total Estimated Effort:** 27-38 days

---

## Success Metrics

1. **P0 Complete:** All critical production blockers resolved (including tenant isolation)
2. **Security:** Gateway authenticates all requests before forwarding
3. **Test Coverage:** No regression in test coverage
4. **Build Clean:** Zero new linter warnings
5. **Documentation:** All new patterns documented

---

## Rollout Plan

**Phase 1 (Week 1-2):** P0 Critical - Tenant isolation (1.3), Gateway auth (1.4)
**Phase 2 (Week 3-4):** P0 Remaining - Withdrawal (1.1), Idempotency (1.2)
**Phase 3 (Week 5-6):** P1 items (2.1, 2.2)
**Phase 4 (Week 7-8):** P2 items (3.x, 4.x)
**Phase 5 (Ongoing):** P3 items as capacity allows

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

### Service client ownership migration:
```
Current (consumer-owned):                    Target (service-owned):
─────────────────────────────────────────    ─────────────────────────────────────────
services/current-account/clients/            services/party/client/
  └── party_client.go            ────────►     └── client.go (Party owns its client)

services/tenant/clients/
  └── party_client.go            ────────►   (same - use services/party/client)

services/current-account/clients/            services/position-keeping/client/
  └── positionkeeping_client.go  ────────►     └── client.go (PK owns its client)

services/current-account/clients/            services/financial-accounting/client/
  └── financialaccounting_client.go ──────►    └── client.go (FA owns its client)
services/payment-order/clients/
  └── financialaccounting_client.go ──────►  (same - use services/financial-accounting/client)

services/payment-order/clients/              services/current-account/client/
  └── current_account_client.go  ────────►     └── client.go (CA owns its client)
```
