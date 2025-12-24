# Async Audit Implementation Analysis Findings

**Date**: 2024-12-24
**Task**: 19.1 - Analysis phase for final optimization and DRY review
**Scope**: 6 services (tenant, payment-order, party, position-keeping, financial-accounting, current-account)

## Executive Summary

The async audit implementation is largely well-structured, with a shared library in
`shared/platform/audit/` that most services correctly use. However, there is one significant
duplication issue (payment-order service), several schema inconsistencies across migrations,
and minor code patterns that could be consolidated.

---

## 1. Code Duplication Findings

### 1.1 Critical: Payment-Order Service Has Full Local Implementation

**Location**: `services/payment-order/adapters/persistence/payment_order_entity.go`

The payment-order service maintains a **complete local copy** of the audit infrastructure instead of using the shared library:

| Component | Shared Library | Payment-Order Local |
|-----------|----------------|---------------------|
| `ErrNilTransaction` | `shared/platform/audit/hooks.go:19` | `payment_order_entity.go:84` |
| `AuditOutbox` struct | `shared/platform/audit/hooks.go:38-54` | `payment_order_entity.go:94-109` |
| `recordAudit()` function | `shared/platform/audit/hooks.go:252-295` | `payment_order_entity.go:130-184` |
| `getUserIDFromContext()` | `shared/platform/audit/context.go:19` | `payment_order_entity.go:188-204` |

**Impact**:

- Maintenance burden (changes must be made in two places)
- Inconsistent behavior (local version uses `uuid.UUID` for RecordID, shared uses `string`)
- Missed Kafka integration (local version writes directly to outbox, shared version attempts Kafka first)

**Recommendation**: Migrate payment-order to use shared audit library. This requires:

1. Implementing `audit.Auditable` interface on `PaymentOrderEntity`
2. Replacing local `recordAudit()` calls with `audit.RecordCreate/Update/Delete`
3. Removing local `AuditOutbox`, `ErrNilTransaction`, `getUserIDFromContext`

### 1.2 Context Key Management Duplication

Two implementations of `getUserIDFromContext()` exist:

1. **shared/domain/models/base.go:105-122** - Uses `auth.UserIDContextKey` directly
2. **shared/platform/audit/context.go:19-30** - Uses `auth.GetUserIDFromContext()`

Both achieve the same goal but via different methods. The audit library's version is more abstracted.

**Recommendation**: Consolidate to use `audit.GetUserFromContext()` everywhere, or extract to a single shared utility.

### 1.3 Error Type Duplication

`ErrNilTransaction` is defined in multiple places:

| Location | Package |
|----------|---------|
| `shared/platform/audit/hooks.go:19` | audit |
| `shared/platform/events/outbox.go:35` | events |
| `services/payment-order/adapters/persistence/payment_order_entity.go:84` | persistence |

**Recommendation**: Consider a shared errors package for common transaction-related errors, or
document that each package intentionally defines its own error for proper `errors.Is()` matching.

---

## 2. Migration Schema Inconsistencies

### 2.1 Record ID Type Inconsistency

| Service | `record_id` Type in audit_log/audit_outbox |
|---------|-------------------------------------------|
| tenant | `VARCHAR(50)` (string IDs) |
| position-keeping | `UUID` |
| party | `UUID` |
| current-account | `UUID` |
| financial-accounting | `UUID` |
| payment-order | `UUID` |

**Note**: Tenant uses string IDs intentionally (tenant IDs are alphanumeric). The shared
`audit.AuditOutbox` struct uses `string` for `RecordID` to accommodate both.

### 2.2 JSON Storage Type Inconsistency

| Service | `old_values`/`new_values` Type |
|---------|-------------------------------|
| party | `TEXT` (contains JSON strings) |
| All others | `JSONB` |

**Location**: `services/party/migrations/20251217000001_audit_system.sql:22-24`

**Impact**:

- Party service stores JSON as text, requiring cast to JSONB for queries
- The `change_summary` view handles this with explicit `::jsonb` casts
- Shared `audit.AuditLog` struct uses `string` type which works with both

**Recommendation**: Consider a migration to standardize party to JSONB for consistency, though functionally equivalent.

### 2.3 Client IP Type Inconsistency

| Service | `client_ip` Type |
|---------|------------------|
| tenant | `INET` |
| position-keeping | `INET` |
| current-account | `INET` |
| financial-accounting | `INET` |
| party | `VARCHAR(45)` |
| payment-order | `VARCHAR(45)` |

**Impact**: Minor - both work for storing IP addresses. `INET` provides validation and supports IPv6.

### 2.4 Status Constraint Inconsistency (RESOLVED)

Current-account originally had a constraint missing `'completed'` status:

- Original: `CHECK (status IN ('pending', 'processing', 'failed'))`
- Fixed in: `20251217000001_fix_audit_status_constraint.sql`

All services now correctly include `'completed'` status.

---

## 3. Service Integration Analysis

### 3.1 Services Using Shared Audit Library Correctly

| Service | Entity | Uses Shared Library |
|---------|--------|---------------------|
| tenant | TenantEntity | Yes - `audit.RecordCreate/Update/Delete` |
| party | PartyEntity | Yes - `audit.RecordCreate/Update/Delete` |
| financial-accounting | FinancialBookingLogEntity | Yes - `audit.RecordCreate/Update/Delete` |
| financial-accounting | LedgerPostingEntity | Yes - `audit.RecordCreate/Update/Delete` |

### 3.2 Services with Custom/Mixed Implementation

| Service | Entity | Implementation |
|---------|--------|----------------|
| payment-order | PaymentOrderEntity | **Local** - full local copy |
| position-keeping | N/A | Uses `audit.GetUserFromContext()` but has custom audit logic in repository |
| current-account | N/A | Uses `audit.GetUserFromContext()` but has custom audit logic in repository |

### 3.3 Shared Domain Models with Audit Hooks

The following models in `shared/domain/models/` implement audit hooks using the shared library:

- `FinancialPositionLog`
- `TransactionLogEntry`
- `TransactionLineage`
- `AuditTrailEntry`
- `Customer`
- `Account`

These all correctly use `audit.RecordCreate/Update/Delete`.

---

## 4. Performance Bottleneck Analysis

### 4.1 Worker Metrics Review

`shared/platform/audit/metrics.go` provides comprehensive metrics:

**Outbox Processing Metrics:**

- `meridian_audit_worker_outbox_depth` (gauge by schema)
- `meridian_audit_worker_outbox_processed_total` (counter)
- `meridian_audit_worker_outbox_failed_total` (counter)
- `meridian_audit_worker_processing_duration_seconds` (histogram)
- `meridian_audit_worker_entry_age_seconds` (histogram)

**Kafka Metrics:**

- `meridian_audit_kafka_events_published_total` (counter)
- `meridian_audit_kafka_events_consumed_total` (counter)
- `meridian_audit_kafka_publish_duration_seconds` (histogram)
- `meridian_audit_kafka_consume_duration_seconds` (histogram)
- `meridian_audit_kafka_consumer_lag_messages` (gauge)
- `meridian_audit_kafka_fallback_used_total` (counter by reason)
- `meridian_audit_kafka_dlq_messages_total` (counter)

**Potential Bottleneck Indicators:**

1. `kafkaPublishDuration` histogram buckets extend to 5s - may need alerting on p99
2. `kafkaFallbackUsed` counter tracks fallback reasons - good for diagnosing issues
3. No explicit batch size metrics for consumer throughput

**Recommendation**: Add `batch_size` label to `processing_duration_seconds` histogram to correlate
processing time with batch sizes.

### 4.2 Worker Configuration

Default worker configuration in `shared/platform/audit/worker.go:28-33`:

- `defaultBatchSize = 100`
- `defaultPollInterval = 5 * time.Second`
- `defaultMaxRetries = 3`
- `defaultProcessingAge = 5 * time.Minute` (stuck entry threshold)

These defaults appear reasonable. No configuration exposure mechanism exists for tuning per-service.

**Recommendation**: Consider exposing these as configurable options in `NewAuditWorker()`.

---

## 5. Dead Code Candidates

### 5.1 Unused Type Aliases

Several services define type aliases for backward compatibility:

```go
// services/tenant/adapters/persistence/audit.go:99-101
const systemUser = audit.DefaultAuditUser
type AuditOutbox = audit.AuditOutbox

// services/party/adapters/persistence/party_entity.go:90
type PartyAuditOutbox = audit.AuditOutbox

// services/financial-accounting/adapters/persistence/booking_log_entity.go:61-65
type AuditOutbox = audit.AuditOutbox
type AuditLog = audit.AuditLog
```

**Recommendation**: Audit tests to determine if these aliases are still needed. If only for test
compatibility, consider updating tests to use the shared types directly.

### 5.2 Tenant-Specific Audit Types (Potentially Redundant)

`services/tenant/adapters/persistence/audit.go` defines:

- `TenantAuditOutbox` - nearly identical to `audit.AuditOutbox`
- `TenantAuditLog` - nearly identical to `audit.AuditLog`

The only difference is these use `string` for RecordID (tenant compatibility). However, the shared
`audit.AuditOutbox` **already uses `string`** for RecordID.

**Recommendation**: These types appear redundant and can likely be removed.

---

## 6. TODO/FIXME Comments (Non-Audit Related)

No TODO/FIXME comments exist in `shared/platform/audit/` directory.

Audit-adjacent TODOs found in services (for reference):

- `services/financial-accounting/service/financial_accounting_service.go:1176` - "TODO: Extract from auth context when available"

---

## 7. Prioritized Recommendations

### High Priority (DRY Violations)

1. **Migrate payment-order to shared audit library** (Est: 2-3 hours)
   - Implement `Auditable` interface on `PaymentOrderEntity`
   - Update hooks to use `audit.RecordCreate/Update/Delete`
   - Remove local `recordAudit()`, `AuditOutbox`, `ErrNilTransaction`
   - Update repository audit handling to use shared patterns

### Medium Priority (Consistency)

1. **Standardize migration schemas** (Est: 1-2 hours)
   - Create migrations to align `old_values`/`new_values` types (TEXT -> JSONB for party)
   - Consider aligning `client_ip` types (VARCHAR -> INET)

2. **Remove redundant tenant audit types** (Est: 30 min)
   - Remove `TenantAuditOutbox` and `TenantAuditLog` if truly unused
   - Update any tests using these types

### Low Priority (Nice to Have)

1. **Consolidate `getUserIDFromContext`** (Est: 30 min)
   - Update `shared/domain/models/base.go` to use `audit.GetUserFromContext()`

2. **Add worker configuration options** (Est: 1 hour)
   - Allow customizing batch size, poll interval, max retries per-service

3. **Add batch size metrics** (Est: 30 min)
   - Add label to processing duration histogram

---

## 8. Files Reference

### Shared Library

- `/shared/platform/audit/hooks.go` - Core audit recording functions
- `/shared/platform/audit/context.go` - User context extraction
- `/shared/platform/audit/worker.go` - Background processing
- `/shared/platform/audit/metrics.go` - Prometheus metrics
- `/shared/platform/audit/publisher.go` - Kafka publishing
- `/shared/platform/audit/consumer.go` - Kafka consumption

### Service Implementations

- `/services/tenant/adapters/persistence/audit.go`
- `/services/party/adapters/persistence/party_entity.go`
- `/services/payment-order/adapters/persistence/payment_order_entity.go` (needs migration)
- `/services/financial-accounting/adapters/persistence/booking_log_entity.go`
- `/services/financial-accounting/adapters/persistence/ledger_posting_entity.go`

### Migration Files

- `/services/tenant/migrations/20251216000001_initial.sql`
- `/services/position-keeping/migrations/20251217000001_audit_system.sql`
- `/services/party/migrations/20251217000001_audit_system.sql`
- `/services/current-account/migrations/20251216000002_audit_system.sql`
- `/services/current-account/migrations/20251217000001_fix_audit_status_constraint.sql`
- `/services/financial-accounting/migrations/20251217000001_audit_system.sql`
- `/services/payment-order/migrations/20251217000001_audit_system.sql`
