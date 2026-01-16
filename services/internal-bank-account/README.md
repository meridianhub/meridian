---
name: internal-bank-account-service
description: BIAN-compliant internal bank account registry for managing counterparty and operational accounts with multi-asset support
triggers:
  - Creating or modifying internal bank accounts
  - Counterparty account management (nostro/vostro)
  - Operational account tracking (clearing, suspense, holding)
  - Multi-asset account support (currency, energy, compute, carbon)
  - Internal ledger account references
  - Account lifecycle management (suspend, activate, close)
  - Balance queries for internal accounts
instructions: |
  InternalBankAccount manages non-customer accounts used for internal accounting purposes:
  - Counterparty accounts (nostro/vostro for correspondent banking)
  - Operational accounts (clearing, suspense, holding, revenue, expense)
  - Multi-asset support (fiat, energy, carbon credits, compute hours)

  Key patterns:
  - Account lifecycle: ACTIVE -> SUSPENDED -> CLOSED (no PENDING state)
  - Account types: CLEARING, NOSTRO, VOSTRO, HOLDING, SUSPENSE, REVENUE, EXPENSE, INVENTORY
  - Balance is NOT stored locally - delegated to Position Keeping service
  - No DELETE operations - accounts managed through status transitions
  - No RecordPosting RPC - postings flow through Financial Accounting -> Position Keeping

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
| **Standalone** | Yes (balance delegated to Position Keeping) |

## Purpose

The Internal Bank Account service manages accounts that are not customer-facing but are essential
for internal accounting and correspondent banking operations:

- **Clearing Accounts**: Settlement and clearing operations during transaction processing
- **Nostro Accounts**: "Our account at your bank" - accounts held at correspondent banks
- **Vostro Accounts**: "Your account at our bank" - accounts held by correspondent banks at us
- **Holding Accounts**: Temporary holding of funds during multi-step processes
- **Suspense Accounts**: Unidentified or pending transactions awaiting resolution
- **Revenue Accounts**: Income and revenue tracking for GL integration
- **Expense Accounts**: Cost and expense tracking for GL integration
- **Inventory Accounts**: Non-cash asset tracking (energy, commodities, compute resources)

## gRPC Methods

### Account Operations

| Method | HTTP | Purpose |
|--------|------|---------|
| `InitiateInternalBankAccount` | `POST /v1/internal-bank-accounts` | Create new internal account |
| `UpdateInternalBankAccount` | `PATCH /v1/internal-bank-accounts/{account_id}` | Modify account settings |
| `ControlInternalBankAccount` | `POST /v1/internal-bank-accounts/{account_id}/control` | Lifecycle transitions |
| `RetrieveInternalBankAccount` | `GET /v1/internal-bank-accounts/{account_id}` | Get account details |
| `ListInternalBankAccounts` | `GET /v1/internal-bank-accounts` | List with filters |
| `GetBalance` | `GET /v1/internal-bank-accounts/{account_id}/balance` | Query balance (from Position Keeping) |

### Method Details

#### InitiateInternalBankAccount

Creates a new internal bank account in ACTIVE status.

```go
req := &iba.InitiateInternalBankAccountRequest{
    AccountCode:    "NOSTRO-USD-HSBC",
    Name:           "USD Nostro at HSBC London",
    AccountType:    iba.INTERNAL_ACCOUNT_TYPE_NOSTRO,
    InstrumentCode: "USD",
    CorrespondentDetails: &iba.CorrespondentBankDetails{
        BankId:             "hsbc-london",
        BankName:           "HSBC London",
        ExternalAccountRef: "GB12HSBC12345678901234",
        SwiftCode:          "HSBCGB2L",
        CorrespondentType:  iba.CORRESPONDENT_TYPE_NOSTRO,
    },
    Description:    "Primary USD clearing account at HSBC London",
    IdempotencyKey: &common.IdempotencyKey{Key: "create-nostro-001"},
}

resp, err := client.InitiateInternalBankAccount(ctx, req)
// resp.AccountId = "2rPxMVkj3tNmqPwT5Wk8Lc4M9xZ"
// resp.Facility contains full account details
```

#### ControlInternalBankAccount

Performs lifecycle state transitions with audit trail.

```go
req := &iba.ControlInternalBankAccountRequest{
    AccountId:     "2rPxMVkj3tNmqPwT5Wk8Lc4M9xZ",
    ControlAction: iba.CONTROL_ACTION_SUSPEND,
    Reason:        "Quarterly compliance review - temporary suspension pending audit completion",
}

resp, err := client.ControlInternalBankAccount(ctx, req)
// resp.Facility.AccountStatus = INTERNAL_ACCOUNT_STATUS_SUSPENDED
// resp.ActionTimestamp = time of state change
```

#### GetBalance

Queries account balance from Position Keeping service.

```go
req := &iba.GetBalanceRequest{
    AccountId: "2rPxMVkj3tNmqPwT5Wk8Lc4M9xZ",
}

resp, err := client.GetBalance(ctx, req)
// resp.CurrentBalance.Amount = "1500000.00"
// resp.CurrentBalance.InstrumentCode = "USD"
// resp.AsOf = timestamp from Position Keeping
```

## Domain Model

```mermaid
classDiagram
    class InternalBankAccountFacility {
        +string AccountID
        +string AccountCode
        +string Name
        +InternalAccountType AccountType
        +ClearingPurpose ClearingPurpose
        +InternalAccountStatus AccountStatus
        +string InstrumentCode
        +CorrespondentBankDetails CorrespondentDetails
        +string Description
        +int32 Version
        +Timestamp CreatedAt
        +Timestamp UpdatedAt
    }

    class CorrespondentBankDetails {
        +string BankID
        +string BankName
        +string ExternalAccountRef
        +string SwiftCode
        +CorrespondentType CorrespondentType
    }

    class InternalAccountType {
        <<enumeration>>
        CLEARING
        NOSTRO
        VOSTRO
        HOLDING
        SUSPENSE
        REVENUE
        EXPENSE
        INVENTORY
    }

    class ClearingPurpose {
        <<enumeration>>
        UNSPECIFIED
        DEPOSIT
        WITHDRAWAL
        SETTLEMENT
        GENERAL
    }

    class InternalAccountStatus {
        <<enumeration>>
        ACTIVE
        SUSPENDED
        CLOSED
    }

    class ControlAction {
        <<enumeration>>
        SUSPEND
        ACTIVATE
        CLOSE
    }

    class CorrespondentType {
        <<enumeration>>
        NOSTRO
        VOSTRO
    }

    InternalBankAccountFacility --> InternalAccountType
    InternalBankAccountFacility --> ClearingPurpose
    InternalBankAccountFacility --> InternalAccountStatus
    InternalBankAccountFacility --> CorrespondentBankDetails
    CorrespondentBankDetails --> CorrespondentType
```

**Field Notes:**

- `AccountID`: System-generated KSUID (e.g., `2rPxMVkj3tNmqPwT5Wk8Lc4M9xZ`)
- `AccountCode`: Business-friendly code (e.g., `NOSTRO-USD-HSBC`, `CLR-001`)
- `InstrumentCode`: References instrument from Reference Data service (e.g., `USD`, `KWH`, `GPU_HOUR`)
- `CorrespondentDetails`: Required for NOSTRO/VOSTRO accounts, null for others

## Account Types

| Type | Description | Typical Use |
|------|-------------|-------------|
| `CLEARING` | Settlement and clearing operations | Interbank settlement, payment clearing |
| `NOSTRO` | Our account at another bank | Foreign currency holdings, correspondent banking |
| `VOSTRO` | Their account at our bank | Correspondent accounts for partner banks |
| `HOLDING` | Temporary fund holding | Escrow, pending transfers, batch processing |
| `SUSPENSE` | Unmatched/pending transactions | Reconciliation, error correction |
| `REVENUE` | Income tracking | Fee collection, interest income |
| `EXPENSE` | Cost tracking | Operating expenses, fee payments |
| `INVENTORY` | Non-cash assets | Energy inventory, carbon credits, compute allocation |

## Clearing Account Purposes

Clearing accounts can be specialized for specific purposes using the `clearing_purpose` field:

| Purpose | Enum Value | Use Case |
|---------|------------|----------|
| Deposit | `CLEARING_PURPOSE_DEPOSIT` | Clearing accounts for deposit operations |
| Withdrawal | `CLEARING_PURPOSE_WITHDRAWAL` | Clearing accounts for withdrawal operations |
| Settlement | `CLEARING_PURPOSE_SETTLEMENT` | Clearing accounts for settlement operations |
| General | `CLEARING_PURPOSE_GENERAL` | General-purpose clearing accounts |

**Important**: The `clearing_purpose` field is only applicable when `account_type` is
`INTERNAL_ACCOUNT_TYPE_CLEARING`. For all other account types, the value must be
`CLEARING_PURPOSE_UNSPECIFIED`.

### Example: Creating Purpose-Specific Clearing Accounts

```go
// Deposit clearing account
req := &iba.InitiateInternalBankAccountRequest{
    AccountCode:     "CLR-GBP-DEPOSIT",
    Name:            "GBP Deposit Clearing",
    AccountType:     iba.INTERNAL_ACCOUNT_TYPE_CLEARING,
    ClearingPurpose: iba.CLEARING_PURPOSE_DEPOSIT,
    InstrumentCode:  "GBP",
}

// Withdrawal clearing account
req := &iba.InitiateInternalBankAccountRequest{
    AccountCode:     "CLR-USD-WITHDRAW",
    Name:            "USD Withdrawal Clearing",
    AccountType:     iba.INTERNAL_ACCOUNT_TYPE_CLEARING,
    ClearingPurpose: iba.CLEARING_PURPOSE_WITHDRAWAL,
    InstrumentCode:  "USD",
}

// Settlement clearing account
req := &iba.InitiateInternalBankAccountRequest{
    AccountCode:     "CLR-EUR-SETTLE",
    Name:            "EUR Settlement Clearing",
    AccountType:     iba.INTERNAL_ACCOUNT_TYPE_CLEARING,
    ClearingPurpose: iba.CLEARING_PURPOSE_SETTLEMENT,
    InstrumentCode:  "EUR",
}
```

### Querying by Clearing Purpose

Use the `clearing_purpose_filter` in `ListInternalBankAccounts`:

```bash
# List all deposit clearing accounts
grpcurl -plaintext -d '{
  "account_type_filter": "INTERNAL_ACCOUNT_TYPE_CLEARING",
  "clearing_purpose_filter": "CLEARING_PURPOSE_DEPOSIT"
}' localhost:50057 meridian.internal_bank_account.v1.InternalBankAccountService/ListInternalBankAccounts
```

### Default Clearing Accounts

Each tenant is provisioned with purpose-specific clearing accounts per instrument:

| Account Code | Purpose | Instrument | Usage |
|--------------|---------|------------|-------|
| `CLR-GBP-DEPOSIT` | DEPOSIT | GBP | Customer deposits |
| `CLR-GBP-WITHDRAW` | WITHDRAWAL | GBP | Customer withdrawals |
| `CLR-USD-DEPOSIT` | DEPOSIT | USD | Customer deposits |
| `CLR-USD-WITHDRAW` | WITHDRAWAL | USD | Customer withdrawals |
| `CLR-EUR-DEPOSIT` | DEPOSIT | EUR | Customer deposits |
| `CLR-EUR-WITHDRAW` | WITHDRAWAL | EUR | Customer withdrawals |

## Account Lifecycle State Machine

```mermaid
stateDiagram-v2
    [*] --> ACTIVE: InitiateInternalBankAccount
    ACTIVE --> SUSPENDED: SUSPEND action
    SUSPENDED --> ACTIVE: ACTIVATE action
    ACTIVE --> CLOSED: CLOSE action
    SUSPENDED --> CLOSED: CLOSE action
    CLOSED --> [*]
```

### State Transitions

| From | To | Action | Description |
|------|-----|--------|-------------|
| - | ACTIVE | Create | New accounts start as ACTIVE |
| ACTIVE | SUSPENDED | SUSPEND | Temporary freeze, reversible |
| SUSPENDED | ACTIVE | ACTIVATE | Resume normal operations |
| ACTIVE | CLOSED | CLOSE | Permanent closure (terminal) |
| SUSPENDED | CLOSED | CLOSE | Close suspended account (terminal) |

### State Descriptions

- **ACTIVE**: Account is operational and can participate in transactions
- **SUSPENDED**: Temporarily frozen; cannot process new transactions but maintains audit trail
- **CLOSED**: Terminal state; no further operations allowed (immutable)

## Multi-Asset Support

Internal accounts support the full range of Meridian instrument dimensions through the Reference Data service:

### Instrument Dimensions

| Dimension | Examples | Use Case |
|-----------|----------|----------|
| `CURRENCY` | USD, EUR, GBP, BTC | Traditional fiat and crypto currencies |
| `ENERGY` | KWH, MWH, THERM | Energy trading, utility accounts |
| `MASS` | KG, TON, LB | Commodity inventory |
| `VOLUME` | LITRE, GALLON, BARREL | Liquid commodities (oil, gas) |
| `TIME` | HOUR, DAY | Time-based billing, subscriptions |
| `COMPUTE` | GPU_HOUR, CPU_SECOND | Cloud compute resource allocation |
| `CARBON` | TONNE_CO2E | Carbon credits, emissions tracking |
| `DATA` | GB, TB | Data transfer, storage allocation |
| `COUNT` | UNIT, TOKEN, VOUCHER | Digital assets, vouchers, countable items |

### Multi-Asset Examples

#### Energy Inventory Account (kWh)

```go
req := &iba.InitiateInternalBankAccountRequest{
    AccountCode:    "INV-ENERGY-GRID1",
    Name:           "Grid 1 Energy Inventory",
    AccountType:    iba.INTERNAL_ACCOUNT_TYPE_INVENTORY,
    InstrumentCode: "KWH",  // Energy in kilowatt-hours
    Description:    "Energy inventory for Grid Region 1 - aggregated generation",
}
```

#### GPU Compute Allocation Account

```go
req := &iba.InitiateInternalBankAccountRequest{
    AccountCode:    "INV-GPU-POOL-A",
    Name:           "GPU Pool A Allocation",
    AccountType:    iba.INTERNAL_ACCOUNT_TYPE_INVENTORY,
    InstrumentCode: "GPU_HOUR",  // GPU compute hours
    Description:    "Allocated GPU compute for AI training cluster A",
}
```

#### Carbon Credit Suspense Account

```go
req := &iba.InitiateInternalBankAccountRequest{
    AccountCode:    "SUS-CARBON-UNMATCHED",
    Name:           "Carbon Credit Suspense",
    AccountType:    iba.INTERNAL_ACCOUNT_TYPE_SUSPENSE,
    InstrumentCode: "TONNE_CO2E",  // Carbon credits
    Description:    "Unmatched carbon credits pending verification",
}
```

## Balance Delegation to Position Keeping

**Critical Design Decision**: Internal Bank Account does NOT store balance locally.

```mermaid
sequenceDiagram
    participant Client
    participant IBA as InternalBankAccount
    participant PK as PositionKeeping
    participant DB as PositionKeeping DB

    Client->>IBA: GetBalance(account_id)
    IBA->>PK: GetAccountBalance(account_id)
    PK->>DB: Query POSTED transactions
    DB-->>PK: Transaction entries
    PK->>PK: Compute balance
    PK-->>IBA: BalanceResponse
    IBA-->>Client: GetBalanceResponse
```

### Why Position Keeping Owns Balance

1. **Single Source of Truth**: Position Keeping maintains the immutable transaction log
2. **Balance Types**: Computes all 7 BIAN balance types (Opening, Closing, Current, Available, Ledger, Reserve, Free)
3. **Audit Trail**: Balance derived from auditable transaction history
4. **Consistency**: Same balance computation logic for all account types (customer and internal)

### Transaction Flow

Postings flow through Financial Accounting, not directly through Internal Bank Account:

```mermaid
sequenceDiagram
    participant CA as CurrentAccount
    participant FA as FinancialAccounting
    participant PK as PositionKeeping
    participant IBA as InternalBankAccount

    Note over CA,IBA: Example: Customer Deposit
    CA->>FA: RecordPosting(customer_credit, internal_debit)
    FA->>PK: InitiateFinancialPositionLog (both legs)
    PK-->>FA: Position logs created
    FA-->>CA: Posting confirmed

    Note over CA,IBA: Later: Balance Query
    IBA->>PK: GetAccountBalance(internal_account_id)
    PK-->>IBA: Computed balance from transaction log
```

## Error Codes

| gRPC Code | Condition | Recovery |
|-----------|-----------|----------|
| `NOT_FOUND` | Account ID does not exist | Verify account ID, check tenant context |
| `ALREADY_EXISTS` | Duplicate account_code | Use different code or retrieve existing |
| `INVALID_ARGUMENT` | Validation failed (missing fields, invalid patterns) | Check request against proto validation rules |
| `FAILED_PRECONDITION` | Invalid state transition (e.g., close already closed) | Check account status before operation |
| `ABORTED` | Optimistic lock conflict (version mismatch) | Re-read account, retry with current version |
| `PERMISSION_DENIED` | Insufficient permissions for operation | Check auth token and tenant access |
| `UNAVAILABLE` | Position Keeping unavailable (for balance queries) | Retry with backoff |

### Validation Errors

| Field | Validation | Error |
|-------|------------|-------|
| `account_code` | Pattern: `^[A-Z0-9_-]+$`, max 50 chars | "account_code must match pattern" |
| `name` | Required, max 255 chars | "name is required" |
| `account_type` | Must not be UNSPECIFIED | "account_type must be specified" |
| `instrument_code` | Pattern: `^[A-Z][A-Z0-9_]*$`, max 32 chars | "instrument_code must match pattern" |
| `control_action.reason` | Min 10 chars for audit completeness | "reason must be at least 10 characters" |
| `correspondent_details` | Required for NOSTRO/VOSTRO types | "correspondent_details required for nostro/vostro" |

## Database Schema

**Schema**: `internal_bank_account`

```mermaid
erDiagram
    internal_bank_accounts {
        uuid id PK "KSUID"
        string account_code UK "Business code"
        string name "Display name"
        string account_type "CLEARING|NOSTRO|VOSTRO|..."
        string status "ACTIVE|SUSPENDED|CLOSED"
        string instrument_code "USD|KWH|GPU_HOUR|..."
        string description "nullable"
        bigint version "Optimistic lock"
        timestamp created_at
        timestamp updated_at
    }

    correspondent_bank_details {
        uuid id PK
        uuid internal_bank_account_id FK
        string bank_id
        string bank_name
        string external_account_ref
        string swift_code "nullable"
        string correspondent_type "NOSTRO|VOSTRO"
    }

    internal_bank_accounts ||--o| correspondent_bank_details : "has (nostro/vostro only)"
```

## Service Dependencies

| Service | Port | Purpose |
|---------|------|---------|
| Position Keeping | 50053 | Balance computation (required for GetBalance) |
| Reference Data | 50055 | Instrument validation (validates instrument_code) |
| Financial Accounting | 50052 | Posting entry point (transactions flow through FA) |

```mermaid
graph TD
    IBA[Internal Bank Account<br>:50057] --> PK[Position Keeping<br>:50053]
    IBA --> RD[Reference Data<br>:50055]
    FA[Financial Accounting<br>:50052] --> PK
    FA --> IBA

    subgraph "Balance Computation"
        PK
    end

    subgraph "Reference Data"
        RD
    end

    subgraph "Transaction Processing"
        FA
    end
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
| `POSITION_KEEPING_ADDR` | `position-keeping:50053` | Position Keeping service address |
| `REFERENCE_DATA_ADDR` | `reference-data:50055` | Reference Data service address |
| `OTEL_SERVICE_NAME` | `internal-bank-account-service` | OpenTelemetry service name |

## Key Patterns

### Idempotency

Create operations accept an `idempotency_key` for exactly-once semantics:

```go
req := &iba.InitiateInternalBankAccountRequest{
    // ... fields ...
    IdempotencyKey: &common.IdempotencyKey{
        Key: "create-nostro-hsbc-usd-20240115",  // Client-generated unique key
    },
}
```

If the same idempotency key is reused:

- Within TTL: Returns the original response (effectively-once)
- After TTL: Creates a new account (key expired)

### Optimistic Locking

Update operations use version-based optimistic locking:

```go
// Read current state
account, _ := client.RetrieveInternalBankAccount(ctx, &iba.RetrieveInternalBankAccountRequest{
    AccountId: "2rPxMVkj3tNmqPwT5Wk8Lc4M9xZ",
})

// Update with expected version
_, err := client.UpdateInternalBankAccount(ctx, &iba.UpdateInternalBankAccountRequest{
    AccountId:       "2rPxMVkj3tNmqPwT5Wk8Lc4M9xZ",
    Name:            "Updated Account Name",
    ExpectedVersion: account.Facility.Version,  // Must match current
})

if status.Code(err) == codes.Aborted {
    // Version conflict - re-read and retry
}
```

### No DELETE Operations

Accounts are never deleted. Lifecycle is managed through status transitions:

```go
// WRONG: No delete endpoint exists
// client.DeleteInternalBankAccount(...)  // Does not exist!

// CORRECT: Close the account
client.ControlInternalBankAccount(ctx, &iba.ControlInternalBankAccountRequest{
    AccountId:     "2rPxMVkj3tNmqPwT5Wk8Lc4M9xZ",
    ControlAction: iba.CONTROL_ACTION_CLOSE,
    Reason:        "Account consolidated into CLR-002 per finance directive FIN-2024-042",
})
```

### Correspondent Banking

Nostro and vostro accounts require correspondent bank details:

```go
// Nostro: Our account at their bank
nostro := &iba.InitiateInternalBankAccountRequest{
    AccountCode: "NOSTRO-EUR-DEUTSCHE",
    AccountType: iba.INTERNAL_ACCOUNT_TYPE_NOSTRO,
    InstrumentCode: "EUR",
    CorrespondentDetails: &iba.CorrespondentBankDetails{
        BankId:             "deutsche-frankfurt",
        BankName:           "Deutsche Bank Frankfurt",
        ExternalAccountRef: "DE89370400440532013000",
        SwiftCode:          "DEUTDEFF",
        CorrespondentType:  iba.CORRESPONDENT_TYPE_NOSTRO,
    },
}

// Vostro: Their account at our bank
vostro := &iba.InitiateInternalBankAccountRequest{
    AccountCode: "VOSTRO-JPY-MUFG",
    AccountType: iba.INTERNAL_ACCOUNT_TYPE_VOSTRO,
    InstrumentCode: "JPY",
    CorrespondentDetails: &iba.CorrespondentBankDetails{
        BankId:             "mufg-tokyo",
        BankName:           "MUFG Bank Tokyo",
        ExternalAccountRef: "VOSTRO-MUFG-001",
        SwiftCode:          "BOTKJPJT",
        CorrespondentType:  iba.CORRESPONDENT_TYPE_VOSTRO,
    },
}
```

## Performance Characteristics

| Operation | Complexity | Notes |
|-----------|------------|-------|
| `InitiateInternalBankAccount` | O(1) | Single insert with idempotency check |
| `RetrieveInternalBankAccount` | O(1) | Primary key lookup |
| `UpdateInternalBankAccount` | O(1) | Single update with version check |
| `ControlInternalBankAccount` | O(1) | Status update with audit entry |
| `ListInternalBankAccounts` | O(n) | Filtered query, paginated |
| `GetBalance` | O(m) | Delegated to Position Keeping (m = transactions) |

**Notes:**

- Account operations are fast (local database)
- Balance queries depend on Position Keeping performance
- Consider caching for high-traffic balance queries

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
export DATABASE_URL="postgres://user:pass@localhost:5432/meridian?search_path=internal_bank_account"
export POSITION_KEEPING_ADDR="localhost:50053"
export REFERENCE_DATA_ADDR="localhost:50055"
export LOG_LEVEL=debug

# Run the service
./internal-bank-account
```

### Running Migrations

```bash
# Generate migration from schema changes
atlas migrate diff --env local

# Apply migrations
atlas migrate apply --env local
```

### Testing with grpcurl

```bash
# Create an account
grpcurl -plaintext -d '{
  "account_code": "CLR-TEST-001",
  "name": "Test Clearing Account",
  "account_type": "INTERNAL_ACCOUNT_TYPE_CLEARING",
  "instrument_code": "GBP",
  "description": "Test clearing account for development"
}' localhost:50057 meridian.internal_bank_account.v1.InternalBankAccountService/InitiateInternalBankAccount

# List accounts
grpcurl -plaintext localhost:50057 meridian.internal_bank_account.v1.InternalBankAccountService/ListInternalBankAccounts

# Get balance
grpcurl -plaintext -d '{"account_id": "2rPxMVkj3tNmqPwT5Wk8Lc4M9xZ"}' \
  localhost:50057 meridian.internal_bank_account.v1.InternalBankAccountService/GetBalance
```

## Related Documentation

- [ADR-0002: Microservices per BIAN Domain](../../docs/adr/0002-microservices-per-bian-domain.md)
- [ADR-0003: Database Schema Migrations](../../docs/adr/0003-database-schema-migrations.md)
- [ADR-0013: Multi-Asset Instrument Support](../../docs/adr/0013-multi-asset-instrument-support.md)
- [ADR-0015: Service Directory Structure](../../docs/adr/0015-service-directory-structure.md)
- [ADR-0023: Balance Delegation to Position Keeping](../../docs/adr/0023-balance-delegation-to-position-keeping.md)
- [ADR-0024: Internal Bank Account Service](../../docs/adr/0024-internal-bank-account-service.md)
- [ADR-0025: Clearing Purpose Specialization](../../docs/adr/0025-clearing-purpose-specialization.md)
- [Proto Definitions](../../api/proto/meridian/internal_bank_account/v1/)
- [Position Keeping Service](../position-keeping/README.md)
- [Reference Data Service](../reference-data/README.md)
