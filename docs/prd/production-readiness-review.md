# Production Readiness Review & Remediation

**Status**: Draft
**Created**: 2026-01-28
**Author**: Production Readiness Audit
**Related PRDs**: [Starlark Service Bindings](./starlark-service-bindings.md)

---

## Executive Summary

Comprehensive codebase audit identified **significant end-to-end test coverage gaps** (only 25% of services have e2e
tests), **4 stubbed implementations in production code**, and **multiple production readiness issues** across transaction
boundaries, error handling, and validation logic.

### Critical Findings

1. **E2E Test Coverage: 25%** - Only 3 out of 12 services have end-to-end tests
2. **Financial Core Untested** - `position-keeping`, `current-account`, `financial-accounting`, `payment-order` lack e2e
   tests
3. **4 Stubbed Implementations** - Production code contains hardcoded/placeholder responses
4. **Transaction Boundary Issues** - Multiple gRPC calls without atomicity can leave inconsistent state
5. **Silent Failures** - NoOp fallbacks that silently discard events and allow duplicate processing

---

## 1. End-to-End Test Coverage Analysis

### Current State: 3/12 Services (25%)

#### ✅ Services WITH E2E Tests

| Service | Test File | Lines | Coverage Quality |
|---------|-----------|-------|------------------|
| **reference-data** | `services/reference-data/e2e/e2e_test.go` | 1,547 | Comprehensive - lifecycle, multi-tenant isolation, cache invalidation, performance baselines |
| **internal-bank-account** | `services/internal-bank-account/e2e/e2e_test.go` | 1,852 | Comprehensive - complete lifecycle, multi-asset accounts, position-keeping integration, concurrent operations |
| **market-information** | `services/market-information/e2e/e2e_test.go` | 1,101 | Comprehensive - FX rates, energy tariffs, quality ladder supersession, bi-temporal audit |

#### ❌ Critical Services WITHOUT E2E Tests

##### 1. **position-keeping** - HIGHEST RISK ⚠️

**Why Critical**: Core ledger for all asset positions - bugs cascade to all other services.

**What's Missing**:

- Position aggregation workflows (sum by account + instrument + bucket)
- Soft deletion + aggregation interaction (`deleted_at` exclusion from sums)
- Append-only constraint enforcement (UPDATE should fail on immutable columns)
- High-frequency insert handling (1000+ positions/sec without deadlock)
- Bucket-based position tracking
- Cross-instrument isolation

**Current Coverage**: Integration tests only (`service/balance_integration_test.go`, `service/initiate_migration_integration_test.go`)

**Impact**: Position discrepancies, incorrect balances, data corruption

---

##### 2. **current-account** - HIGH RISK

**Why Critical**: Manages customer account balances.

**What's Missing**:

- Complete deposit workflow: Account → Position-Keeping → Financial-Accounting
- Withdrawal with lien: Reserve → Execute → Release
- Failed withdrawal compensation: Execute failure → Release lien
- Webhook delivery reliability (regulatory notifications)
- Balance check race conditions
- Account lifecycle with status transitions

**Current Coverage**: Integration tests with mocked dependencies (`grpc_service_integration_test.go`, `account_control_integration_test.go`)

**Impact**: Lost customer funds, incorrect balances, stuck liens

---

##### 3. **financial-accounting** - HIGH RISK

**Why Critical**: Double-entry bookkeeping for all transactions.

**What's Missing**:

- Complete posting workflows (debit/credit pairing)
- Trial balance generation (sum debits = sum credits)
- Reconciliation with position-keeping
- NoOp fallback behavior under Redis/Kafka failure
- Orphaned booking logs from partial failures

**Current Coverage**: Integration tests with testcontainers (`service/grpc_integration_test.go`)

**Impact**: Incorrect financial statements, audit failures, unbalanced ledgers

---

##### 4. **payment-order** - HIGH RISK

**Why Critical**: Orchestrates fund transfers across services.

**What's Missing**:

- Full saga pattern with REAL services (reserve → execute → complete)
- Compensation scenarios (failed payment → reverse lien)
- Concurrent lien execution (race condition testing)
- Bucket ID evaluation failure handling
- Gateway timeout and retry behavior

**Current Coverage**: Integration tests with mocked current-account and financial-accounting (`service/integration_test.go`)

**Impact**: Payments stuck in PENDING, funds reserved but never released, silent gateway failures

---

#### ⚠️ Services WITHOUT Integration OR E2E Tests

##### 5. **gateway** - MEDIUM RISK

**What's Missing**: Authentication, routing, rate limiting, request validation

**Current Coverage**: Unit tests only (10 test files)

**Impact**: Security vulnerabilities, API downtime, unauthorized access

---

##### 6. **audit-worker** - LOW RISK

**What's Missing**: Kafka consumption, cross-service audit trail consistency

**Current Coverage**: Minimal (1 test file)

**Impact**: Incomplete audit logs, compliance issues

---

##### 7. **utilization-metering-consumer** - LOW RISK

**What's Missing**: Event consumption, metering data aggregation, integration with position-keeping

**Current Coverage**: Unit tests only (8 test files)

**Impact**: Incorrect billing, revenue loss

---

### Cross-Service E2E Tests

✅ **Multi-Service Audit** - `tests/audit-e2e/audit_writer_e2e_test.go` (691 lines)

- Tests audit writes across current-account, financial-accounting, position-keeping
- Verifies tenant isolation and bounded context enforcement

✅ **Multi-Tenant Isolation** - `tests/multi_tenant/e2e_test.go`

- Tests cross-service tenant isolation

---

## 2. Stubbed/Mocked Implementations in Production Code

### 2.1 Party Service - Hardcoded KYC/AML Verification

**File**: `services/party/service/grpc_service.go:710-712`

**Issue**: The `ExchangeDemographics` method always returns "VERIFIED" without actual KYC/AML verification.

```go
// TODO: Integrate with external verification service (KYC/AML provider)
// Currently returns hardcoded "VERIFIED" status for development
verificationStatus := "VERIFIED"
```

**Impact**:

- No actual compliance checks
- Cannot detect fraudulent identities
- Regulatory violation risk

**Severity**: **HIGH**

**Remediation**:

- Integrate with external KYC/AML provider (e.g., Jumio, Onfido, Trulioo)
- OR add feature flag to prevent production deployment with stub
- Add verification status audit trail

---

### 2.2 Market Information Service - Database Not Wired

**File**: `services/market-information/cmd/main.go:75-261`

**Issue**: Service scaffolded but not fully functional - no database persistence, service not registered, ECB worker disabled.

```go
// TODO: Wire database into repository once persistence layer is implemented (Task 3)
// Currently scaffolding without database to avoid holding idle connections.
// Uncomment when ready:
// dbConfig := bootstrap.DefaultDatabaseConfig()
// db, err := bootstrap.NewDatabase(ctx, dbConfig)

// TODO: Register Market Information service when proto definitions are available
// pb.RegisterMarketInformationServiceServer(grpcServer, marketInformationService)

// TODO: Enable ECB worker once marketInformationServer is available
```

**Impact**:

- Service cannot persist market data
- No FX rates or energy tariffs stored
- ECB data ingestion worker not running

**Severity**: **HIGH**

**Remediation**:

- Complete database integration (lines 75-84)
- Register gRPC service (lines 98-99)
- Enable ECB worker (lines 205-208)
- Enable database cleanup (lines 258-261)
- Verify e2e tests pass after integration

---

### 2.3 Financial Accounting Service - NoOp Fallbacks

**File**: `services/financial-accounting/cmd/main.go:560-606`

**Issue**: When Redis or Kafka is unavailable, service falls back to no-op implementations that **silently discard**
idempotency checks and events.

```go
// noopIdempotencyService provides a no-operation implementation of idempotency.Service.
// This allows the service to start without Redis for development and testing.
// In production, use idempotency.NewRedisService for proper distributed idempotency.
type noopIdempotencyService struct{}

func (n *noopIdempotencyService) IsProcessed(ctx context.Context, idempotencyKey string) (bool, error) {
    return false, nil // ALWAYS returns false - allows duplicate processing
}

// noopEventPublisher provides a no-operation implementation of service.EventPublisher.
// This allows the service to start without Kafka for development and testing.
// In production, use messaging.NewKafkaEventPublisher for proper event publishing.
type noopEventPublisher struct{}

func (n *noopEventPublisher) Publish(ctx context.Context, event *pb.FinancialAccountingEvent) error {
    return nil // SILENTLY discards events
}
```

**Impact**:

- **Duplicate processing** when Redis unavailable (idempotency checks bypassed)
- **Lost events** when Kafka unavailable (events silently discarded)
- No alerting or visibility when fallbacks are active

**Severity**: **HIGH**

**Remediation Options**:

**Option 1 (Recommended)**: Fail-fast in production

```go
if redisClient == nil {
    if os.Getenv("ENVIRONMENT") == "production" {
        logger.Error("CRITICAL: Redis unavailable in production - failing fast")
        return fmt.Errorf("Redis required in production environment")
    }
    logger.Warn("Using NoOp idempotency service - development/testing only")
    idempotencyService = &noopIdempotencyService{}
}
```

**Option 2**: Add observability

- Emit metrics when NoOp services are used
- Alert on fallback activation in production
- Add health check degradation

---

### 2.4 Horizon Demo Utility - Placeholder Implementation

**File**: `utilities/horizon-demo/main.go:288-301, 420-423`

**Issue**: Demo utility returns fake data instead of executing actual operations.

```go
// Placeholder demo results - the horizon-demo is a proof-of-concept
result := &DemoResult{
    Steps: []StepResult{
        {Step: 1, Name: "Create Test Account", Status: StatusOK, Details: "HORIZON-TEST-placeholder"},
        {Step: 2, Name: fmt.Sprintf("Deposit £%.0f", depositGBP), Status: StatusOK,
         Details: fmt.Sprintf("Balance: £%.0f", depositGBP)},
        // ... more hardcoded results
    },
}

// Cleanup function is a no-op
func executeCleanup(logger *slog.Logger, accountID string) error {
    logger.Debug("cleanup would delete account", "account_id", accountID)
    return nil // Does nothing
}
```

**Impact**:

- Misleading demo output (appears to work but does nothing)
- Cannot be used for actual testing or validation

**Severity**: **MEDIUM**

**Remediation**:

- Either complete the implementation
- OR clearly mark as non-functional demo tool in documentation
- OR remove entirely if no longer needed

---

## 3. Production Readiness Issues

### 3.1 Transaction Boundary Problems

#### Issue 1: Payment Order - Orphaned Booking Logs

**File**: `services/payment-order/service/payment_orchestrator.go:576-673`

**Problem**: Multiple gRPC calls without transaction boundary - partial failures leave inconsistent state.

**Workflow**:

1. `InitiateFinancialBookingLog` (creates PENDING)
2. `CaptureLedgerPosting` (DEBIT)
3. `CaptureLedgerPosting` (CREDIT) - **CAN FAIL HERE**
4. `UpdateFinancialBookingLog` (marks POSTED)

**Evidence**: Extensive RECONCILIATION_REQUIRED logging at lines 702-743

**Impact**: Manual reconciliation needed for partial failures, unbalanced ledgers

**Remediation**:

- Implement distributed transaction coordination (Saga pattern with compensation)
- OR use transactional outbox pattern for atomic state + event persistence
- Add automated reconciliation worker to detect and fix orphaned logs

---

#### Issue 2: Current Account - Withdrawal Status Update Failure

**File**: `services/current-account/service/grpc_service.go:896-913`

**Problem**: Withdrawal completion can succeed while status update fails (eventual consistency issue).

```go
// TODO(tm:tech-debt-cleanup.33): This represents an eventual consistency tradeoff
if err := pendingWithdrawal.Complete(); err != nil {
    // Log warning but don't fail - the withdrawal has already executed
    s.logger.Warn("failed to transition withdrawal to completed status")
```

**Impact**: Withdrawal record stays in PENDING even though funds transferred

**Remediation**: Implement Outbox Pattern

- Write withdrawal state + status update event atomically in same transaction
- Background worker publishes events to ensure eventual consistency
- See `shared/platform/events/outbox.go` for implementation

---

#### Issue 3: Payment Order - Lien Execution Race Condition

**File**: `services/payment-order/service/payment_orchestrator.go:1069-1179`

**Problem**: Async lien execution with retry but no distributed lock - concurrent updates can race.

```go
func (o *PaymentOrchestrator) updateLienExecutionStatus(...) {
    // Use a fresh context to ensure status update isn't cancelled
    //nolint:contextcheck // Intentionally using fresh context
    updateCtx := context.Background()
```

**Impact**: Payment orders stuck in PENDING lien_execution_status, retries exhausted on contention

**Remediation**:

- Implement distributed locking (Redis SETNX or similar)
- OR use database-level advisory locks
- Add metrics for lock contention

---

### 3.2 Missing Validation Logic

#### Issue 1: Payment Order - Bucket ID Evaluation Silent Failures

**File**: `services/payment-order/service/payment_orchestrator.go:301-355`

**Problem**: All errors are silently swallowed, returns empty string.

```go
func (o *PaymentOrchestrator) evaluateBucketID(...) (string, error) {
    // Skip if reference data client not configured
    if o.referenceDataClient == nil {
        return "", nil  // Silently degrades
    }
    // ...more silent failures
}
```

**Impact**: Bucket evaluation failures invisible, payments succeed with wrong bucket, silent data corruption in position-keeping

**Remediation**:

- Add observability: emit metric when bucket evaluation fails
- Add alerting for repeated failures
- Consider fail-fast option for critical bucket-required instruments

---

#### Issue 2: Current Account - Balance Check Race Condition

**File**: `services/current-account/service/grpc_service.go:847-866`

**Problem**: `PrepareForDebit` checks available balance but relies on Position Keeping - race condition between balance
check and actual debit.

```go
account, err = account.PrepareForDebit(amount)
if err != nil {
    if errors.Is(err, domain.ErrInsufficientFunds) {
        // Error returned after the check
```

**Impact**: Could allow overdraft if balance changes between check and execution

**Remediation**:

- Use pessimistic locking (SELECT FOR UPDATE)
- OR use optimistic locking with version checking
- OR rely on Position Keeping constraints for final enforcement

---

### 3.3 Error Handling Gaps

#### Issue 1: Current Account - Fire-and-Forget Webhook Delivery

**File**: `services/current-account/service/grpc_service.go:1905-1920`

**Problem**: Webhook delivery has no retry, no persistence - failures are invisible.

```go
//nolint:contextcheck // Intentionally using background context
go s.sendFreezeWebhook(tenantID.String(), accountID, req.Reason, timestamp)
```

**Impact**: Regulatory notifications may be lost (account freeze, suspension events)

**Remediation**:

- Implement transactional outbox pattern for webhook delivery
- Store webhook events in database
- Background worker retries failed deliveries
- Add webhook delivery status tracking

---

## 4. Recommended Implementation Plan

### Phase 1: Critical Financial Services E2E Tests (Weeks 1-4)

**Priority Order**: position-keeping → current-account → financial-accounting → payment-order

#### Week 1: Position-Keeping E2E Tests

**Test Package**: `services/position-keeping/e2e/`

**Tests to Implement**:

- `TestPositionAggregation_E2E` - Sum positions by account + instrument + bucket
- `TestSoftDeletion_E2E` - Verify `deleted_at` exclusion from aggregations
- `TestAppendOnly_E2E` - Verify UPDATE fails on immutable columns
- `TestHighFrequencyInserts_E2E` - 1000+ positions/sec without deadlock
- `TestBucketIsolation_E2E` - Cross-bucket position isolation
- `TestMultiTenantIsolation_E2E` - Tenant A cannot see tenant B positions

**Pattern**: Follow `internal-bank-account/e2e/e2e_test.go` structure

**Success Criteria**: All critical position-keeping paths have e2e coverage

---

#### Week 2: Current-Account E2E Tests

**Test Package**: `services/current-account/e2e/`

**Tests to Implement**:

- `TestDeposit_E2E_HappyPath` - Account → Position-Keeping → Financial-Accounting
- `TestWithdrawal_E2E_WithLien` - Reserve → Execute → Complete
- `TestWithdrawal_E2E_Compensation` - Execute failure → Release lien
- `TestWebhookDelivery_E2E` - Freeze notification reliability
- `TestBalanceCheck_E2E` - Race condition handling
- `TestAccountLifecycle_E2E` - Status transitions (Initiated → Active → Suspended → Closed)

**Integration Point**: Test with REAL position-keeping and financial-accounting services (not mocks)

**Success Criteria**: All deposit/withdrawal/lien workflows have e2e coverage

---

#### Week 3: Financial-Accounting E2E Tests

**Test Package**: `services/financial-accounting/e2e/`

**Tests to Implement**:

- `TestDoubleEntryPosting_E2E` - Debit + credit pairing, sum = 0
- `TestTrialBalance_E2E` - Sum all debits = sum all credits
- `TestReconciliation_E2E` - Compare with position-keeping balances
- `TestOrphanedBookingLog_E2E` - Detect and recover from partial failures
- `TestBookingLogLifecycle_E2E` - PENDING → POSTED → CANCELLED

**Integration Point**: Test with REAL position-keeping service

**Success Criteria**: Double-entry bookkeeping guarantees verified end-to-end

---

#### Week 4: Payment-Order E2E Tests

**Test Package**: `services/payment-order/e2e/`

**Tests to Implement**:

- `TestPaymentSaga_E2E_HappyPath` - Reserve → Gateway → Execute → Complete
- `TestPaymentSaga_E2E_GatewayFailure` - Gateway rejection → Reverse lien
- `TestPaymentSaga_E2E_LienFailure` - Lien creation failure → No gateway call
- `TestConcurrentLienExecution_E2E` - Race condition handling
- `TestBucketEvaluation_E2E` - Bucket-aware solvency validation
- `TestPaymentTimeout_E2E` - Timeout handling and compensation

**Integration Point**: Test with REAL current-account, financial-accounting, gateway (use mock gateway)

**Success Criteria**: All saga compensation scenarios verified with real services

---

### Phase 2: Fix Stubbed Implementations (Weeks 5-8)

#### Week 5: Party Service - KYC/AML Integration

**Options**:

**Option 1 (Recommended for MVP)**: Feature flag guard

```go
if os.Getenv("KYC_STUB_ENABLED") != "true" {
    return status.Error(codes.Unimplemented, "KYC verification not implemented")
}
```

**Option 2 (Production-ready)**: Integrate with external provider

- Select provider: Jumio, Onfido, Trulioo, IDology
- Implement adapter pattern for provider
- Add verification status audit trail
- Add e2e tests with provider sandbox

**Deliverable**: Either feature flag guard OR provider integration

---

#### Week 6: Market Information Service - Database Integration

**Tasks**:

1. Uncomment database initialization code (lines 75-84)
2. Register Market Information gRPC service (lines 98-99)
3. Enable ECB worker (lines 205-208)
4. Enable database cleanup (lines 258-261)
5. Run existing e2e tests to verify integration
6. Add database migration for market data schema (if not exists)

**Verification**: `services/market-information/e2e/e2e_test.go` passes with real database

---

#### Week 7: Financial Accounting - Remove NoOp Fallbacks

**Approach**: Fail-fast in production

**Implementation**:

```go
func initializeIdempotencyService(...) (idempotency.Service, error) {
    redisClient, err := redis.NewClient(...)
    if err != nil {
        if isProduction() {
            return nil, fmt.Errorf("CRITICAL: Redis unavailable in production: %w", err)
        }
        logger.Warn("Using NoOp idempotency service - development only")
        return &noopIdempotencyService{}, nil
    }
    return idempotency.NewRedisService(redisClient), nil
}

func initializeEventPublisher(...) (service.EventPublisher, error) {
    kafkaProducer, err := messaging.NewKafkaProducer(...)
    if err != nil {
        if isProduction() {
            return nil, fmt.Errorf("CRITICAL: Kafka unavailable in production: %w", err)
        }
        logger.Warn("Using NoOp event publisher - development only")
        return &noopEventPublisher{}, nil
    }
    return messaging.NewKafkaEventPublisher(kafkaProducer), nil
}
```

**Deliverable**: Service fails to start in production without Redis/Kafka

---

#### Week 8: Horizon Demo - Document or Complete

**Options**:

**Option 1**: Complete implementation

- Implement actual account creation, deposit, exchange, withdrawal
- Implement cleanup function

**Option 2 (Recommended)**: Document limitations

- Add README explaining this is a non-functional placeholder
- Add warning in CLI output: "This is a demo - no actual operations are performed"
- Consider renaming to `horizon-demo-placeholder`

**Option 3**: Remove entirely if not needed

---

### Phase 3: Fix Production Readiness Issues (Weeks 9-11)

#### Week 9: Implement Outbox Pattern

**Services to Update**:

- `current-account` - Withdrawal status updates
- `current-account` - Webhook delivery
- `payment-order` - Lien execution status updates

**Implementation**:

1. Add `outbox_events` table to each service database
2. Write state changes + events atomically in same transaction
3. Background worker polls outbox and publishes events
4. Mark events as processed after successful publish
5. Add retry logic with exponential backoff

**Reference**: `shared/platform/events/outbox.go`

---

#### Week 10: Add Distributed Locking

**Service**: `payment-order` - Lien execution

**Implementation**:

```go
func (o *PaymentOrchestrator) updateLienExecutionStatus(...) error {
    lockKey := fmt.Sprintf("lien:execution:%s", paymentOrderID)
    lock, err := o.lockService.Acquire(ctx, lockKey, 30*time.Second)
    if err != nil {
        return fmt.Errorf("failed to acquire lock: %w", err)
    }
    defer lock.Release(ctx)

    // Update lien execution status with lock held
    // ...
}
```

**Options**:

- Redis-based: `github.com/bsm/redislock`
- Database-based: PostgreSQL advisory locks
- Distributed: etcd or Consul

---

#### Week 11: Add Observability for Silent Failures

**Services to Update**:

- `payment-order` - Bucket ID evaluation failures
- `financial-accounting` - NoOp fallback usage (if not removed)

**Implementation**:

```go
func (o *PaymentOrchestrator) evaluateBucketID(...) (string, error) {
    // ...existing logic...
    if err != nil {
        metrics.BucketEvaluationFailure.Inc()
        o.logger.Warn("bucket evaluation failed - using default bucket",
            "payment_order_id", paymentOrderID,
            "instrument_code", instrumentCode,
            "error", err)
        // Alert if failure rate > threshold
        return "", nil
    }
    // ...
}
```

**Metrics to Add**:

- `bucket_evaluation_failures_total` (counter)
- `noop_idempotency_service_used` (gauge, 0/1)
- `noop_event_publisher_used` (gauge, 0/1)
- `webhook_delivery_failures_total` (counter)

---

## 5. Test Infrastructure Patterns

### Pattern 1: Comprehensive Lifecycle Testing

Based on `internal-bank-account/e2e/e2e_test.go`:

```go
func TestAccountLifecycle_E2E(t *testing.T) {
    // Setup testcontainers
    db, cleanup := testdb.SetupCockroachDB(t, nil)
    defer cleanup()

    // Test full lifecycle
    // Create → Update → Activate → Suspend → Reactivate → Close → Verify

    // 1. Create account
    account := createTestAccount(t, ctx, service)
    assert.Equal(t, StatusInitiated, account.Status)

    // 2. Update details
    updateAccount(t, ctx, service, account.ID)

    // 3. Activate
    activateAccount(t, ctx, service, account.ID)
    assert.Equal(t, StatusActive, account.Status)

    // ... continue through all states
}
```

---

### Pattern 2: Multi-Tenant Isolation

Based on `reference-data/e2e/e2e_test.go`:

```go
func TestMultiTenantIsolation_E2E(t *testing.T) {
    // Create data in tenant A
    tenantA := "tenant-a"
    accountA := createAccount(t, ctx, service, tenantA, "account-a")

    // Create data in tenant B
    tenantB := "tenant-b"
    accountB := createAccount(t, ctx, service, tenantB, "account-b")

    // Verify tenant A cannot see tenant B's data
    accounts := listAccounts(t, ctx, service, tenantA)
    assert.Len(t, accounts, 1)
    assert.Equal(t, accountA.ID, accounts[0].ID)

    // Verify tenant B cannot see tenant A's data
    accounts = listAccounts(t, ctx, service, tenantB)
    assert.Len(t, accounts, 1)
    assert.Equal(t, accountB.ID, accounts[0].ID)
}
```

---

### Pattern 3: Cross-Service Integration

Based on `tests/audit-e2e/audit_writer_e2e_test.go`:

```go
func TestCrossServiceIntegration_E2E(t *testing.T) {
    // Setup: Start multiple services
    currentAccountService := startCurrentAccountService(t, db)
    financialAccountingService := startFinancialAccountingService(t, db)
    positionKeepingService := startPositionKeepingService(t, db)

    // Perform operation in Service A
    transactionID := uuid.New().String()
    deposit := executeDeposit(t, ctx, currentAccountService, transactionID)

    // Verify audit log in Service A database
    auditLogs := queryAuditLogs(t, currentAccountDB, transactionID)
    assert.NotEmpty(t, auditLogs)

    // Verify position log in Service B database
    positionLog := queryPositionLog(t, positionKeepingDB, transactionID)
    assert.NotNil(t, positionLog)

    // Verify financial booking log in Service C database
    bookingLog := queryBookingLog(t, financialAccountingDB, transactionID)
    assert.NotNil(t, bookingLog)

    // Verify transaction IDs link across services
    assert.Equal(t, transactionID, positionLog.TransactionID)
    assert.Equal(t, transactionID, bookingLog.TransactionID)
}
```

---

### Pattern 4: Performance Baselines

All e2e tests should include latency assertions:

```go
func TestOperationPerformance_E2E(t *testing.T) {
    start := time.Now()

    // Execute operation
    result := executeOperation(t, ctx, service)

    duration := time.Since(start)

    // Assert performance baseline
    assert.True(t, duration < 50*time.Millisecond,
        "Expected operation < 50ms, got %v", duration)
}
```

**Recommended Baselines**:

- Account creation: < 100ms
- Balance query: < 50ms
- Position aggregation (1000 positions): < 100ms
- Deposit/withdrawal workflow: < 500ms

---

### Pattern 5: Async Operations with `await`

**CRITICAL**: Never use `time.Sleep` in tests - always use `await` package.

```go
import "github.com/meridianhub/meridian/shared/platform/await"

func TestAsyncOperation_E2E(t *testing.T) {
    // Trigger async operation
    triggerWithdrawal(t, ctx, service, accountID)

    // Wait for status update using await (not time.Sleep!)
    err := await.Until(func() bool {
        withdrawal := getWithdrawal(t, ctx, service, withdrawalID)
        return withdrawal.Status == StatusCompleted
    })
    require.NoError(t, err, "Withdrawal should reach COMPLETED status")

    // Verify final state
    withdrawal := getWithdrawal(t, ctx, service, withdrawalID)
    assert.Equal(t, StatusCompleted, withdrawal.Status)
}
```

**Why `await` is better than `time.Sleep`**:

- Polls condition until met or timeout (faster)
- Configurable timeout and poll interval
- Fails immediately when condition is met (no wasted time)
- Explicit about what we're waiting for

**Defaults**: 10s timeout, 100ms poll interval

---

## 6. Success Criteria

### Phase 1 Complete (Weeks 1-4)

- [ ] Position-keeping has e2e test suite with >80% coverage of critical paths
- [ ] Current-account has e2e test suite covering deposit/withdrawal/lien workflows
- [ ] Financial-accounting has e2e test suite covering double-entry posting
- [ ] Payment-order has e2e test suite covering full saga pattern with real services
- [ ] All new e2e tests follow established patterns (lifecycle, multi-tenant, cross-service, performance, await)

### Phase 2 Complete (Weeks 5-8)

- [ ] Party service has KYC/AML integration OR feature flag guard preventing stub usage in production
- [ ] Market Information service has database persistence enabled and working
- [ ] Financial Accounting service fails fast in production without Redis/Kafka (no NoOp fallbacks)
- [ ] Horizon Demo is either completed OR clearly documented as non-functional placeholder

### Phase 3 Complete (Weeks 9-11)

- [ ] Outbox Pattern implemented for withdrawal status updates and webhook delivery
- [ ] Distributed locking implemented for payment order lien execution
- [ ] Observability added for bucket ID evaluation failures
- [ ] Metrics dashboards created for silent failure detection
- [ ] Alerts configured for critical metric thresholds

### Overall Targets

- [ ] **E2E Test Coverage**: 25% → 75% (9/12 services)
- [ ] **Critical Services**: 0% → 100% e2e coverage (position-keeping, current-account, financial-accounting, payment-order)
- [ ] **Stubbed Implementations**: 4 stubs → 0 stubs in production code
- [ ] **Production Blockers**: All P0 issues resolved
- [ ] **CI Integration**: All new e2e tests running in CI pipeline

---

## 7. Risk Assessment

| Service/Component | E2E Coverage | Production Readiness | Overall Risk |
|-------------------|--------------|---------------------|--------------|
| **position-keeping** | ❌ None | ⚠️ Append-only constraints untested | **CRITICAL** |
| **current-account** | ❌ None | ⚠️ Eventual consistency issues, webhook failures | **HIGH** |
| **financial-accounting** | ❌ None | ⚠️ NoOp fallbacks, orphaned logs | **HIGH** |
| **payment-order** | ❌ None | ⚠️ Transaction boundaries, race conditions | **HIGH** |
| **party** | ❌ None | ⚠️ Stubbed KYC/AML verification | **HIGH** |
| **market-information** | ✅ Comprehensive | ⚠️ Database not wired | **MEDIUM** |
| **gateway** | ❌ None | ⚠️ Auth/routing untested | **MEDIUM** |
| **tenant** | ⚠️ Partial | ✅ Good | **MEDIUM** |
| **reference-data** | ✅ Comprehensive | ✅ Good | **LOW** |
| **internal-bank-account** | ✅ Comprehensive | ✅ Good | **LOW** |
| **audit-worker** | ❌ None | ⚠️ Kafka consumption untested | **LOW** |
| **utilization-metering-consumer** | ❌ None | ⚠️ Event processing untested | **LOW** |

---

## 8. Estimated Effort

| Phase | Duration | Resources | Hours |
|-------|----------|-----------|-------|
| **Phase 1**: Critical Financial Services E2E Tests | 4 weeks | 2 developers | 320 hours |
| **Phase 2**: Fix Stubbed Implementations | 4 weeks | 1 developer | 160 hours |
| **Phase 3**: Fix Production Readiness Issues | 3 weeks | 1 developer | 120 hours |
| **Total** | **11 weeks** | | **600 hours** |

**Assumptions**:

- Developers are familiar with testcontainers and e2e testing patterns
- Can run tests in parallel without blocking each other
- Reference existing e2e tests as templates to accelerate development

---

## 9. Open Questions

1. **KYC/AML Integration**: Which provider should we integrate with? (Jumio, Onfido, Trulioo, IDology)
2. **Distributed Locking**: Redis, etcd, or database advisory locks?
3. **Horizon Demo**: Complete implementation or remove entirely?
4. **Gateway E2E Tests**: Priority for Phase 1 or defer to Phase 4?
5. **Audit-Worker**: Critical for current release or defer?

---

## 10. References

- [Starlark Service Bindings PRD](./starlark-service-bindings.md) - Covers Starlark saga e2e testing
- [CLAUDE.md Testing Guidelines](../../CLAUDE.md#testing-guidelines) - Use `await` instead of `time.Sleep`
- [ADR 0028: Starlark Saga CEL Validation](../adr/0028-starlark-saga-cel-valuation.md)
- Existing E2E Tests:
  - `services/reference-data/e2e/e2e_test.go`
  - `services/internal-bank-account/e2e/e2e_test.go`
  - `services/market-information/e2e/e2e_test.go`
  - `tests/audit-e2e/audit_writer_e2e_test.go`

---

**Status**: Ready for Task Master breakdown
**Next Steps**: Create Task Master tag `production-readiness-review` and break down into implementable tasks

---

## Appendix A: Starlark Saga Architecture Gaps

**Source**: Earlier architectural audit (pre-starlark-service-bindings PRD)
**Status**: These findings are **ADDITIONAL** to the e2e testing gaps covered in [starlark-service-bindings.md](./starlark-service-bindings.md)

The typed service client infrastructure is **85% architecturally built but never connected to runtime**. These are
**implementation/wiring issues**, not testing gaps.

---

### A.1 CRITICAL: Schema-Runtime Mismatch

**File**: `shared/pkg/saga/schema/handlers.yaml` vs `.star` files

**Problem**: `handlers.yaml` defines **2-part handler names** but `.star` files use **3-part names**.

**Schema** (`handlers.yaml`):

```yaml
position_keeping:
  initiate_log:
    params: [...]
```

**.star files** (`services/current-account/sagas/withdrawal.star`):

```python
# Uses 3-part name: current_account.position_keeping.initiate_log
log_position_result = current_account.position_keeping.initiate_log(...)
```

**Impact**: `BuildServiceModules()` can never match handlers because naming convention doesn't align.

**Evidence**:

- `shared/pkg/saga/schema/handlers.yaml` - Defines 2-part names
- `shared/pkg/saga/handlers.go:281-303` - Handler registration expects 3-part names

**Remediation**:

1. **Option 1**: Update `handlers.yaml` to use 3-part names
2. **Option 2**: Update `.star` files to use 2-part names
3. **Option 3**: Add name mapping layer in `BuildServiceModules()`

**Recommended**: Option 1 - Update schema to match runtime

---

### A.2 CRITICAL: BuildServiceModules Never Called in Production

**File**: `shared/pkg/saga/starlark_runner.go:33-80`

**Problem**: `StarlarkSagaRunnerConfig.ServiceModules` field exists but is **always nil** in production. The entire typed
module system is architecturally wired but never activated.

**Evidence**:

```go
// shared/pkg/saga/starlark_runner.go:33-80
type StarlarkSagaRunnerConfig struct {
    Runtime        *Runtime
    Registry       *DomainHandlerRegistry
    Logger         *slog.Logger
    ServiceModules []starlark.StringDict // ← Always nil in production
}
```

**No production call sites**:

```bash
$ grep -r "BuildServiceModules" services/ --include="*.go" | grep -v "_test.go"
# (no results)
```

**Impact**: The typed service client infrastructure is dead code - never executed in production.

**Remediation**:

1. Call `BuildServiceModules()` during service initialization
2. Pass result to `StarlarkSagaRunnerConfig.ServiceModules`
3. Add e2e test to verify typed modules work

**Example**:

```go
// services/current-account/cmd/main.go
func main() {
    // ...
    schema := saga.LoadSchemaFromDirectory("shared/pkg/saga/schema")
    serviceModules, err := saga.BuildServiceModules(schema, handlerRegistry)
    if err != nil {
        log.Fatal(err)
    }

    sagaRunner, err := saga.NewStarlarkSagaRunner(saga.StarlarkSagaRunnerConfig{
        Runtime:        runtime,
        Registry:       handlerRegistry,
        ServiceModules: serviceModules, // ← Now wired
        Logger:         logger,
    })
}
```

---

### A.3 HIGH: Compensation Not Implemented for Typed Modules

**File**: `shared/pkg/saga/schema/service_modules.go:243-281`

**Problem**: `handlers.yaml` has `compensate:` fields but `wrapHandler()` ignores them entirely.

**Schema** (`handlers.yaml`):

```yaml
position_keeping:
  initiate_log:
    handler: position_keeping.initiate_log
    compensate: position_keeping.cancel_log  # ← Defined but ignored
```

**Implementation** (`service_modules.go:243-281`):

```go
func wrapHandler(...) starlark.Callable {
    return starlark.NewBuiltin(handlerName, func(...) {
        // Calls handler.Handler but never reads handler.Compensate
        result, err := handler.Handler(ctx, params)
        // ...
    })
}
```

**Also affected**: Old `invoke_handler` shim

- `shared/pkg/saga/handlers.go` - Parses `compensate_handler`/`compensate_params` kwargs but never uses them

**Impact**: Saga compensation/rollback cannot work with typed modules.

**Remediation**:

1. Store compensation handler in step metadata
2. Execute compensation handlers in reverse order (LIFO) when saga fails
3. See `starlark-service-bindings.md` for expected behavior

---

### A.4 MEDIUM: DSL Builtins Are Stubs

**File**: `shared/pkg/saga/builtins.go:104-171`

**Problem**: DSL builtins return placeholder/noop values.

**Examples**:

```go
// Line 109: saga() returns placeholder
starlark.NewBuiltin("saga", func(...) (starlark.Value, error) {
    return starlarkstruct.FromStringDict(...), nil // Placeholder
}),

// Line 139: cel_eval() returns expression as string (doesn't actually evaluate)
starlark.NewBuiltin("cel_eval", func(...) (starlark.Value, error) {
    return starlark.String(expression), nil // Doesn't evaluate!
}),

// Line 149: resolve_account() returns None
starlark.NewBuiltin("resolve_account", func(...) (starlark.Value, error) {
    return starlark.None, nil
}),

// Line 169: invoke_saga() returns None
starlark.NewBuiltin("invoke_saga", func(...) (starlark.Value, error) {
    return starlark.None, nil
}),
```

**Impact**: Advanced saga features (saga composition, CEL evaluation, account resolution) don't work.

**Remediation**:

1. Implement actual CEL evaluation using `github.com/google/cel-go`
2. Implement account resolution (query reference-data service)
3. Implement saga composition (nested saga invocation)
4. Add tests for each builtin

---

### A.5 MEDIUM: Linter Pre-Check Rule Dead

**File**: `shared/pkg/saga/linter.go:72-102`

**Problem**: `SetHandlerMetadata()` is never called, so `verifiedHandlers` map stays empty and
`LintIssueTypeMissingPreCheck` can never fire.

**Code**:

```go
// Line 72: verifiedHandlers stays empty
func (l *Linter) SetHandlerMetadata(handlers map[string]*HandlerMetadata) {
    l.verifiedHandlers = handlers // Never called
}

// Line 98: This lint check never triggers
if !handlerInfo.HasPreCheck {
    issue := &LintIssue{
        Type:    LintIssueTypeMissingPreCheck, // Dead code
        // ...
    }
}
```

**Only wired enforcement**: `SetEnforcementLevel()` for Decimal arithmetic

**Remediation**:

1. Call `SetHandlerMetadata()` during saga runner initialization
2. Pass handler metadata from schema
3. OR remove the dead pre-check lint rule

---

### A.6 LOW: starlarkToGo Duplication

**Problem**: Two implementations with different capabilities.

**Basic version** (`runtime.go`):

- Missing many types
- No error handling

**Comprehensive version** (`schema/service_modules.go:starlarkToGoValue()`):

- 8 type handlers
- Proper error handling
- Only runs inside `BuildServiceModules` (never called)

**Remediation**: Consolidate into single implementation in shared location

---

### A.7 LOW: Documentation Generator Orphaned

**File**: `tools/saga-doc-gen/`

**Problem**: Complete CLI tool with tests but not invoked by CI/Makefile. Would fail immediately due to schema mismatch
(#A.1).

**Remediation**:

1. Fix schema mismatch first (#A.1)
2. Add `make docs` target to run generator
3. Add to CI to regenerate docs on schema changes

---

### A.8 LOW: Schema LoadFromDirectory Unused

**Problem**: Only called by orphaned doc-gen tool (#A.7).

**Remediation**: Keep for future use after doc-gen is wired up

---

### A.9 LOW: Type Coercion Layer Unused

**File**: `shared/pkg/saga/schema/coercion.go`

**Problem**: `CoerceParams()` and `CoerceValue()` are comprehensive (8 type handlers) but only execute inside
`BuildServiceModules` wrappers, which never run (#A.2).

**Remediation**: Automatically fixed when #A.2 is resolved

---

## Summary: Starlark Architecture Issues

| # | Issue | Severity | Status | Blocks |
|---|-------|----------|--------|--------|
| **A.1** | Schema-Runtime name mismatch | **CRITICAL** | Not addressed | Typed modules unusable |
| **A.2** | BuildServiceModules never called | **CRITICAL** | Not addressed | Entire typed system is dead code |
| **A.3** | Compensation not implemented | **HIGH** | Not addressed | Saga rollback broken |
| **A.4** | DSL builtins are stubs | **MEDIUM** | Not addressed | Advanced features unavailable |
| **A.5** | Linter pre-check dead | **MEDIUM** | Not addressed | Lint rule never fires |
| **A.6** | starlarkToGo duplication | **LOW** | Not addressed | Code quality issue |
| **A.7** | Doc generator orphaned | **LOW** | Not addressed | Documentation generation unavailable |
| **A.8** | Schema loader unused | **LOW** | Not addressed | Depends on #A.7 |
| **A.9** | Type coercion unused | **LOW** | Not addressed | Depends on #A.2 |

**Critical Path**:

1. Fix schema mismatch (#A.1)
2. Wire `BuildServiceModules` into service init (#A.2)
3. Implement compensation in typed path (#A.3)

Without #A.1 and #A.2, the entire typed client infrastructure is architectural dead code that can never execute.

---

## Relationship to starlark-service-bindings.md PRD

**starlark-service-bindings.md** focuses on:

- **Testing** the typed service infrastructure (assumes it works)
- E2E tests for saga execution with real services
- Compensation testing

**This appendix** documents:

- **Architectural gaps** that prevent typed infrastructure from working
- Wiring issues (BuildServiceModules never called)
- Implementation stubs (DSL builtins)

**Both are needed**: Fix the architecture first (this appendix), then test it works (starlark-service-bindings.md).
