# Multi-Organisation Settlement Demo

This document describes the multi-organisation (multi-tenancy) demonstration for
Meridian, showcasing complete data isolation between organisations while enabling
cross-organisation settlement via external protocols.

## Overview

Meridian supports multiple organisations running on shared infrastructure with
complete data isolation. This demo provisions four organisations representing
diverse use cases:

| Organisation | Display Name | Settlement Asset | Use Case |
|--------------|--------------|------------------|----------|
| `meridian` | Meridian Control Plane | USD | Tenant Zero - hosts control plane |
| `post_office` | UK Post Office | GBP | Traditional fiat banking |
| `motive` | Motive AI Compute | GPU-HOUR | GPU compute marketplace |
| `un_wfp` | UN World Food Programme | RICE-VOUCHER | Humanitarian aid |

## Architecture

```text
                    ┌─────────────────────────────────────────────────────┐
                    │                    Tilt Cluster                      │
                    │                                                      │
                    │  ┌──────────┐  ┌──────────┐  ┌──────────┐           │
                    │  │ Current  │  │ Position │  │ Financial│           │
                    │  │ Account  │  │ Keeping  │  │ Accounting│          │
                    │  └────┬─────┘  └────┬─────┘  └────┬─────┘           │
                    │       │             │             │                  │
                    │       └─────────────┼─────────────┘                  │
                    │                     │                                │
                    │              ┌──────▼──────┐                         │
                    │              │  CockroachDB │                        │
                    │              │   (shared)   │                        │
                    │              └──────────────┘                        │
                    │                     │                                │
                    │     ┌───────────────┼───────────────┐               │
                    │     │               │               │               │
                    │  ┌──▼───┐      ┌────▼────┐    ┌────▼────┐          │
                    │  │org_  │      │org_     │    │org_     │          │
                    │  │meri- │      │post_    │    │motive   │          │
                    │  │dian  │      │office   │    │         │          │
                    │  └──────┘      └─────────┘    └─────────┘          │
                    │                                                      │
                    └─────────────────────────────────────────────────────┘
```

**Key Points:**

- Each organisation gets a dedicated PostgreSQL schema (`org_<id>`)
- JWT tokens include `x-tenant-id` claim
- `SET LOCAL search_path = org_<id>` enforces isolation at the database level
- No cross-organisation queries are possible

## Prerequisites

Before running the demo:

1. **Local Kubernetes cluster** with Tilt:

   ```bash
   # Create cluster with local registry (one-time)
   ctlptl create cluster kind --registry=ctlptl-registry --name=kind-meridian-local
   ```

2. **Start Tilt**:

   ```bash
   cd meridian-main
   tilt up
   ```

3. **Wait for all services** to be healthy (check Tilt UI at
   <http://localhost:10350>)

## Quick Start

Run the full demo sequence:

```bash
# 1. Provision organisations (creates schemas and registry entries)
./scripts/demo-provision-organisations.sh

# 2. Seed demo accounts and balances
./scripts/demo-seed-data.sh

# 3. Run cross-organisation settlement demo
./scripts/demo-cross-org-settlement.sh

# 4. Validate the environment
./scripts/demo-validation.sh
```

## Demo Scripts

### Organisation Provisioning

`./scripts/demo-provision-organisations.sh`

Provisions the four demo organisations using the `orgctl` CLI:

```bash
# Example manual provisioning
./orgctl register --id=post_office --name="UK Post Office" --settlement-asset=GBP
./orgctl register --id=motive --name="Motive AI Compute" --settlement-asset=GPU-HOUR
./orgctl register --id=un_wfp --name="UN World Food Programme" --settlement-asset=RICE-VOUCHER
./orgctl register --id=meridian --name="Meridian Control Plane" --settlement-asset=USD
```

**What it creates:**

- Organisation registry entries (stored in `org_meridian.organisations` table)
- Database schemas for each organisation (`org_post_office`, `org_motive`, etc.)
- Keycloak client configurations (optional)

### Seed Data

`./scripts/demo-seed-data.sh`

Creates demo accounts and balances:

| Organisation | Accounts | Initial Balance |
|--------------|----------|-----------------|
| `post_office` | 5 customer accounts | GBP 1,000 each |
| `motive` | 3 provider accounts | 100 GPU-hours each |
| `un_wfp` | 10 beneficiary accounts | 1,000 vouchers each |
| `meridian` | 1 treasury account | USD 1,000,000 |

### Cross-Organisation Settlement

`./scripts/demo-cross-org-settlement.sh`

Demonstrates external atomic swap between UN WFP and Motive AI:

**Scenario:** UN WFP purchases 10 GPU-hours from Motive AI to train a crop
yield prediction model, paying with rice vouchers.

**Exchange:**

- UN WFP pays: 500 RICE-VOUCHERS
- Motive delivers: 10 GPU-HOURS
- Exchange rate: 50 vouchers per GPU-hour

**Key demonstration points:**

1. Organizations are fully isolated (no internal gRPC shortcuts)
2. Settlement occurs via external DEX protocol (simulated)
3. Audit trails show "External party" classification
4. Same security model as separate production deployments

### Validation Suite

`./scripts/demo-validation.sh`

Automated validation of the demo environment:

- Service health checks
- Kubernetes pod status
- Organisation provisioning
- Organisation isolation (schema separation)
- Keycloak configuration
- Database schema validation
- Observability stack
- Kafka cluster health
- Tilt environment
- Demo data verification

## Manual Verification

### Verify Organisation Isolation

```bash
# List organisations
./orgctl list

# Try to access Post Office account from Motive context (should fail)
grpcurl -plaintext -H "x-tenant-id:motive" \
  -d '{"account_id": "po-customer-1"}' \
  localhost:50051 meridian.current_account.v1.CurrentAccountService/RetrieveCurrentAccount

# Access account from correct organisation context (should succeed)
grpcurl -plaintext -H "x-tenant-id:post_office" \
  -d '{"account_id": "po-customer-1"}' \
  localhost:50051 meridian.current_account.v1.CurrentAccountService/RetrieveCurrentAccount
```

### Verify Database Schemas

```bash
# Connect to CockroachDB
kubectl exec -it cockroachdb-0 -- ./cockroach sql --insecure

# List organisation schemas
SELECT schema_name FROM information_schema.schemata
WHERE schema_name LIKE 'org_%';

# Check Post Office accounts
SET search_path = org_post_office;
SELECT * FROM accounts LIMIT 5;

# Check Motive accounts (different schema)
SET search_path = org_motive;
SELECT * FROM accounts LIMIT 5;
```

### Check Observability

```bash
# Grafana dashboards (org-scoped metrics)
open http://localhost:3000

# Prometheus queries
open http://localhost:9090

# Query by organisation
# In Prometheus: {tenant="post_office"}
```

## Tenant Zero Pattern

The `meridian` organisation is special - it serves as "Tenant Zero" or the
control plane organisation:

- Hosts the `organisation.organisations` registry table
- Future: Billing and usage tracking for customer organisations
- Demonstrates dogfooding: Meridian runs on Meridian

**Note:** The control plane organisation does NOT participate in customer
settlements - it only provides infrastructure.

## Security Model

### Data Isolation

- **Schema-level isolation**: Each organisation has a dedicated PostgreSQL
  schema
- **Query enforcement**: `SET LOCAL search_path = org_<id>` executed at the
  start of every transaction
- **JWT claims**: `x-tenant-id` claim in access tokens
- **Request validation**: Missing/invalid tenant context routes to DLQ

### Cross-Organisation Access

- **No direct access**: Organizations cannot query each other's data
- **External settlement**: Cross-org transactions use external DEX protocols
- **Audit classification**: Cross-org counterparties shown as "External party"
- **Same security model**: Equivalent to separate cloud deployments

## Troubleshooting

### Services Not Ready

```bash
# Check Tilt UI
open http://localhost:10350

# Check pod status
kubectl get pods

# Check service logs
kubectl logs -l app=current-account --tail=100
```

### Organisation Not Found

```bash
# Verify organisation exists
./orgctl list

# Re-run provisioning (idempotent)
./scripts/demo-provision-organisations.sh
```

### Account Not Found

```bash
# Verify correct organisation context
grpcurl -plaintext -H "x-tenant-id:<correct_org>" \
  -d '{"account_id": "<account_id>"}' \
  localhost:50051 meridian.current_account.v1.CurrentAccountService/RetrieveCurrentAccount

# Re-run seed data (idempotent)
./scripts/demo-seed-data.sh
```

### Keycloak Not Configured

```bash
# Run Keycloak setup
./scripts/keycloak-setup.sh

# Verify realm exists
curl http://localhost:18080/realms/meridian/.well-known/openid-configuration
```

## Next Steps

After running the demo:

1. **Review Grafana dashboards** at <http://localhost:3000> for org-scoped
   metrics
2. **Explore Tempo traces** with `tenant.id` attribute
3. **Run the Horizon Integrity Proof** (`./scripts/demo.sh`) to verify
   idempotency
4. **Scale services** to test load balancing across organisations

## Related Documentation

- [ADR-0006: Tilt Local Development](../adr/0006-tilt-local-development.md)
- [Architecture Overview](ARCHITECTURE.md)
- [Demo Guide](DEMO_GUIDE.md)
