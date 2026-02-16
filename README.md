# Meridian

A programmable billing engine with business logic defined in Starlark and CEL.

```python
def distribute_revenue(ctx):
    participants = party.list_participants(ctx.org_id)
    for p in participants:
        share = party.get_structuring_data(p.id, ctx.org_id)["allocation_share"]
        account = resolve_account(party_id=p.id, org_id=ctx.org_id, currency="GBP")
        post(account, ctx.amount * Decimal(share), "CREDIT")
```

Meridian normalizes billing capability. Define what to charge, how to split revenue, and when to settle — the engine handles the double-entry ledger, audit trails, and payment integration. Start with simple subscriptions; the same infrastructure scales to complex multi-party distributions.

## How It Works

| Layer | Technology | Purpose |
|-------|------------|---------|
| **Business Logic** | Starlark | Sagas, distributions, settlement flows |
| **Validation** | CEL | Account policies, limits, eligibility rules |
| **Valuation** | CEL + Market Data | Pricing for any asset type |
| **Ledger** | Double-entry, bi-temporal | Every balance stored, every change traceable |
| **Payments** | Stripe Connect | Payment rails integration |

### Starlark

Starlark is a deterministic subset of Python. No imports, no filesystem, no network — pure business logic that replays identically on failure.

```python
def contribute(ctx):
    reserve(ctx.from_account, ctx.amount)
    credit(resolve_account(
        party_id=ctx.party_id,
        org_id=ctx.org_id,
        currency=ctx.currency
    ), ctx.amount)
    return {"contributed": str(ctx.amount)}
```

### CEL

CEL expressions guard operations and compute valuations.

```cel
// Account policy: daily limit
transaction.amount <= account.daily_limit - account.daily_spent

// Eligibility check
party.verification_status == "VERIFIED"

// Time-of-use pricing
rate_schedule.lookup(timestamp.hour) * quantity
```

### Bi-Temporal

Every record tracks two timelines:

- **Event time**: When it happened
- **Knowledge time**: When the system recorded it

Estimates supersede to actuals without overwriting history. The audit trail shows what was known at any point in time.

## Use Cases

| Domain | Billing Model | Relevant Features |
|--------|---------------|-------------------|
| **SaaS** | Subscriptions, usage metering | Billing cycles, Stripe integration |
| **Energy** | Time-of-use rates, estimates → actuals | Bi-temporal, quality ladder |
| **Marketplaces** | Revenue splits to sellers | Multi-party distribution sagas |
| **Betting/Gaming** | Pool contributions and payouts | Org-scoped accounts, segregated funds |
| **Carbon** | Exchange-priced assets | Multi-asset ledger, market data |
| **Compute** | Spot-priced resource usage | Usage metering, valuation |

## Compliance

| Requirement | Implementation |
|-------------|----------------|
| **Audit Trail** | Immutable, bi-temporal transaction history |
| **Fund Segregation** | Org-scoped accounts track positions within pools |
| **Identity** | Party service with Stripe Identity integration |
| **Reconciliation** | Automated variance detection and settlement lifecycle |

## Quick Start

```bash
git clone git@github.com:meridianhub/meridian.git
cd meridian
go mod download

ctlptl create cluster kind --registry=ctlptl-registry --name=kind-meridian-local
tilt up
```

- Tilt UI: http://localhost:10350
- API Gateway: http://localhost:8080
- gRPC: localhost:9090

See [CONTRIBUTING.md](CONTRIBUTING.md) for detailed setup.

## Architecture

Follows [BIAN](https://bian.org/) service domain patterns.

| Service | Purpose |
|---------|---------|
| **CurrentAccount** | Customer accounts, transaction orchestration |
| **PositionKeeping** | Pre-ledger transaction log, position tracking |
| **FinancialAccounting** | Double-entry bookkeeping |
| **PaymentOrder** | Saga orchestration, settlement |
| **MarketInformation** | Bi-temporal pricing, quality ladder |
| **Party** | Customer data, associations |
| **Reconciliation** | Variance detection, disputes |
| **ControlPlane** | Manifest management, Stripe billing |

See [docs/adr/](docs/adr/) for architectural decisions.

## Technology

- **Language**: Go
- **API**: Protocol Buffers + gRPC
- **Database**: CockroachDB
- **Events**: Apache Kafka
- **Orchestration**: Kubernetes
- **Payments**: Stripe Connect

## Documentation

- [Architecture Decisions](docs/adr/)
- [API Reference](api/proto/)
- [PRDs](docs/prd/)
- [Contributing](CONTRIBUTING.md)

## License

Business Source License 1.1 — See [LICENSE](LICENSE).

- Use, modify, and deploy internally
- Cannot offer competing Billing/Treasury-as-a-Service
- Converts to Apache 2.0 on January 14, 2030
