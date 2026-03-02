---
name: prd-033-gateway-architecture
description: Clarify gateway service boundaries per BIAN, extract financial-gateway from payment-order, rename api-gateway
triggers:

  - Adding or modifying payment rail integrations (Stripe, SWIFT, ACH)
  - Working on operational-gateway dispatch or webhook ingestion
  - Reviewing gateway service naming or responsibilities
  - Adding new external provider connections

instructions: |
  Meridian has three gateway concerns that map to BIAN service domains:

  1. api-gateway (renamed from gateway) — inbound HTTP/gRPC reverse proxy, auth, tenant resolution
  2. operational-gateway — outbound non-financial dispatch (IoT commands, KYC, partner files)
  3. financial-gateway (new) — outbound financial network I/O (Stripe, future SWIFT/ACH/FedNow)

  payment-order remains the domain orchestrator for payment lifecycle. It calls financial-gateway
  via gRPC for network I/O. financial-gateway is a thin adapter — no business logic, no decisions.

  Both operational-gateway and financial-gateway compose from shared dispatch infrastructure
  in shared/pkg/dispatch/ (retry, circuit breaker, connection management, acknowledgment tracking).
---

# PRD-033: Gateway Architecture — BIAN-Aligned Service Boundaries

**Author:** Meridian Platform Team
**Status:** Draft
**Date:** 2026-03-02

---

## 1. Problem Statement

Meridian has three services with "gateway" in the name or gateway-like
responsibilities, and the boundaries between them are unclear:

| Current Service | Actual Role | Problem |
|----------------|-------------|---------|
| `gateway` | API reverse proxy (inbound HTTP → gRPC) | Name collides with BIAN gateway concepts |
| `operational-gateway` | Outbound instruction dispatch | Currently handles both financial (Stripe via mappings) and non-financial dispatch |
| `payment-order` | Payment orchestration | Contains stripe-go SDK, webhook handlers, and network I/O mixed with business logic |

Additionally, Stripe integration is scattered across five services:

- `payment-order/adapters/gateway/stripe/` — PaymentIntents, webhooks, idempotency, platform fees
- `reconciliation/adapters/stripe/` — Settlement ingestion, balance transactions
- `control-plane/internal/stripe/` — Webhook consumption, reconciliation, preflight checks
- `party/verification/` — Stripe Identity (KYC)
- `reference-data/saga/defaults/stripe_payment/` — Starlark payment saga definitions

This scattering makes it difficult to:

- Reason about Stripe as a "payment rail" vs individual integrations
- Add a second payment rail (SWIFT, ACH, FedNow) without touching multiple services
- Maintain clear compliance/audit boundaries for financial message flows
- Distinguish financial from non-financial external communication

### 1.1 BIAN Reference Model

BIAN defines a three-tier model for payment communication:

| BIAN Service Domain | Responsibility |
|---------------------|----------------|
| **Payment Order** | Compliance checks, counterparty limits, netting. |
| **Payment Execution** (obsolete BIAN 14.0, now **Payment Settlement**) | Funds transfer orchestration. |
| **Financial Gateway** | Financial network connections. Pure I/O. |

BIAN also defines **Operational Gateway** separately: secure sending/receiving
of non-financial messages to/from external entities.

The key BIAN insight: the financial gateway is pure I/O. It formats messages,
manages encryption keys, handles retries and retransmission. It makes no
business decisions.

> *"The service domain does not create content itself, it provides a message
> exchange service between (financial) institutions."* — BIAN Financial Gateway

### 1.2 Smart Meter / IoT Distinction

The line between financial and non-financial is not "does it have financial
consequences?" but "is the message itself a financial instrument?"

- **Requesting HH data from a smart meter** → operational-gateway
  (asking a device for data; the data feeds into position-keeping
  via a saga, but the communication is non-financial)
- **Creating a Stripe PaymentIntent** → financial-gateway
  (the message IS the money moving)
- **Sending a tariff schedule to an IoT device** → operational-gateway
- **Receiving a SWIFT MT103** → financial-gateway

---

## 2. Target Architecture

### 2.1 Service Responsibilities

```text
┌─────────────────────────────────────────────────────────────────┐
│                        api-gateway                              │
│                   (renamed from gateway)                        │
│  ┌──────────────────────────────────────────────────────────┐   │
│  │  Inbound HTTP/REST/JSON, Connect, gRPC-Web               │   │
│  │  JWT/API-key authentication, tenant resolution            │   │
│  │  Rate limiting, request routing to backend services       │   │
│  │  WebSocket event streaming (Kafka/outbox)                 │   │
│  └──────────────────────────────────────────────────────────┘   │
└─────────────────────────────────────────────────────────────────┘

┌─────────────────────────────────────────────────────────────────┐
│                     payment-order                               │
│              (BIAN Payment Order + Payment Settlement)          │
│  ┌──────────────────────────────────────────────────────────┐   │
│  │  Payment state machine (pending → authorized → captured)  │   │
│  │  Compliance checks, counterparty limits                   │   │
│  │  Lien management (fund reservations)                      │   │
│  │  Business-level idempotency                               │   │
│  │  Saga coordination, domain events                         │   │
│  └──────────────────────────────────────────────────────────┘   │
│                            │                                    │
│                   calls via gRPC                                │
│                            ▼                                    │
├─────────────────────────────────────────────────────────────────┤
│                     financial-gateway                           │
│                    (BIAN Financial Gateway)                     │
│  ┌──────────────────────────────────────────────────────────┐   │
│  │  Payment rail adapters (Stripe, future SWIFT/ACH/FedNow) │   │
│  │  Webhook ingestion + signature verification               │   │
│  │  API key/secret management, key rotation                  │   │
│  │  Network-level retries, circuit breakers                  │   │
│  │  Message formatting, protocol translation                 │   │
│  │  NO BUSINESS LOGIC — pure I/O adapter                     │   │
│  └──────────────────────────────────────────────────────────┘   │
└─────────────────────────────────────────────────────────────────┘

┌─────────────────────────────────────────────────────────────────┐
│                   operational-gateway                           │
│                 (BIAN Operational Gateway)                      │
│  ┌──────────────────────────────────────────────────────────┐   │
│  │  Non-financial external dispatch                          │   │
│  │  IoT commands (smart meters, tariff pushes)               │   │
│  │  KYC verification dispatch                                │   │
│  │  Partner file exchange (CSV, SFTP)                        │   │
│  │  SLA-governed communication with third parties            │   │
│  └──────────────────────────────────────────────────────────┘   │
└─────────────────────────────────────────────────────────────────┘
```

### 2.2 Shared Dispatch Infrastructure

Both gateways compose from a shared library for common dispatch concerns:

**`shared/pkg/dispatch/`** (extracted from current `operational-gateway`):

- Connection management (provider credentials, endpoints)
- Retry with exponential backoff and jitter
- Circuit breaker (per-provider health tracking)
- Acknowledgment tracking (outbound → ack state machine)
- Instruction persistence (idempotent dispatch, crash recovery)
- Observability (metrics, tracing, structured logging)

The gateways differ in:

| Concern | financial-gateway | operational-gateway |
|---------|-------------------|---------------------|
| Audit trail | Immutable, regulatory-grade | SLA-dependent |
| Failure handling | Guaranteed delivery, reconciliation | Best-effort with retry |
| Secret management | HSM-ready, PCI-DSS scope | Standard vault/env |
| Message format | Payment-rail specific (Stripe API, ISO 20022) | Heterogeneous (HTTP, SFTP, MQTT) |
| Idempotency | Stripe idempotency keys, dedup | Provider-dependent |

### 2.3 Egress Flow (Outbound Payment)

```text
payment-order                    financial-gateway              Stripe
     │                                  │                         │
     │  DispatchPayment(amount,         │                         │
     │    currency, customer_ref,       │                         │
     │    rail: "stripe")               │                         │
     │ ────────────────────────────────>│                         │
     │                                  │  stripe.PaymentIntents  │
     │                                  │  .Create(...)           │
     │                                  │ ───────────────────────>│
     │                                  │                         │
     │                                  │  <── PaymentIntent obj  │
     │  <── DispatchResult              │                         │
     │      (provider_ref: pi_xxx,      │                         │
     │       status: REQUIRES_ACTION)   │                         │
```

### 2.4 Ingress Flow (Webhook)

```text
Stripe                     financial-gateway              payment-order
  │                               │                             │
  │  POST /webhooks/stripe        │                             │
  │  (payment_intent.succeeded)   │                             │
  │ ─────────────────────────────>│                             │
  │                               │  verify signature           │
  │                               │  parse event                │
  │                               │  publish domain event       │
  │                               │ ───────────────────────────>│
  │                               │                             │
  │                               │               update state machine
  │                               │               emit PaymentCaptured
```

---

## 3. Migration Plan

### Phase 1: Shared Dispatch Library

Extract common dispatch infrastructure from `operational-gateway` into `shared/pkg/dispatch/`:

**Source files (operational-gateway):**

- `adapters/httpadapter/http_dispatcher.go` → shared retry, circuit breaker patterns
- `adapters/secrets/env_secret_store.go` → shared secret resolution interface
- `adapters/persistence/connection_entity.go`, `connection_repository.go` → shared connection model
- `adapters/persistence/instruction_entity.go`, `instruction_repository.go` → shared instruction persistence
- `worker/dispatch_worker.go` → shared worker pattern (poll-dispatch-ack loop)

**Approach:** Extract interfaces and common types. Both gateways import from shared.
Operational-gateway refactors to consume the shared library, validating the extraction
before financial-gateway is created.

### Phase 2: Create financial-gateway Service

**New service:** `services/financial-gateway/`

**Extract from payment-order:**

- `adapters/gateway/stripe/stripe_gateway.go` → Stripe PaymentIntent adapter
- `adapters/gateway/stripe/client.go` → Multi-tenant Stripe client (Connected Accounts)
- `adapters/gateway/stripe/webhook_adapter.go` → Webhook signature verification + event parsing
- `adapters/gateway/stripe/platform_fee.go` → Platform fee calculation
- `adapters/gateway/stripe/idempotency.go` → Network-level idempotency key generation
- `adapters/gateway/stripe/manifest_tenant_config.go` → Tenant Stripe config from manifest
- `adapters/gateway/stripe/config.go` → Stripe connection config
- `adapters/gateway/resilient_gateway.go` → Retry/circuit breaker wrapper

**Extract from control-plane:**

- `internal/stripe/webhook.go` → Webhook event processing
- `internal/stripe/consumer.go` → Event queue consumption
- `internal/stripe/publisher.go` → Internal event publishing

**Extract from reconciliation (optional, Phase 3):**

- `adapters/stripe/settlement_ingestor.go` → Settlement data fetch
- `adapters/stripe/balance_transaction_client.go` → Balance transaction API

**Proto definition:** `api/proto/meridian/financial_gateway/v1/financial_gateway.proto`

Key RPCs:

```protobuf
service FinancialGatewayService {
  // Egress: send financial message to payment rail
  rpc DispatchPayment(DispatchPaymentRequest) returns (DispatchPaymentResponse);
  rpc DispatchRefund(DispatchRefundRequest) returns (DispatchRefundResponse);

  // Ingress: webhook registration and status
  rpc GetWebhookStatus(GetWebhookStatusRequest) returns (GetWebhookStatusResponse);

  // Provider management
  rpc GetProviderHealth(GetProviderHealthRequest) returns (GetProviderHealthResponse);
}
```

**payment-order changes:**

- Remove direct stripe-go dependency
- Replace `adapters/gateway/stripe/` with gRPC client to financial-gateway
- `payment_gateway.go` interface stays (port) — implementation changes from Stripe adapter to gRPC client
- Starlark payment saga (`v2.0.0.star`) routes to financial-gateway instead of operational-gateway

### Phase 3: Rename gateway → api-gateway

**Rename:** `services/gateway/` → `services/api-gateway/`

This is a separate phase because it touches:

- Docker Compose service names
- Kubernetes manifests
- CI/CD pipeline references
- Import paths across the codebase

Approach: Create the new directory, update imports, deprecate old path.
The gateway proto package name does not need to change if we use Go
module path aliasing.

### Phase 4: Clarify operational-gateway scope

Remove any financial dispatch mappings from operational-gateway.
After Phase 2, Stripe dispatch routes through financial-gateway.
Operational-gateway retains:

- KYC verification dispatch (Stripe Identity stays in
  `party/verification/` as a domain adapter, but dispatch mechanics
  route through operational-gateway)
- Smart meter data requests
- Partner file exchange (CSV/SFTP)
- IoT device commands
- General third-party SLA-governed communication

---

## 4. What Stays Where After Migration

### Stripe Code Disposition

| Current Location | Disposition | Target |
|-----------------|-------------|--------|
| `payment-order/adapters/gateway/stripe/` | Move | `financial-gateway/adapters/stripe/` |
| `payment-order/adapters/gateway/resilient_gateway.go` | Move | `shared/pkg/dispatch/` (generalized) |
| `payment-order/adapters/gateway/payment_gateway.go` | Keep | Interface stays, impl becomes gRPC client |
| `reconciliation/adapters/stripe/` | Keep (Phase 3) | Eventually moves to financial-gateway |
| `control-plane/internal/stripe/` | Move | `financial-gateway/adapters/stripe/webhook/` |
| `party/verification/stripe_provider.go` | Keep | KYC is non-financial, stays in party domain |
| `reference-data/saga/defaults/stripe_payment/` | Update | Saga calls financial-gateway, not operational-gateway |

### Service Dependencies (Post-Migration)

```text
                    ┌──────────────┐
                    │  api-gateway │ (inbound traffic)
                    └──────┬───────┘
                           │
              ┌────────────┼────────────┐
              ▼            ▼            ▼
      ┌──────────┐  ┌───────────┐  ┌──────────┐
      │  current  │  │  payment  │  │  party   │
      │  account  │  │   order   │  │          │
      └──────────┘  └─────┬─────┘  └────┬─────┘
                          │              │
                          ▼              ▼
                   ┌─────────────┐  ┌─────────────────┐
                   │  financial  │  │  operational     │
                   │  gateway    │  │  gateway         │
                   └──────┬──────┘  └────────┬────────┘
                          │                  │
                          ▼                  ▼
                   ┌──────────┐      ┌──────────────┐
                   │  Stripe  │      │  Smart meters │
                   │  SWIFT   │      │  KYC providers│
                   │  ACH     │      │  Partner SFTP │
                   └──────────┘      └──────────────┘
```

---

## 5. Success Criteria

1. **financial-gateway deployed as independent service** with Stripe adapter, webhook ingestion, and gRPC API
2. **payment-order has no direct stripe-go dependency** — all Stripe communication via financial-gateway gRPC
3. **shared/pkg/dispatch/ extracted** and consumed by both gateways
4. **gateway renamed to api-gateway** across codebase, Docker, and CI
5. **operational-gateway contains no financial dispatch** — only non-financial external communication
6. **Existing payment flows unbroken** — all integration tests pass, Stripe webhook processing works
7. **Starlark payment saga updated** to route through financial-gateway

---

## 6. Non-Goals

- **Adding new payment rails** (SWIFT, ACH, FedNow) — this PRD
  establishes the architecture that makes them possible,
  not the integrations themselves
- **Changing the payment-order domain model** — state machine,
  lien management, and saga coordination are unchanged
- **Moving reconciliation Stripe adapters** — settlement ingestion
  can move to financial-gateway in a future phase but is not blocking
- **KYC provider changes** — Stripe Identity integration stays in
  party service; dispatch mechanics are already
  operational-gateway-aligned

---

## 7. Risks and Mitigations

| Risk | Impact | Mitigation |
|------|--------|------------|
| Additional network hop (payment-order → financial-gateway) adds latency | Low — Stripe API calls are 200-500ms; local gRPC adds <1ms | Measure baseline latency before/after; both services can co-locate |
| Webhook delivery changes during migration | Medium — dropped webhooks lose payment confirmations | Blue/green: financial-gateway receives webhooks alongside payment-order during transition; deduplicate via idempotency keys |
| Shared dispatch library extraction breaks operational-gateway | Medium — operational-gateway is in active development | Extract incrementally; operational-gateway refactors first as validation |
| Gateway rename breaks CI/CD | Low — mechanical change | Single PR with sed-based rename; run full CI before merge |

---

## 8. Dependencies

- **PRD-032 (Event-Triggered Saga Execution)** — Starlark payment saga needs event triggers to route through financial-gateway
- **operational-gateway PRs (#1317-#1330)** — Active work that establishes dispatch patterns we'll extract to shared/pkg/dispatch/
- **Stripe Connect wiring** — existing Stripe integration must be stable before extraction

---

## 9. Phasing and Estimation

| Phase | Description | Complexity | Dependencies |
|-------|-------------|-----------|--------------|
| 1 | Extract shared dispatch library | 5 points | operational-gateway PRs merged |
| 2 | Create financial-gateway, extract Stripe | 8 points | Phase 1 |
| 3 | Rename gateway → api-gateway | 3 points | None (independent) |
| 4 | Clean operational-gateway scope | 2 points | Phase 2 |

Phases 1 and 3 can run in parallel. Phase 2 is the critical path.
Total: ~13 points on the critical path (Phases 1 → 2 → 4),
with Phase 3 independent.
