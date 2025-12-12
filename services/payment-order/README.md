---
name: payment-order-service
description: BIAN payment order saga orchestrator for fund reservation, gateway execution, and settlement
triggers:
  - Implementing payment order workflows
  - Designing saga patterns for distributed transactions
  - Adding payment processing to services
  - Handling external payment gateway callbacks
  - Managing fund reservations and settlements
  - Webhook signature validation (HMAC-SHA256)
instructions: |
  PaymentOrder orchestrates money movement as a saga:

  State Machine: INITIATED → RESERVED → EXECUTING → COMPLETED
                        ↘ FAILED (with compensation)
                        ↘ CANCELLED (before EXECUTING)
  COMPLETED → REVERSED (post-completion compensation)

  Key flows:
  1. InitiatePaymentOrder: Creates order, validates amount/IBAN
  2. Async worker reserves funds via CurrentAccount.InitiateLien
  3. Async worker sends to gateway, transitions to EXECUTING
  4. Gateway webhook calls UpdatePaymentOrder (HMAC-protected)
  5. On settlement: COMPLETED, async ExecuteLien with retry (max 5)
  6. On rejection: FAILED, releases lien (automatic compensation)

  Cancellation only allowed before EXECUTING
  Reversal only allowed for COMPLETED orders

  Ports: 50054 (gRPC), 8080 (HTTP webhooks)
---

# PaymentOrder Service

BIAN-compliant payment order service with saga orchestration for distributed transactions.

## Overview

| Attribute | Value |
|-----------|-------|
| **BIAN Domain** | Payment Order |
| **Port** | 50054 (gRPC), 8080 (HTTP) |
| **Language** | Go |
| **Database** | PostgreSQL/CockroachDB |
| **Standalone** | No (requires CurrentAccount) |

## gRPC Methods

| Method | HTTP | Purpose |
|--------|------|---------|
| `InitiatePaymentOrder` | `POST /v1/payment-orders` | Create payment |
| `RetrievePaymentOrder` | `GET /v1/payment-orders/{id}` | Get order details |
| `UpdatePaymentOrder` | `PATCH /v1/payment-orders/{id}` | Handle webhook callback |
| `CancelPaymentOrder` | `POST /v1/payment-orders/{id}/cancel` | Cancel before execution |
| `ReversePaymentOrder` | `POST /v1/payment-orders/{id}/reverse` | Reverse completed payment |
| `ListPaymentOrders` | `GET /v1/payment-orders` | List with filters |

## HTTP Endpoints

| Endpoint | Method | Purpose |
|----------|--------|---------|
| `/webhook/payment-gateway` | POST | External gateway callbacks |
| `/health` | GET | Health check |

## Domain Model

### PaymentOrder

```text
PaymentOrder {
  ID: UUID
  DebtorAccountID: string
  CreditorReference: string (IBAN)
  Amount: Money
  Status: INITIATED | RESERVED | EXECUTING | COMPLETED | FAILED | CANCELLED | REVERSED
  LienID: string
  GatewayReferenceID: string
  IdempotencyKey: string
  FailureReason: string
  LienExecutionStatus: PENDING | SUCCEEDED | FAILED
  LienExecutionAttempts: int (max 5)
  Version: int
}
```

## Payment Saga State Machine

```text
INITIATED
    │
    ├──→ RESERVED (funds reserved via InitiateLien)
    │        │
    │        ├──→ EXECUTING (sent to gateway)
    │        │        │
    │        │        ├──→ COMPLETED (gateway settled)
    │        │        │        │
    │        │        │        └──→ REVERSED (post-completion reversal)
    │        │        │
    │        │        └──→ FAILED (gateway rejected, lien released)
    │        │
    │        └──→ CANCELLED (user cancelled, lien released)
    │
    └──→ FAILED (reservation failed)
```

## Saga Orchestration Flow

### Happy Path

1. **Initiation**: `InitiatePaymentOrder` creates order in INITIATED status
2. **Reservation**: Async worker calls `CurrentAccount.InitiateLien` → RESERVED
3. **Execution**: Async worker sends to payment gateway → EXECUTING
4. **Settlement**: Gateway webhook confirms → COMPLETED
5. **Lien Execution**: Async `ExecuteLien` converts reservation to debit (retries up to 5x)

### Compensation

- **Gateway Rejection**: Automatically releases lien via `TerminateLien`
- **Cancellation**: User cancels before EXECUTING, lien released
- **Reversal**: Manual reversal of COMPLETED orders creates compensating entries

## Webhook Security

| Feature | Implementation |
|---------|----------------|
| Authentication | HMAC-SHA256 signature |
| Header | `X-Webhook-Signature` |
| Timestamp | Max 5 minutes age |
| Rate Limiting | 100 req/sec per IP |
| Idempotency | Deterministic key from (gateway_ref + status + gateway_event_ts) |

## Service Dependencies

| Service | Port | Purpose |
|---------|------|---------|
| CurrentAccount | 50057 | Lien operations (reserve, execute, terminate) |

## Database Schema

**Schema**: `payment_order`

### payment_orders Table

| Column | Type | Purpose |
|--------|------|---------|
| `id` | UUID | Primary key |
| `debtor_account_id` | VARCHAR(255) | Account to debit |
| `creditor_reference` | VARCHAR(255) | Target IBAN |
| `amount_cents` | BIGINT | Amount in cents |
| `currency` | CHAR(3) | ISO 4217 |
| `status` | VARCHAR(20) | Saga state |
| `lien_id` | VARCHAR(255) | CurrentAccount reservation |
| `gateway_reference_id` | VARCHAR(255) | External transaction ID |
| `idempotency_key` | VARCHAR(255) | Unique constraint |
| `lien_execution_status` | VARCHAR(20) | PENDING, SUCCEEDED, FAILED |
| `lien_execution_attempts` | INT | Retry counter |
| `version` | INT | Optimistic locking |

## Kafka Events

| Topic | Purpose |
|-------|---------|
| `payment-order.initiated.v1` | Order created |
| `payment-order.reserved.v1` | Funds reserved |
| `payment-order.executing.v1` | Sent to gateway |
| `payment-order.completed.v1` | Settlement confirmed |
| `payment-order.failed.v1` | Processing failed |
| `payment-order.cancelled.v1` | User cancelled |
| `payment-order.reversed.v1` | Post-completion reversal |

## Configuration

| Variable | Default | Purpose |
|----------|---------|---------|
| `GRPC_PORT` | 50054 | gRPC server port |
| `HTTP_PORT` | 8080 | Webhook server port |
| `WEBHOOK_HMAC_SECRET` | - | Signature validation |
| `HTTP_RATE_LIMIT_PER_SECOND` | 100 | Rate limit |
| `HTTP_RATE_LIMIT_BURST` | 200 | Burst allowance |

## References

- [BIAN Payment Order Specification](https://github.com/bian-official/public/blob/main/release13.0.0/semantic-apis/oas3/yamls/PaymentOrder.yaml)
- [Service Architecture](../README.md)
- [Proto Definitions](../../api/proto/meridian/payment_order/v1/)
