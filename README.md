# Meridian

[![Build & Test](https://github.com/meridianhub/meridian/actions/workflows/build.yml/badge.svg?branch=develop)](https://github.com/meridianhub/meridian/actions/workflows/build.yml?query=branch%3Adevelop)
[![Nightly](https://github.com/meridianhub/meridian/actions/workflows/nightly.yml/badge.svg)](https://github.com/meridianhub/meridian/actions/workflows/nightly.yml)
[![Code Quality](https://github.com/meridianhub/meridian/actions/workflows/quality.yml/badge.svg?branch=develop)](https://github.com/meridianhub/meridian/actions/workflows/quality.yml?query=branch%3Adevelop)
[![Security Scanning](https://github.com/meridianhub/meridian/actions/workflows/security.yml/badge.svg?branch=develop)](https://github.com/meridianhub/meridian/actions/workflows/security.yml?query=branch%3Adevelop)
[![Go Version](https://img.shields.io/github/go-mod/go-version/meridianhub/meridian)](https://go.dev/)
[![License: BSL 1.1](https://img.shields.io/badge/License-BSL_1.1-blue.svg)](LICENSE)

Open-source billing engine with a double-entry ledger.
Define your pricing and settlement logic in code —
Meridian handles the bookkeeping, audit trails, and payment collection.

> **Status**: Active development. Core ledger, saga orchestration, and Stripe Connect
> integration are implemented. Looking for design partners.
> [Contact](mailto:ben@meridianhub.org)

## The Problem

Billing starts as a Stripe checkout and a cron job. Then you need usage metering.
Then revenue splits. Then your auditor asks for a transaction trail.
Then estimates need to reconcile with actuals.
Now you're maintaining a financial system — and you're not a financial company.

## How It Works

Define your economy in a declarative manifest — instruments, account types, and
settlement rules. Meridian provisions the ledger, enforces double-entry
bookkeeping, and handles compensation when something fails.

```bash
# Register a customer
curl -X POST http://localhost:8090/v1/parties \
  -H "X-Tenant-ID: default" \
  -d '{"partyType": "PARTY_TYPE_PERSON", "legalName": "Alice Smith"}'

# Open an account
curl -X POST http://localhost:8090/v1/current-accounts \
  -H "X-Tenant-ID: default" \
  -d '{"partyId": "...", "baseCurrency": "CURRENCY_GBP"}'

# Deposit — ledger entries, position updates, and audit trail created atomically
curl -X POST http://localhost:8090/v1/current-accounts/{id}/deposits \
  -H "X-Tenant-ID: default" \
  -d '{"amount": {"amount": {"currencyCode": "GBP", "units": "100"}}}'
```

Behind the scenes, each operation runs as a **saga** — a multi-step workflow
that either completes fully or compensates automatically. Sagas are written in
sandboxed scripts that Meridian validates before execution.

## What's Built

- **Double-entry ledger** with immutable, bi-temporal audit trail
- **Saga orchestration** with automatic compensation — settlement completes or reverts
- **Stripe Connect** integration for multi-tenant payment collection
- **Multi-asset support** — currency, kWh, carbon credits, compute hours
- **Reconciliation** — variance detection between expected and actual
- **Usage metering** — ingest events, apply pricing rules, produce invoices
- **Identity verification** — pluggable providers (Onfido, mock for development)
- **Multi-tenant** — org-scoped accounts with data isolation

## Quick Start

**Prerequisites**: Go 1.26+, Docker, kubectl, kind, tilt

```bash
git clone git@github.com:meridianhub/meridian.git
cd meridian
go mod download

ctlptl create cluster kind --registry=ctlptl-registry --name=kind-meridian-local
tilt up
```

- Tilt UI: <http://localhost:10350>
- API: <http://localhost:8080>
- gRPC: localhost:9090

See [CONTRIBUTING.md](CONTRIBUTING.md) for detailed setup and development workflow.

## Local Development

The dev stack runs Meridian as a single unified binary backed by a single-node CockroachDB instance.
It is the fastest way to iterate locally without Kubernetes.

### Prerequisites

| Tool | Version | Install |
|------|---------|---------|
| Docker + Docker Compose v2 | 24+ / 2.20+ | [docs.docker.com](https://docs.docker.com/get-docker/) |
| Go | 1.26+ | [go.dev/dl](https://go.dev/dl/) |
| grpcurl | any | `brew install grpcurl` |
| jq | any | `brew install jq` |

### Start and Stop

```bash
# Build images and start the stack (CockroachDB + migrations + meridian)
make dev-up

# Stop containers, preserve data volumes
make dev-down

# Stop containers and delete all data volumes (full reset)
make dev-clean
```

`make dev-up` runs `docker compose -f deploy/dev/docker-compose.yml up --build`. On first run this
builds the `meridian` Docker image from source, which takes a few minutes. Subsequent runs use the
cached image unless source files have changed.

The `migrate` container applies all embedded schema migrations to CockroachDB on startup and then
exits. The `meridian` container starts only after migrations complete successfully.

### Port Mappings

| Port | Service | Protocol |
|------|---------|----------|
| 26257 | CockroachDB SQL (postgres wire) | TCP |
| 8080 | CockroachDB web UI | HTTP |
| 50051 | Meridian gRPC | TCP |
| 8090 | Meridian HTTP gateway | HTTP |

CockroachDB is bound to `127.0.0.1` only. The Meridian ports are accessible from `localhost`.

### Health Check

```bash
# HTTP health endpoint (returns 200 OK when the binary is running)
curl -s http://localhost:8090/healthz

# gRPC health check (requires grpcurl)
grpcurl -plaintext localhost:50051 grpc.health.v1.Health/Check
```

### Using grpcurl

The gRPC server has reflection enabled, so you can discover all available methods without a proto
file.

```bash
# List all services
grpcurl -plaintext localhost:50051 list

# List methods for a specific service
grpcurl -plaintext localhost:50051 list meridian.party.v1.PartyService

# Describe a request message
grpcurl -plaintext localhost:50051 describe meridian.party.v1.RegisterPartyRequest
```

### Basic Business Flow

The gateway accepts REST/JSON on port 8090 and native gRPC on port 50051. The following commands
execute a complete deposit flow via the HTTP/JSON gateway.

```bash
# 1. Register a party (customer)
PARTY=$(curl -s -X POST http://localhost:8090/v1/parties \
  -H "Content-Type: application/json" \
  -H "X-Tenant-ID: default" \
  -d '{"partyType": "PARTY_TYPE_PERSON", "legalName": "Alice Smith"}')
echo $PARTY | jq .
PARTY_ID=$(echo $PARTY | jq -r '.party.partyId')

# 2. Open a current account (GBP)
ACCOUNT=$(curl -s -X POST http://localhost:8090/v1/current-accounts \
  -H "Content-Type: application/json" \
  -H "X-Tenant-ID: default" \
  -d "{
    \"partyId\": \"$PARTY_ID\",
    \"accountIdentification\": \"GB29NWBK60161331926819\",
    \"baseCurrency\": \"CURRENCY_GBP\"
  }")
echo $ACCOUNT | jq .
ACCOUNT_ID=$(echo $ACCOUNT | jq -r '.facility.accountId')

# 3. Deposit 100 GBP
curl -s -X POST "http://localhost:8090/v1/current-accounts/$ACCOUNT_ID/deposits" \
  -H "Content-Type: application/json" \
  -H "X-Tenant-ID: default" \
  -d '{
    "amount": {"amount": {"currencyCode": "GBP", "units": "100"}},
    "description": "Initial deposit",
    "reference": "REF-001",
    "idempotencyKey": {"key": "dep-001"}
  }' | jq .

# 4. Check balances
curl -s "http://localhost:8090/v1/accounts/$ACCOUNT_ID/balances" \
  -H "X-Tenant-ID: default" | jq .
```

The same flow is available over native gRPC (bypasses the gateway):

```bash
# Register a party
grpcurl -plaintext \
  -H "x-tenant-id: default" \
  -d '{"partyType": "PARTY_TYPE_PERSON", "legalName": "Alice Smith"}' \
  localhost:50051 meridian.party.v1.PartyService/RegisterParty

# Open a current account (substitute PARTY_ID from previous response)
grpcurl -plaintext \
  -H "x-tenant-id: default" \
  -d '{"partyId": "PARTY_ID", "accountIdentification": "GB29NWBK60161331926819", "baseCurrency": "CURRENCY_GBP"}' \
  localhost:50051 meridian.current_account.v1.CurrentAccountService/InitiateCurrentAccount

# Deposit 100 GBP (substitute ACCOUNT_ID from previous response)
grpcurl -plaintext \
  -H "x-tenant-id: default" \
  -d '{
    "accountId": "ACCOUNT_ID",
    "amount": {"amount": {"currencyCode": "GBP", "units": "100"}},
    "description": "Initial deposit",
    "reference": "REF-001"
  }' \
  localhost:50051 meridian.current_account.v1.CurrentAccountService/ExecuteDeposit
```

See [docs/guides/calling-meridian-apis.md](docs/guides/calling-meridian-apis.md) for a detailed
API guide covering all supported protocols, error handling, and tenant isolation.

### API Explorer

A Swagger UI is embedded at `api/openapi/swagger-ui.html`. To browse the API interactively while
the dev stack is running:

```bash
make swagger-ui
# Open http://localhost:8091/swagger-ui.html
```

### Known Limitations vs Full Kubernetes Stack

| Feature | Dev stack | Full K8s stack |
|---------|-----------|----------------|
| Kafka event publishing | Disabled (no-op) | Enabled |
| Redis idempotency store | Disabled (Postgres fallback) | Enabled |
| Authentication | Disabled (`AUTH_MODE=disabled`) | Keycloak OIDC |
| Cron jobs / schedulers | Not running | Running |
| Multi-replica | Single process | Horizontal pod autoscaling |
| Observability (OTLP) | Disabled | Enabled |
| TLS | None | cert-manager |

The dev stack is suitable for feature development and integration testing. Use the full K8s stack
(`make deploy-local`) when validating auth flows, event-driven behaviour, or multi-replica
correctness.

## Architecture

Built on [BIAN](https://bian.org/) banking service domain patterns.

| Service | Purpose |
|---------|---------|
| **CurrentAccount** | Customer accounts, transaction orchestration |
| **PositionKeeping** | Pre-ledger transaction log, position tracking |
| **FinancialAccounting** | Double-entry bookkeeping |
| **PaymentOrder** | Settlement, Stripe Connect integration |
| **Party** | Customer data, identity verification |
| **MarketInformation** | Pricing data with quality ladder (estimate / actual / verified) |
| **Reconciliation** | Variance detection, dispute management |
| **Forecasting** | Forward curves and forecast generation |
| **ControlPlane** | Tenant management, manifest configuration, Stripe billing |
| **EventRouter** | CEL-filtered event routing, saga triggering |
| **FinancialGateway** | External payment provider dispatch and reconciliation |
| **OperationalGateway** | Non-financial outbound dispatch (KYC, IoT, partner) |
| **Identity** | OIDC authentication and identity provider integration |
| **MCP Server** | AI assistant integration via Model Context Protocol |

See [services/README.md](services/README.md) for the full architecture diagram
and [docs/adr/](docs/adr/) for architectural decisions.

## Technology

Go | Protocol Buffers + gRPC | CockroachDB | Apache Kafka | Kubernetes | Stripe Connect

## License

Business Source License 1.1 — See [LICENSE](LICENSE).

- Use, modify, and deploy for any internal purpose
- Cannot offer a competing Billing/Treasury-as-a-Service
- Converts to Apache 2.0 on February 12, 2030
