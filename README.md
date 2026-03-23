# Meridian

[![Build & Test](https://github.com/meridianhub/meridian/actions/workflows/build.yml/badge.svg?branch=develop)](https://github.com/meridianhub/meridian/actions/workflows/build.yml?query=branch%3Adevelop)
[![Nightly](https://github.com/meridianhub/meridian/actions/workflows/nightly.yml/badge.svg)](https://github.com/meridianhub/meridian/actions/workflows/nightly.yml)
[![Code Quality](https://github.com/meridianhub/meridian/actions/workflows/quality.yml/badge.svg?branch=develop)](https://github.com/meridianhub/meridian/actions/workflows/quality.yml?query=branch%3Adevelop)
[![Security Scanning](https://github.com/meridianhub/meridian/actions/workflows/security.yml/badge.svg?branch=develop)](https://github.com/meridianhub/meridian/actions/workflows/security.yml?query=branch%3Adevelop)
[![codecov](https://codecov.io/gh/meridianhub/meridian/branch/develop/graph/badge.svg)](https://codecov.io/gh/meridianhub/meridian)
[![Go Report Card](https://goreportcard.com/badge/github.com/meridianhub/meridian)](https://goreportcard.com/report/github.com/meridianhub/meridian)
[![Go Reference](https://pkg.go.dev/badge/github.com/meridianhub/meridian.svg)](https://pkg.go.dev/github.com/meridianhub/meridian)
[![Go Version](https://img.shields.io/github/go-mod/go-version/meridianhub/meridian)](https://go.dev/)
[![License: BSL 1.1](https://img.shields.io/badge/License-BSL_1.1-blue.svg)](LICENSE)
[![Ask DeepWiki](https://deepwiki.com/badge.svg)](https://deepwiki.com/meridianhub/meridian)

**A source-available transaction integrity engine for the real-world economy.**

Define your pricing, settlement, and multi-party revenue-sharing logic in code.
Meridian handles the bookkeeping, audit trails, reconciliation, and payment collection.

> **Status**: Active development. Core ledger, saga orchestration, multi-asset support, and Stripe Connect
> integration are implemented. Looking for design partners.
> [Contact](mailto:ben@meridianhub.org)

## Why Meridian

Billing starts as a Stripe checkout and a cron job. Then you need usage
metering. Then revenue splits. Then your auditor asks for a transaction trail.
Then estimates need to reconcile with actuals.
Now you're maintaining a financial system - and you're not a financial company.

## What Makes It Different

### Every movement of value is traceable

Whether you're moving money, megawatts, or carbon credits - every transaction
is recorded in a double-entry ledger with an immutable audit trail. Meridian
natively handles multiple asset types (currency, energy, carbon, compute hours,
vouchers) in a single ledger, with type safety that prevents accidentally
mixing incompatible units.

### Multi-step operations that recover from failure

Real-world transactions span multiple services - reserving funds, posting to
the ledger, triggering settlement. If any step fails partway through, Meridian
automatically reverses the completed steps to keep the system consistent. No
partial state, no manual cleanup.

Business logic is written in
[Starlark](https://github.com/bazelbuild/starlark), a subset of Python used
by Google's Bazel. It reads like simple Python, so your team reviews sagas
the same way they review any other code - no new DSL to learn. Meridian's
runtime enforces execution step limits, so every workflow is guaranteed to
terminate - no runaway scripts can exhaust compute resources. Errors are
caught at compile time, and AI can generate reliable saga code because the
schema constrains it to only call real handlers with real types.

### Define your economy, then run it

Define instruments, account types, settlement rules, and pricing logic in a
declarative manifest. Meridian doesn't just provision the configuration - it
continuously operates it. Scheduled billing fires monthly. Settlement triggers
when market data arrives. Failed steps reverse automatically. The manifest
*is* the running system.

A complete tote betting platform - instruments, accounts, double-entry
settlement, Stripe integration, event-driven payouts - can be expressed in
under 400 lines of manifest. No code deployment. No PRs to Meridian core.
No migrations.

### Data that knows its own quality

Measurements carry what was known, how it was known, and when it was known.
The quality ladder (Estimate -> Coefficient -> Actual -> Revised) handles
late-arriving data, out-of-order meter reads, and delayed settlement without
locking the database.

### AI-configurable

Because the economy is declarative and the scripting language is constrained,
the entire economic model can be configured by conversation. AI generates
working saga code, the schema catches mistakes at compile time, and the UI
lets you visualize your instruments, accounts, and settlement flows before
anything runs.

## What You Get

- **Double-entry ledger** with immutable audit trail
- **Automatic failure recovery** - multi-step operations complete or revert cleanly
- **Multi-asset support** - currency, kWh, carbon credits, compute hours, vouchers, custom instruments
- **Stripe Connect** integration for multi-tenant payment collection
- **Reconciliation** - variance detection, dispute management, imbalance tracking
- **Usage metering** - ingest events, apply pricing rules, produce invoices
- **Market data** - bi-temporal observations with quality ladder and wash-and-reload corrections
- **Identity & KYC** - pluggable verification providers (Onfido, Stripe Identity)
- **Multi-tenant** - schema-per-tenant data isolation
- **Event routing** - CEL-filtered event routing to saga handlers
- **Financial gateway** - Stripe payment intents, webhook handling, refund compensation
- **Operational gateway** - non-financial outbound dispatch (IoT, regulatory, partner integrations)
- **Observability** - OpenTelemetry tracing, Prometheus metrics, structured logging
- **AI integration** - MCP server for AI assistant access to the platform

## Use Cases

Meridian ships with a [cookbook](cookbook/) of ready-to-use saga patterns and [example manifests](examples/manifests/):

| Pattern | Description |
|---------|-------------|
| **SaaS billing** | Usage metering, tiered pricing, dunning, Stripe settlement |
| **Energy settlement** | Half-hourly metering, estimate-to-actual reconciliation, grid balancing |
| **Carbon offset** | Credit registry, retirement tracking, verified offset settlement |
| **Tote betting** | Pooled stakes, event-driven settlement, proportional payout distribution |
| **Payment gateway** | Stripe payment intents, webhook handling, refund compensation |
| **KYC compliance** | Identity verification gates before account activation |
| **Precious metals** | Commodity accounts with market-rate valuation |
| **Dynamic pricing** | Time-of-use and capacity-based pricing with real-time adjustment |

## Architecture

All services compile into a single binary for simplified deployment, organized into
[BIAN](https://bian.org/)-aligned domain services with hexagonal architecture.

### Domain Services

| Service | Purpose |
|---------|---------|
| **CurrentAccount** | Customer accounts, deposits, withdrawals, lien management |
| **PositionKeeping** | High-frequency position log, balance computation, compaction |
| **FinancialAccounting** | Double-entry bookkeeping, ledger posting |
| **PaymentOrder** | Payment orchestration, Stripe settlement, dunning, billing |
| **Party** | Customer registration, KYC verification, payment methods |
| **ReferenceData** | Instrument registry, account types, CEL validation, saga definitions |
| **MarketInformation** | Bi-temporal market data, quality ladder, delta engine |
| **Reconciliation** | Settlement runs, variance detection, dispute management |
| **Forecasting** | Forward curves and forecast generation |

### Platform Services

| Service | Purpose |
|---------|---------|
| **ControlPlane** | Manifest system, tenant management, Stripe billing |
| **EventRouter** | CEL-filtered event routing, saga triggering, utilization metering |
| **FinancialGateway** | Stripe payment intent adapter, webhook handling |
| **OperationalGateway** | Non-financial outbound dispatch (IoT, regulatory, partner) |
| **Identity** | Embedded Dex OIDC, SSO, JWT/API-key authentication |
| **MCP Server** | AI assistant integration via Model Context Protocol |

See [services/README.md](services/README.md) for the full architecture diagram
and [docs/adr/](docs/adr/) for architectural decisions.

## Technology

| Layer | Stack |
|-------|-------|
| Language | Go |
| API | Protocol Buffers, gRPC, REST (transcoded) |
| Database | PostgreSQL / CockroachDB |
| Messaging | Apache Kafka |
| Orchestration | Kubernetes, Tilt (local dev) |
| Scripting | Starlark (sagas), CEL (validation/routing) |
| Payments | Stripe Connect |
| Auth | Dex (OIDC), JWT, API keys |
| Observability | OpenTelemetry, Prometheus, Grafana |

## Try It

**Prerequisites**: Docker and Docker Compose

```bash
git clone https://github.com/meridianhub/meridian.git
cd meridian
make dev-up
```

Open [localhost:5173](http://localhost:5173) to explore the web UI - register
customers, open accounts, make deposits, and see the ledger in action. Every
operation runs as a saga across multiple services with automatic compensation
on failure.

REST API is also available at `localhost:8090` and gRPC at `localhost:50051`.

See [CONTRIBUTING.md](CONTRIBUTING.md) for the full development setup, Kubernetes deployment, and API reference.

## License

Business Source License 1.1 - See [LICENSE](LICENSE).

Free for development, testing, and evaluation. Production use is free for up
to 5,000 active accounts across all tenants. Beyond that, a commercial license
is required.

Converts to Apache 2.0 on February 12, 2030. For commercial licensing,
contact [ben@meridianhub.org](mailto:ben@meridianhub.org).
