---
name: position-keeping-service
description: BIAN transaction log for immutable financial transaction history and position tracking
triggers:
  - Designing transaction capture and position keeping systems
  - Implementing financial audit trails and reconciliation workflows
  - Working with BIAN Transaction Log domain patterns
  - Building event-driven transaction systems
  - Understanding capacity limits and state machines for financial data
  - Handling optimistic concurrency control in transactional systems
instructions: |
  PositionKeeping maintains immutable transaction history with comprehensive audit trails.

  Key concepts:
  - FinancialPositionLog: Aggregate root with 10,000 entry limit
  - Transaction state machine: PENDING → RECONCILED → POSTED → REVERSED
  - Parent-child lineage for reversals and amendments
  - Kafka event publishing (fire-and-forget) for downstream consumers

  Capacity limits:
  - MaxTransactionEntries: 10,000 per log
  - MaxAuditEntries: 10,000 per log

  Immutability: POSTED logs cannot be modified (terminal state)

  Port: 50053 (gRPC)
---

# PositionKeeping Service

BIAN-compliant position keeping service for immutable financial transaction history and audit trails.

## Overview

| Attribute | Value |
|-----------|-------|
| **BIAN Domain** | Position Keeping |
| **Port** | 50053 (gRPC) |
| **Language** | Go |
| **Database** | PostgreSQL/CockroachDB |
| **Standalone** | Yes |

## gRPC Methods

| Method | HTTP | Purpose |
|--------|------|---------|
| `InitiateFinancialPositionLog` | `POST /v1/position-logs` | Create log with initial transaction |
| `RetrieveFinancialPositionLog` | `GET /v1/position-logs/{id}` | Get log details |
| `ListFinancialPositionLogs` | `GET /v1/position-logs` | List with filters |
| `UpdateFinancialPositionLog` | `PATCH /v1/position-logs/{id}` | State transitions |
| `BulkImportTransactions` | `POST /v1/position-logs/bulk` | Batch import |

## Domain Model

### FinancialPositionLog (Aggregate Root)

```text
FinancialPositionLog {
  LogID: UUID
  AccountID: string (max 34 chars)
  Version: int64
  CurrentStatus: TransactionStatus
  PreviousStatus: TransactionStatus
  ReconciliationStatus: ReconciliationStatus
  StatusReason: string
  FailureReason: string
  Entries: []TransactionLogEntry (max 10,000)
  AuditTrail: []AuditTrailEntry (max 10,000)
  Lineage: TransactionLineage
}
```

### TransactionLogEntry

```text
TransactionLogEntry {
  EntryID: UUID
  TransactionID: UUID
  AccountID: string
  Amount: Money
  Direction: DEBIT | CREDIT
  Timestamp: time.Time
  Description: string
  Reference: string
  Source: API | BATCH | MANUAL | SYSTEM | IMPORT | MIGRATION | CORRECTION
}
```

### TransactionLineage

```text
TransactionLineage {
  TransactionID: UUID
  ParentTransactionID: *UUID
  ChildTransactionIDs: []UUID
  RelatedTransactionIDs: []UUID
  TransactionType: string
}
```

## Transaction Status State Machine

```text
PENDING
    │
    ├──→ RECONCILED (matched with external system)
    │        │
    │        ├──→ POSTED (final, immutable)
    │        │        │
    │        │        └──→ REVERSED (special case)
    │        │
    │        └──→ AMENDED (modifications while reconciled)
    │
    ├──→ FAILED
    ├──→ REJECTED
    └──→ CANCELLED
```

### Reconciliation Status

| Status | Description |
|--------|-------------|
| `UNRECONCILED` | Not yet matched |
| `MATCHED` | Matched with external data |
| `MISMATCHED` | Discrepancy found |
| `RESOLVED` | Discrepancy resolved |

## Kafka Event Publishing

**Topics:**

| Topic | Event |
|-------|-------|
| `position-keeping.transaction-captured.v1` | New transaction |
| `position-keeping.transaction-amended.v1` | Post-capture modification |
| `position-keeping.transaction-reconciled.v1` | External matching |
| `position-keeping.transaction-posted.v1` | Final committed |
| `position-keeping.transaction-rejected.v1` | Rejected |
| `position-keeping.transaction-failed.v1` | Processing failed |
| `position-keeping.transaction-cancelled.v1` | Cancelled |
| `position-keeping.bulk-transaction-captured.v1` | Batch import |

**Publishing Pattern:**

- Fire-and-forget (doesn't block main operation)
- Partition key = LogID (ensures ordering per aggregate)
- Protobuf serialization

## Database Schema

**Schema**: `position_keeping`

### financial_position_logs Table

| Column | Type | Purpose |
|--------|------|---------|
| `log_id` | UUID | Primary key |
| `account_id` | VARCHAR(34) | Account reference |
| `version` | BIGINT | Optimistic locking |
| `current_status` | VARCHAR(20) | Transaction state |
| `reconciliation_status` | VARCHAR(20) | Matching state |
| `status_reason` | TEXT | Transition reason |

### transaction_log_entries Table

| Column | Type | Purpose |
|--------|------|---------|
| `entry_id` | UUID | Primary key |
| `financial_position_log_id` | UUID | Parent reference |
| `transaction_id` | UUID | Transaction identifier |
| `amount_cents` | BIGINT | Amount in minor units |
| `currency` | CHAR(3) | ISO 4217 |
| `direction` | VARCHAR(10) | debit or credit |
| `source` | VARCHAR(20) | Transaction source |

### audit_trail_entries Table

| Column | Type | Purpose |
|--------|------|---------|
| `audit_id` | UUID | Primary key |
| `financial_position_log_id` | UUID | Parent reference |
| `user_id` | VARCHAR(100) | Actor |
| `action` | VARCHAR(100) | Action taken |
| `ip_address` | VARCHAR(45) | Client IP |
| `details` | JSONB | Additional context |

## Capacity Limits

| Limit | Value | Error |
|-------|-------|-------|
| Max Transaction Entries | 10,000 | `ErrTooManyEntries` |
| Max Audit Entries | 10,000 | `ErrTooManyEntries` |

## Currency Support

7 currencies with proper decimal handling:

| Currency | Decimals |
|----------|----------|
| GBP, USD, EUR, CHF, CAD, AUD | 2 |
| JPY | 0 |

## Configuration

| Variable | Default | Purpose |
|----------|---------|---------|
| `GRPC_PORT` | 50053 | gRPC server port |
| `DATABASE_URL` | - | PostgreSQL connection string |
| `KAFKA_BROKERS` | kafka:9092 | Kafka broker addresses |
| `REDIS_ENABLED` | false | Enable idempotency cache |
| `REDIS_ADDRESS` | redis:6379 | Redis address |

## Key Patterns

### Idempotency

- Redis-backed distributed locking
- Idempotency keys for exactly-once semantics
- 5-minute TTL for pending operations

### Optimistic Locking

Version field required for all updates. Returns conflict error on mismatch.

### Immutability

POSTED logs cannot be modified. Returns `ErrAlreadyPosted`.

## References

- [BIAN Position Keeping Specification](https://github.com/bian-official/public/blob/main/release13.0.0/semantic-apis/oas3/yamls/PositionKeeping.yaml)
- [Service Architecture](../README.md)
- [Proto Definitions](../../api/proto/meridian/position_keeping/v1/)
