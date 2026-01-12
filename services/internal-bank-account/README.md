---
name: internal-bank-account-service
description: BIAN-compliant internal bank account registry for managing counterparty and operational accounts
triggers:
  - Creating or modifying internal bank accounts
  - Counterparty account management
  - Operational account tracking (nostro, vostro, suspense)
  - Multi-asset account support
  - Internal ledger account references
instructions: |
  InternalBankAccount manages non-customer accounts used for internal accounting purposes:
  - Counterparty accounts (nostro/vostro for correspondent banking)
  - Operational accounts (suspense, clearing, settlement)
  - Multi-asset support (fiat, energy, carbon credits)

  Key patterns:
  - Account lifecycle: PENDING → ACTIVE → SUSPENDED → CLOSED
  - Account types: NOSTRO, VOSTRO, SUSPENSE, CLEARING, SETTLEMENT, OPERATIONAL
  - Currency/asset type tracking for multi-asset support
  - Integration with Financial Accounting for ledger posting

  Port: 50057 (gRPC)
---

# InternalBankAccount Service

BIAN-compliant internal bank account registry microservice for managing counterparty and operational accounts.

## Overview

| Attribute | Value |
|-----------|-------|
| **BIAN Domain** | Internal Bank Account |
| **Port** | 50057 (gRPC) |
| **Language** | Go |
| **Database** | PostgreSQL/CockroachDB |
| **Standalone** | Yes (no service dependencies initially) |

## Purpose

The Internal Bank Account service manages accounts that are not customer-facing but are essential
for internal accounting and correspondent banking operations:

- **Nostro Accounts**: "Our account at your bank" - accounts held at correspondent banks
- **Vostro Accounts**: "Your account at our bank" - accounts held by correspondent banks
- **Suspense Accounts**: Temporary holding accounts for unmatched transactions
- **Clearing Accounts**: Used during settlement processes
- **Settlement Accounts**: Final destination for settled transactions
- **Operational Accounts**: General-purpose internal accounts

## gRPC Methods

> Note: Methods will be defined in Task 6 (Define gRPC API contracts)

### Account Operations (Planned)

| Method | HTTP | Purpose |
|--------|------|---------|
| `InitiateInternalBankAccount` | `POST /v1/internal-bank-accounts` | Create new internal account |
| `RetrieveInternalBankAccount` | `GET /v1/internal-bank-accounts/{id}` | Get account details |
| `UpdateInternalBankAccount` | `PUT /v1/internal-bank-accounts/{id}` | Modify account properties |
| `TerminateInternalBankAccount` | `DELETE /v1/internal-bank-accounts/{id}` | Close account |

## Domain Model

```mermaid
classDiagram
    class InternalBankAccount {
        +UUID ID
        +string AccountID
        +string AccountName
        +AccountType Type
        +AccountStatus Status
        +string Currency
        +string AssetType
        +string CorrespondentBankID
        +string Description
        +int64 Version
        +Time CreatedAt
        +Time UpdatedAt
    }

    class AccountType {
        <<enumeration>>
        NOSTRO
        VOSTRO
        SUSPENSE
        CLEARING
        SETTLEMENT
        OPERATIONAL
    }

    class AccountStatus {
        <<enumeration>>
        PENDING
        ACTIVE
        SUSPENDED
        CLOSED
    }

    InternalBankAccount --> AccountType
    InternalBankAccount --> AccountStatus
```

**Field Notes:**

- `AccountID`: Business ID format `IBA-{uuid[:8]}`
- `Currency`: ISO 4217 currency code (e.g., "USD", "EUR") or asset type code
- `AssetType`: Multi-asset support (e.g., "FIAT", "ENERGY_KWH", "CARBON_CREDIT")
- `CorrespondentBankID`: Reference to correspondent bank for nostro/vostro accounts

## Account Lifecycle

```text
PENDING (awaiting approval)
   │
   └──→ ACTIVE (operational)
           │
           ├──→ SUSPENDED (temporarily inactive)
           │        │
           │        └──→ ACTIVE (reactivated)
           │
           └──→ CLOSED (terminated)
```

- **PENDING**: Account created but awaiting approval/activation
- **ACTIVE**: Account is operational and can be used
- **SUSPENDED**: Temporarily disabled, can be reactivated
- **CLOSED**: Terminal state, no further operations allowed

## Service Dependencies

| Service | Port | Purpose |
|---------|------|---------|
| FinancialAccounting | 50052 | Ledger account references (planned) |

Initially standalone; dependencies will be added as integration points are implemented.

## Database Schema

**Schema**: `internal_bank_account`

```mermaid
erDiagram
    internal_bank_accounts {
        uuid id PK
        string account_id UK "IBA-{uuid[:8]}"
        string account_name
        string account_type "NOSTRO|VOSTRO|SUSPENSE|etc"
        string status "PENDING|ACTIVE|SUSPENDED|CLOSED"
        string currency "ISO 4217 or asset code"
        string asset_type "FIAT|ENERGY_KWH|etc"
        string correspondent_bank_id "nullable"
        string description "nullable"
        bigint version
        timestamp created_at
        timestamp updated_at
    }
```

## Configuration

| Variable | Default | Description |
|----------|---------|-------------|
| `LOG_LEVEL` | `info` | Logging level (debug, info, warn, error) |
| `LOG_FORMAT` | `json` | Log format (json, text) |
| `GRPC_PORT` | `50057` | gRPC server port |
| `DATABASE_URL` | - | PostgreSQL connection string |
| `DB_MAX_OPEN_CONNS` | `25` | Max open database connections |
| `DB_MAX_IDLE_CONNS` | `5` | Max idle database connections |
| `OTEL_SERVICE_NAME` | `internal-bank-account-service` | OpenTelemetry service name |

## Key Patterns

### Multi-Asset Support

Internal accounts can hold different asset types beyond traditional fiat currencies:

```go
// Example: Creating an energy trading account
account := &InternalBankAccount{
    AccountName: "Energy Trading Suspense",
    Type:        AccountTypeSuspense,
    Currency:    "KWH",
    AssetType:   "ENERGY_KWH",
}
```

### Correspondent Banking

Nostro and vostro accounts link to correspondent banks:

```go
// Nostro account at correspondent bank
nostro := &InternalBankAccount{
    AccountName:         "USD Nostro at ABC Bank",
    Type:                AccountTypeNostro,
    Currency:            "USD",
    CorrespondentBankID: "abc-bank-uuid",
}
```

## Development

### Building

```bash
# Build binary
go build -o internal-bank-account ./services/internal-bank-account/cmd

# Build Docker image
docker build -t internal-bank-account:latest \
  -f services/internal-bank-account/cmd/Dockerfile .
```

### Running Locally

```bash
# Set required environment variables
export DATABASE_URL="postgres://user:pass@localhost:5432/internal_bank_account"
export LOG_LEVEL=debug

# Run the service
./internal-bank-account
```

### Running Migrations

```bash
# Generate migration from GORM models
atlas migrate diff --env local

# Apply migrations
atlas migrate apply --env local
```

## Related Documentation

- [ADR-0002: Microservices per BIAN Domain](../../docs/adr/0002-microservices-per-bian-domain.md)
- [ADR-0003: Database Schema Migrations](../../docs/adr/0003-database-schema-migrations.md)
- [ADR-0015: Service Directory Structure](../../docs/adr/0015-service-directory-structure.md)
