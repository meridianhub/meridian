# Meridian

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

You write business logic in [Starlark](https://github.com/google/starlark-go)
(a deterministic Python subset — no imports, no filesystem, no network).
Meridian runs it on a double-entry ledger with automatic compensation on failure.

```python
deposit_saga = saga(name="current_account_deposit")

def execute_deposit():
    amount = Decimal(input_data["amount"])
    account_id = input_data["account_id"]

    step(name="log_position")
    position_keeping.initiate_log(
        position_id=account_id,
        amount=amount,
        currency=input_data["currency"],
        direction="CREDIT",
        transaction_id=input_data["transaction_id"],
    )

    step(name="post_to_ledger")
    financial_accounting.capture_posting(
        account_id=account_id,
        amount=amount,
        currency=input_data["currency"],
        direction="CREDIT",
        transaction_id=input_data["transaction_id"],
    )

execute_deposit()
```

When you need revenue splits across participants, the same engine handles it:

```python
def execute_distribution():
    total = Decimal(input_data["total_amount"])

    step(name="list_participants")
    participants = party.list_participants(org_id=input_data["org_id"])

    for p in participants:
        share = Decimal(str(p["metadata"]["allocation_share"]))
        account_ref = build_org_account_ref(
            party_id=p["party_id"],
            org_id=input_data["org_id"],
            currency="GBP",
        )
        account_id = resolve_account(reference=account_ref)

        step(name="credit_" + p["party_id"])
        position_keeping.initiate_log(
            position_id=account_id,
            amount=total * share,
            currency="GBP",
            direction="CREDIT",
            transaction_id=input_data["transaction_id"],
        )
```

Validation and pricing rules use [CEL](https://cel.dev/) expressions:

```cel
// Enforce daily spending limit
transaction.amount <= account.daily_limit - account.daily_spent

// Time-of-use pricing
rate_schedule.lookup(timestamp.hour) * quantity
```

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

## Architecture

Built on [BIAN](https://bian.org/) banking service domain patterns.

| Service | Purpose |
|---------|---------|
| **CurrentAccount** | Customer accounts, transaction orchestration |
| **PositionKeeping** | Pre-ledger transaction log, position tracking |
| **FinancialAccounting** | Double-entry bookkeeping |
| **PaymentOrder** | Saga orchestration, settlement, Stripe integration |
| **MarketInformation** | Pricing data with quality ladder (estimate / actual / verified) |
| **Party** | Customer data, identity verification |
| **Reconciliation** | Variance detection, dispute management |
| **ControlPlane** | Tenant management, billing configuration |

See [docs/adr/](docs/adr/) for architectural decisions.

## Technology

Go | Protocol Buffers + gRPC | CockroachDB | Apache Kafka | Kubernetes | Stripe Connect

## License

Business Source License 1.1 — See [LICENSE](LICENSE).

- Use, modify, and deploy for any internal purpose
- Cannot offer a competing Billing/Treasury-as-a-Service
- Converts to Apache 2.0 on February 12, 2030
