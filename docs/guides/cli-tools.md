# CLI Tools Reference

Meridian ships several CLI tools for operational tasks built from `cmd/`.
Most connect to services via gRPC, while `position-tool` connects directly
to the database and `meridian-cli` runs offline.

## Overview

| Tool | Purpose | Connects to |
|------|---------|-------------|
| `tenantctl` | Tenant lifecycle management | TenantService (gRPC) |
| `ibactl` | Internal account provisioning | InternalAccountService + TenantService (gRPC) |
| `instrument-cli` | CEL expression dry-run / simulation | ReferenceDataService (gRPC) |
| `position-tool` | Bulk position import/export/rebucket | Database (direct) |
| `market-data-tool` | Bulk market data CSV import | MarketInformationService (gRPC) |
| `meridian-cli` | Saga script validation | None (offline) |

## Building

```bash
# Build a specific tool
go build -o <tool-name> ./cmd/<tool-name>

# Examples
go build -o tenantctl ./cmd/tenantctl
go build -o ibactl ./cmd/ibactl
go build -o instrument-cli ./cmd/instrument-cli
go build -o position-tool ./cmd/position-tool
```

## tenantctl

Manage tenant lifecycle: register, get, list, deprovision, status.

```bash
# Register a new tenant
tenantctl register --id=acme_bank --name="Acme Bank" --settlement-asset=GBP

# List all tenants
tenantctl list

# Check tenant status
tenantctl status acme_bank

# Deprovision (requires --confirm)
tenantctl deprovision acme_bank --confirm
```

**Flags**: `--service-url` (default: `localhost:50056`), `--timeout`

See also: [Production Deployment Runbook](../runbooks/production-deployment.md)

## ibactl

Provision default internal accounts (clearing, nostro, vostro, holding, suspense,
revenue, expense, inventory) for tenants using configurable template sets.

```bash
# List available template sets
ibactl provision-defaults --list-templates

# Provision defaults for a single tenant
ibactl provision-defaults acme_bank

# Use energy template set
ibactl provision-defaults energy_co --template-set=energy

# Provision for all active tenants
ibactl provision-defaults --all

# Dry run
ibactl provision-defaults acme_bank --dry-run
```

**Template sets**: `default` (standard banking), `energy` (energy trading),
`compute` (cloud/AI billing), `minimal` (suspense only)

**Flags**: `--service-url`, `--tenant-service-url` (for `--all`),
`--template-set`, `--dry-run`, `--continue-on-error`, `--max-concurrent`,
`--timeout`

## instrument-cli

Simulate instrument transactions offline. Fetches an instrument definition via gRPC,
then locally evaluates CEL expressions for validation, bucket key generation, and
error messages. No data is written.

```bash
# Basic simulation
instrument-cli simulate --tenant=acme_bank --instrument=USD --amount=100.00

# With attributes for non-fungible instruments
instrument-cli simulate --tenant=acme_bank --instrument=CARBON_CREDIT \
  --amount=50.00 --attr=vintage_year=2024 --attr=registry=VERRA

# With validity period
instrument-cli simulate --tenant=acme_bank --instrument=VOUCHER \
  --amount=10 --valid-from=2024-01-01T00:00:00Z --valid-to=2024-12-31T23:59:59Z

# JSON output for scripting
instrument-cli simulate --tenant=acme_bank --instrument=USD --amount=100 --json

# Local development (insecure connection)
instrument-cli simulate --insecure --tenant=acme_bank --instrument=USD --amount=100
```

**Use cases**:

- Test CEL expressions before activating instruments
- Debug validation failures in production
- Understand how attributes map to bucket IDs
- Preview position records before creation

**Flags**: `--service-url`, `--insecure`, `--timeout`, `--json`, `--version` (instrument version, 0 = latest)

## position-tool

Bulk position management with direct database access. Supports import, export, and rebucketing.

```bash
# Import positions from CSV
position-tool import --tenant=acme_bank --source=positions.csv --db-url=$DATABASE_URL

# Dry-run import (validate only)
position-tool import --tenant=acme_bank --source=positions.csv --dry-run --db-url=$DATABASE_URL

# Export positions for a specific instrument
position-tool export --tenant=acme_bank --instrument=USD --output=export.csv --db-url=$DATABASE_URL

# Rebucket positions after instrument definition change
position-tool rebucket --tenant=acme_bank --instrument=CARBON_CREDIT --db-url=$DATABASE_URL
```

**Features**: checkpoint/resume for interrupted imports, progress reporting
with ETA, graceful shutdown via SIGINT/SIGTERM, configurable batch sizes.

**Flags**: `--tenant` (required), `--db-url` (required, or `DATABASE_URL` env), `--dry-run`, `--log-level`

**Note**: This tool connects directly to the database, not via gRPC. Use with care in production.

## market-data-tool

Bulk market data CSV import with validation and checkpoint support.

```bash
# Import observations from CSV
market-data-tool import --tenant=acme_bank --dataset=FX_RATES --source=rates.csv

# Validate CSV without importing
market-data-tool validate --tenant=acme_bank --dataset=FX_RATES --source=rates.csv

# Query expected CSV schema for a dataset
market-data-tool schema --tenant=acme_bank --dataset=FX_RATES
```

See also: [ADR-0026: Canonical Ingestion Contract](../adr/0026-canonical-ingestion-contract.md)

## meridian-cli

Offline Starlark saga script validation. Loads a script, generates mock handlers
from the embedded `handlers.yaml`, and runs it in a sandboxed Starlark runtime.

```bash
# Validate a saga script
meridian-cli saga validate path/to/saga.star
```

**Use case**: Pre-commit validation of saga scripts without a live platform.

See also: [Saga Validation Guide](saga-validation.md), [Starlark Style Guide](starlark-style-guide.md)
