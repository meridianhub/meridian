---
name: financial-accounting-service
description: BIAN-compliant double-entry bookkeeping and general ledger service
triggers:
  - Financial accounting operations
  - Double-entry bookkeeping patterns
  - Ledger posting validation
  - Booking log lifecycle management
  - Money and currency handling
  - Idempotent gRPC operations
  - Kafka event consumption (deposits)
instructions: |
  FinancialAccounting implements double-entry bookkeeping where debits always equal credits.

  Key concepts:
  - BookingLog: BIAN control record aggregating related postings
  - LedgerPosting: Individual debit/credit line item
  - PostingDirection: DEBIT (asset/expense increase) or CREDIT (liability/income increase)

  Core operation (ProcessDeposit):
    Debit Entry:  Customer Account [asset increase]
    Credit Entry: Bank Cash Account [liability increase]
    Balance Check: Debits = Credits

  Port: 50052 (gRPC), 8082 (metrics)
---

# FinancialAccounting Service

BIAN-compliant financial accounting microservice for double-entry bookkeeping.

## Overview

| Attribute | Value |
|-----------|-------|
| **BIAN Domain** | Financial Standard Management |
| **Port** | 50052 (gRPC), 8082 (metrics) |
| **Language** | Go |
| **Database** | PostgreSQL/CockroachDB |
| **Standalone** | Yes |

## gRPC Methods

### Booking Log Operations

| Method | HTTP | Purpose |
|--------|------|---------|
| `InitiateFinancialBookingLog` | `POST /v1/booking-logs` | Create booking log |
| `UpdateFinancialBookingLog` | `PUT /v1/booking-logs/{id}` | Update status/rules |
| `RetrieveFinancialBookingLog` | `GET /v1/booking-logs/{id}` | Get with postings |
| `ListFinancialBookingLogs` | `GET /v1/booking-logs` | List with filters |

### Ledger Posting Operations

| Method | HTTP | Purpose |
|--------|------|---------|
| `CaptureLedgerPosting` | `POST /v1/booking-logs/{id}/postings` | Create posting |
| `UpdateLedgerPosting` | `PUT /v1/postings/{id}` | Update status |
| `RetrieveLedgerPosting` | `GET /v1/postings/{id}` | Get posting |
| `ListLedgerPostings` | `GET /v1/postings` | List with filters |

## Domain Model

### FinancialBookingLog (Aggregate Root)

```text
FinancialBookingLog {
  ID: UUID
  FinancialAccountType: string
  ProductServiceReference: string
  BusinessUnitReference: string
  ChartOfAccountsRules: string
  BaseCurrency: string (ISO 4217)
  Status: PENDING | POSTED | FAILED | CANCELLED | REVERSED
  postings: []LedgerPosting
}
```

### LedgerPosting

```text
LedgerPosting {
  ID: UUID
  FinancialBookingLogID: UUID
  Direction: DEBIT | CREDIT
  Amount: Money
  AccountID: string
  ValueDate: time.Time
  Status: PENDING | POSTED | FAILED
  CorrelationID: string
}
```

## Double-Entry Bookkeeping

Every financial transaction creates balanced postings:

```text
Deposit £500.00:
  DEBIT   Customer Account (asset increase)    = £500.00
  CREDIT  Bank Cash Account (liability increase) = £500.00
  ─────────────────────────────────────────────────────────
  Balance Check: Debits (£500.00) = Credits (£500.00) ✓
```

**Validation Rules:**

- Amounts must be positive (reversals use opposite direction)
- Debits must equal credits within a booking log
- Terminal states (POSTED, FAILED) prevent further transitions

## Kafka Integration

**Consumer**: DepositEvent

Consumes deposit events and creates balanced debit/credit postings automatically.

**Supported Currencies**: GBP, USD, EUR, JPY, CHF, CAD, AUD

## Database Schema

**Schema**: `financial_accounting`

### financial_booking_logs Table

| Column | Type | Purpose |
|--------|------|---------|
| `id` | UUID | Primary key |
| `financial_account_type` | VARCHAR(50) | Account type |
| `base_currency` | VARCHAR(3) | ISO 4217 code |
| `status` | VARCHAR(50) | Lifecycle state |
| `idempotency_key` | VARCHAR(255) | Exactly-once (unique) |
| `version` | BIGINT | Optimistic locking |

### ledger_postings Table

| Column | Type | Purpose |
|--------|------|---------|
| `id` | UUID | Primary key |
| `financial_booking_log_id` | UUID | Parent reference |
| `posting_direction` | VARCHAR(10) | DEBIT or CREDIT |
| `amount_cents` | BIGINT | Amount in cents |
| `currency` | VARCHAR(3) | ISO 4217 code |
| `account_id` | VARCHAR(255) | Target account |
| `value_date` | TIMESTAMPTZ | Effective date |
| `correlation_id` | VARCHAR(255) | Trace ID |

## Configuration

| Variable | Default | Purpose |
|----------|---------|---------|
| `GRPC_PORT` | 50052 | gRPC server port |
| `METRICS_PORT` | 8082 | Prometheus metrics |
| `DATABASE_URL` | - | PostgreSQL connection string |
| `BANK_CASH_ACCOUNT_ID` | - | Well-known bank account |

## Prometheus Metrics

| Metric | Type | Purpose |
|--------|------|---------|
| `financial_accounting_operation_duration_seconds` | Histogram | Operation latency |
| `financial_accounting_postings_total` | Counter | Postings by direction/currency |
| `financial_accounting_double_entry_validations_total` | Counter | Balance checks |
| `financial_accounting_errors_total` | Counter | Errors by category |

## References

- [BIAN Financial Accounting Specification](https://github.com/bian-official/public/blob/main/release13.0.0/semantic-apis/oas3/yamls/FinancialAccounting.yaml)
- [Service Architecture](../README.md)
- [Proto Definitions](../../api/proto/meridian/financial_accounting/v1/)
