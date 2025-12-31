---
name: current-account-service
description: BIAN current account facility with lien-based fund reservations for payment processing
triggers:
  - Creating or modifying current account operations
  - Implementing fund reservations (liens) for payments
  - Handling account lifecycle (freeze, close, activate)
  - Overdraft facility management
  - Integration with payment order saga patterns
  - Transaction logging and position keeping
  - Financial accounting and ledger posting
  - Account balance validation and reconciliation
instructions: |
  CurrentAccount orchestrates customer-facing banking operations with three upstream dependencies:
  - Party (validate customer exists and is active)
  - PositionKeeping (transaction audit trail)
  - FinancialAccounting (double-entry ledger posting)

  Key patterns:
  - Lien-based fund reservation for payment processing (ACTIVE → EXECUTED or TERMINATED)
  - Overdraft facility: AvailableBalance = Balance + (OverdraftEnabled ? OverdraftLimit : 0)
  - Account lifecycle: ACTIVE → FROZEN → CLOSED (state machine)
  - Optimistic locking via version field for concurrent updates

  Port: 50057 (gRPC)
---

# CurrentAccount Service

BIAN-compliant current account facility microservice with lien-based payment reservations.

## Overview

| Attribute | Value |
|-----------|-------|
| **BIAN Domain** | Current Account |
| **Port** | 50057 (gRPC) |
| **Language** | Go |
| **Database** | PostgreSQL/CockroachDB |
| **Standalone** | No (requires Party, PositionKeeping, FinancialAccounting) |

## gRPC Methods

### Account Operations

| Method | HTTP | Purpose |
|--------|------|---------|
| `InitiateCurrentAccount` | `POST /v1/current-accounts` | Create new account |
| `ExecuteDeposit` | `POST /v1/current-accounts/{id}/deposits` | Deposit funds |
| `RetrieveCurrentAccount` | `GET /v1/current-accounts/{id}` | Get account details |

### Lien Operations (Fund Reservation)

| Method | HTTP | Purpose |
|--------|------|---------|
| `InitiateLien` | `POST /v1/current-accounts/{id}/liens` | Reserve funds |
| `ExecuteLien` | `POST /v1/liens/{id}/execute` | Debit reserved funds |
| `TerminateLien` | `POST /v1/liens/{id}/terminate` | Release reservation |
| `RetrieveLien` | `GET /v1/liens/{id}` | Get lien details |

## Domain Model

```mermaid
classDiagram
    class CurrentAccount {
        +UUID ID
        +string AccountID
        +string AccountIdentification
        +UUID PartyID
        +Money Balance
        +Money AvailableBalance
        +AccountStatus Status
        +Money OverdraftLimit
        +bool OverdraftEnabled
        +int64 Version
    }

    class Lien {
        +UUID ID
        +UUID AccountID
        +Money Amount
        +LienStatus Status
        +string PaymentOrderReference
        +Time ExpiresAt
        +int Version
    }

    class AccountStatus {
        <<enumeration>>
        ACTIVE
        FROZEN
        CLOSED
    }

    class LienStatus {
        <<enumeration>>
        ACTIVE
        EXECUTED
        TERMINATED
    }

    CurrentAccount "1" --> "*" Lien : has
    CurrentAccount --> AccountStatus
    Lien --> LienStatus
```

**Field Notes:**

- `AccountID`: Business ID format `ACC-{uuid[:8]}`
- `AccountIdentification`: IBAN format
- `PaymentOrderReference`: Idempotency key for payment orders

## Lien Lifecycle

```text
ACTIVE (reservation)
   │
   ├──→ EXECUTED (funds debited, payment committed)
   │
   └──→ TERMINATED (reservation released, payment cancelled)
```

- **ACTIVE**: Funds reserved, reduces AvailableBalance
- **EXECUTED**: Terminal state, funds withdrawn from Balance
- **TERMINATED**: Terminal state, AvailableBalance restored

## Service Dependencies

| Service | Port | Purpose |
|---------|------|---------|
| Party | 50055 | Validate party exists and is active |
| PositionKeeping | 50053 | Transaction audit trail logging |
| FinancialAccounting | 50052 | Double-entry ledger posting |

All clients use circuit breaker with exponential backoff retry (3 retries).

## Database Schema

**Schema**: `current_account`

```mermaid
erDiagram
    accounts {
        uuid id PK
        varchar(100) account_id UK "ACC-xxxxxxxx"
        varchar(34) account_identification UK "IBAN"
        uuid party_id FK
        bigint balance "cents"
        bigint available_balance "cents"
        varchar(20) status "ACTIVE, FROZEN, CLOSED"
        bigint overdraft_limit "cents"
        bigint version "optimistic lock"
    }

    liens {
        uuid id PK
        uuid account_id FK
        bigint amount_cents
        varchar(20) status "ACTIVE, EXECUTED, TERMINATED"
        varchar(255) payment_order_reference UK "idempotency key"
        timestamptz expires_at "nullable"
    }

    accounts ||--o{ liens : "has"
```

## Configuration

| Variable | Default | Purpose |
|----------|---------|---------|
| `GRPC_PORT` | 50057 | gRPC server port |
| `DATABASE_URL` | - | PostgreSQL connection string |
| `K8S_NAMESPACE` | default | Kubernetes namespace for service discovery |
| `DB_MAX_OPEN_CONNS` | 25 | Connection pool size |

## Key Patterns

### Retry Idempotency

**Safe to Retry (Idempotent):**

| Method | Idempotency Key | Behavior |
|--------|-----------------|----------|
| `InitiateLien` | `PaymentOrderReference` | Returns existing lien if key matches |
| `ExecuteLien` | Lien ID (path param) | No-op if already EXECUTED |
| `TerminateLien` | Lien ID (path param) | No-op if already TERMINATED |
| `ExecuteDeposit` | `IdempotencyKey` header | Returns existing result if key matches |

**Retry Guidance:**

- Always include `IdempotencyKey` header for `ExecuteDeposit` to prevent duplicate credits
- `InitiateLien` uses `PaymentOrderReference` as natural idempotency key (unique per payment)
- Terminal state transitions (EXECUTED, TERMINATED) are no-ops on retry
- Use exponential backoff: 100ms → 200ms → 400ms (max 3 retries)

**Non-Idempotent Operations:**

- `InitiateCurrentAccount`: Creating duplicate accounts requires unique party/IBAN

### Overdraft Facility

```text
AvailableBalance = Balance + (OverdraftEnabled ? OverdraftLimit : 0)
```

Allows withdrawals beyond zero balance up to the configured limit.

### Payment Order Saga Integration

1. `InitiateLien` - Reserve funds (updates AvailableBalance)
2. External payment processing
3. `ExecuteLien` (success) or `TerminateLien` (failure/cancellation)

### Optimistic Locking

All mutations check `WHERE version = expected_version`. Returns conflict error on mismatch.

## Account Lifecycle Events

The service publishes events to Kafka for account lifecycle state transitions.
Events use fire-and-forget semantics - publishing failures are logged but do not fail
the business operation.

### Event Topics

| Event | Kafka Topic | Trigger |
|-------|-------------|---------|
| AccountFrozen | `current-account.account-frozen.v1` | Account transitions to FROZEN status |
| AccountUnfrozen | `current-account.account-unfrozen.v1` | Account transitions from FROZEN to ACTIVE |
| AccountClosed | `current-account.account-closed.v1` | Account transitions to CLOSED status |

### Event Schemas

Events are serialized as Protocol Buffers.
See `api/proto/meridian/events/v1/current_account_events.proto` for full schema definitions.

**AccountFrozenEvent:**

```json
{
  "event_id": "uuid",
  "account_id": "ACC-xxxxxxxx",
  "reason": "Suspicious activity detected",
  "frozen_at": "2024-01-15T10:30:00Z",
  "frozen_by": "compliance-officer",
  "correlation_id": "uuid",
  "causation_id": "uuid",
  "timestamp": "2024-01-15T10:30:00Z",
  "version": 5,
  "metadata": {}
}
```

**AccountUnfrozenEvent:**

```json
{
  "event_id": "uuid",
  "account_id": "ACC-xxxxxxxx",
  "unfrozen_at": "2024-01-16T14:00:00Z",
  "unfrozen_by": "compliance-officer",
  "correlation_id": "uuid",
  "causation_id": "uuid",
  "timestamp": "2024-01-16T14:00:00Z",
  "version": 6,
  "metadata": {}
}
```

**AccountClosedEvent:**

```json
{
  "event_id": "uuid",
  "account_id": "ACC-xxxxxxxx",
  "closing_balance": {"units": 0, "nanos": 0, "currency_code": "GBP"},
  "closure_reason": "Customer requested closure",
  "closed_by": "customer-service",
  "closure_date": "2024-01-20T09:00:00Z",
  "correlation_id": "uuid",
  "causation_id": "uuid",
  "timestamp": "2024-01-20T09:00:00Z",
  "version": 7,
  "metadata": {}
}
```

### Event Publishing Guarantees

- **Fire-and-forget**: Event publishing errors do not fail the business operation
- **At-most-once delivery**: Events may be lost if Kafka is unavailable
- **Idempotent consumers**: Consumers should handle duplicate events gracefully using `event_id`
- **Ordering**: Events are keyed by `account_id` for partition-level ordering

## Webhook Notifications

For regulatory compliance, account freeze and closure events trigger webhook notifications to tenant-configured HTTP endpoints.

### Supported Events

| Event Type | Trigger | Use Case |
|------------|---------|----------|
| `account.frozen` | Account frozen | Compliance reporting, customer notification |
| `account.closed` | Account closed | Regulatory reporting, audit trail |

### Webhook Configuration

Webhooks are configured per-tenant in the Tenant service. The webhook URL is retrieved via gRPC call to the Tenant service.

| Environment Variable | Default | Description |
|---------------------|---------|-------------|
| `WEBHOOK_REQUEST_TIMEOUT` | `5s` | HTTP request timeout per attempt |
| `WEBHOOK_MAX_RETRIES` | `3` | Maximum retry attempts |

### Retry Policy

Webhooks use exponential backoff with the following default delays:

| Attempt | Delay |
|---------|-------|
| 1st retry | 1 second |
| 2nd retry | 2 seconds |
| 3rd retry | 4 seconds |

After all retries are exhausted, the delivery is marked as failed in the audit table.

### Webhook Payload Format

```json
{
  "event_id": "550e8400-e29b-41d4-a716-446655440000",
  "event_type": "account.frozen",
  "timestamp": "2024-01-15T10:30:00Z",
  "account_id": "ACC-12345678",
  "tenant_id": "tenant-001",
  "reason": "Suspicious activity detected",
  "final_balance": {
    "amount": 150000,
    "currency_code": "GBP"
  }
}
```

**Field Notes:**

- `final_balance` is only included for `account.closed` events
- `amount` is in minor units (e.g., pence for GBP, cents for USD)
- `reason` is optional and included when provided in the control action request

### Webhook Delivery Audit

All webhook delivery attempts are recorded in the `webhook_deliveries` table for audit trail:

| Column | Type | Description |
|--------|------|-------------|
| `id` | UUID | Delivery record ID |
| `event_id` | VARCHAR | Event ID that triggered delivery |
| `event_type` | VARCHAR | Event type (account.frozen, account.closed) |
| `tenant_id` | VARCHAR | Tenant that owns the account |
| `account_id` | VARCHAR | Affected account ID |
| `webhook_url` | VARCHAR | Target webhook URL |
| `status` | VARCHAR | pending, success, or failed |
| `attempts` | INT | Number of delivery attempts |
| `last_attempt_at` | BIGINT | Unix timestamp of last attempt |
| `last_error` | VARCHAR | Error message from last failure |
| `response_code` | INT | HTTP status code from last attempt |
| `created_at` | BIGINT | Unix timestamp when queued |
| `completed_at` | BIGINT | Unix timestamp when completed/failed |

### Webhook Security

- Webhooks are sent via HTTPS (HTTP URLs in configuration are rejected)
- `User-Agent: Meridian-Webhook/1.0` header is included
- `Content-Type: application/json` header is set
- Tenants should validate the `tenant_id` matches their expected value

## References

- [BIAN Current Account Specification](https://github.com/bian-official/public/blob/main/release13.0.0/semantic-apis/oas3/yamls/CurrentAccount.yaml)
- [Service Architecture](../README.md)
- [Proto Definitions](../../api/proto/meridian/current_account/v1/)
- [Event Proto Definitions](../../api/proto/meridian/events/v1/current_account_events.proto)
