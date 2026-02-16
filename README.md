# Meridian

**Define your billing model in code. We handle the ledger.**

```python
# Your business logic, not ours
def distribute_revenue(ctx):
    participants = party.list_participants(ctx.org_id)
    for p in participants:
        share = party.get_structuring_data(p.id, ctx.org_id)["allocation_share"]
        account = resolve_account(party_id=p.id, org_id=ctx.org_id, currency="GBP")
        post(account, ctx.amount * Decimal(share), "CREDIT")
```

Meridian is a programmable billing engine. You define what to charge, how to split revenue, and when to settle — in [Starlark](https://github.com/google/starlark-go) (a locked-down Python) and [CEL](https://cel.dev/) (for validation). We provide the double-entry ledger, audit trails, and payment integration.

## The Problem

Billing systems are either:

1. **Rigid SaaS** — works until your model doesn't fit their assumptions
2. **Custom-built** — 6 months of engineering before you bill anyone
3. **Spreadsheets** — until the auditor asks for proof

You need infrastructure that adapts to *your* model, not the other way around.

## The Solution

| Layer | Technology | Purpose |
|-------|------------|---------|
| **Business Logic** | Starlark | Sagas, distributions, settlement flows |
| **Validation** | CEL | Account policies, limits, eligibility rules |
| **Valuation** | CEL + Market Data | Price anything: kWh, carbon credits, GPU-hours |
| **Ledger** | Double-entry, bi-temporal | Every balance stored, every change traceable |
| **Payments** | Stripe Connect | Real money in, real money out |

### Starlark: Your Logic, Sandboxed

Starlark is Python without the footguns. No imports, no filesystem, no network — just pure business logic that runs deterministically. When a saga fails, it replays identically.

```python
# Contribution saga: member funds a syndicate position
def contribute(ctx):
    # Reserve funds from personal account
    reserve(ctx.from_account, ctx.amount)

    # Credit org-scoped account
    credit(resolve_account(
        party_id=ctx.party_id,
        org_id=ctx.org_id,
        currency=ctx.currency
    ), ctx.amount)

    # Record the structuring data
    return {"contributed": str(ctx.amount)}
```

### CEL: Validate Before You Transact

CEL expressions guard every operation. Define once, enforce everywhere.

```cel
// Account policy: daily limit
transaction.amount <= account.daily_limit - account.daily_spent

// Eligibility: KYC verified
party.verification_status == "VERIFIED"

// Valuation: time-of-use energy pricing
rate_schedule.lookup(timestamp.hour) * quantity
```

### Bi-Temporal: Prove Everything

Every record tracks two timelines:

- **Event time**: When it happened in the real world
- **Knowledge time**: When the system learned about it

When estimates become actuals, we don't overwrite — we supersede. The audit trail shows exactly what you knew, when you knew it, and what changed.

This is the difference between "trust me" and "verify it yourself."

## Who It's For

Meridian is infrastructure for businesses that:

- Bill for things that aren't simple subscriptions
- Need audit trails that survive regulatory scrutiny
- Want to define business logic, not maintain billing code
- Handle multi-party splits, pooling, or syndication

### Example Verticals

| Vertical | What You'd Bill | Why Meridian |
|----------|-----------------|--------------|
| **Energy** | kWh at time-of-use rates | Bi-temporal estimates → actuals |
| **Marketplaces** | Revenue splits to sellers | Multi-party distribution sagas |
| **Betting/Gaming** | Pool contributions and payouts | Segregated funds, audit trail |
| **Carbon** | Tonnes CO₂e at exchange prices | Multi-asset with market data |
| **GPU Cloud** | Compute-hours at spot pricing | Usage metering with valuation |

## Compliance-Ready

Built for regulated industries:

| Requirement | How Meridian Helps |
|-------------|-------------------|
| **Audit Trail** | Immutable, bi-temporal, every transaction traceable to origin |
| **Segregation of Funds** | Org-scoped accounts track positions within pools |
| **AML/KYC** | Party service integrates with Stripe Identity |
| **Fair & Transparent** | Market Data Service provides verifiable pricing |
| **Reconciliation** | Automated variance detection and settlement lifecycle |

The architecture doesn't change for compliance — it's how the system works by default.

## Quick Start

```bash
# Clone and setup
git clone git@github.com:meridianhub/meridian.git
cd meridian
go mod download

# Local Kubernetes cluster
ctlptl create cluster kind --registry=ctlptl-registry --name=kind-meridian-local

# Start development environment
tilt up
```

**Access:**
- Tilt UI: http://localhost:10350
- API Gateway: http://localhost:8080
- gRPC: localhost:9090

See [CONTRIBUTING.md](CONTRIBUTING.md) for detailed setup.

## Architecture

Meridian follows [BIAN](https://bian.org/) service domain patterns — the same architecture used by global banks, adapted for modern infrastructure.

| Service | Purpose |
|---------|---------|
| **CurrentAccount** | Customer accounts, transaction orchestration |
| **PositionKeeping** | Pre-ledger transaction log, position tracking |
| **FinancialAccounting** | Double-entry bookkeeping, general ledger |
| **PaymentOrder** | Saga orchestration, settlement |
| **MarketInformation** | Bi-temporal pricing with quality ladder |
| **Party** | Customer data, associations, KYC status |
| **Reconciliation** | Variance detection, dispute management |
| **ControlPlane** | Manifest management, Stripe billing |

See [docs/adr/](docs/adr/) for architectural decisions.

## Technology

- **Language**: Go
- **API**: Protocol Buffers + gRPC
- **Database**: CockroachDB (distributed SQL)
- **Events**: Apache Kafka
- **Orchestration**: Kubernetes
- **Payments**: Stripe Connect

## Documentation

- [Architecture Decisions](docs/adr/) — why we built it this way
- [API Reference](api/proto/) — Protocol Buffer definitions
- [PRDs](docs/prd/) — feature specifications
- [Contributing](CONTRIBUTING.md) — development setup and standards

## License

Business Source License 1.1 — See [LICENSE](LICENSE).

- Use, modify, and deploy for your business
- Cannot offer competing Billing/Treasury-as-a-Service
- Converts to Apache 2.0 on January 14, 2030

Same model as CockroachDB, MariaDB, and HashiCorp.

---

**MeridianHub** — Programmable billing infrastructure.

[Website](https://meridianhub.cloud) ·
[Documentation](https://docs.meridianhub.cloud) ·
[Contact](mailto:hello@meridianhub.cloud)
