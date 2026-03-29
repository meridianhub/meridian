---
name: internal-account-service
description: BIAN-compliant internal account registry for managing counterparty and operational accounts with multi-asset support
triggers:
  - Creating or modifying internal accounts
  - Counterparty account management (nostro/vostro)
  - Operational account tracking (clearing, suspense, holding)
  - Multi-asset account support (currency, energy, compute, carbon)
  - Internal ledger account references
  - Account lifecycle management (suspend, activate, close)
  - Balance queries for internal accounts
instructions: |
  InternalAccount manages non-customer accounts used for internal accounting purposes:
  - Counterparty accounts (nostro/vostro for counterparty banking)
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

# InternalAccount Service

BIAN-compliant internal account registry microservice for managing counterparty and operational accounts.

## Overview

| Attribute | Value |
|-----------|-------|
| **BIAN Domain** | Internal Account |
| **Port** | 50057 (gRPC) |
| **Language** | Go |
| **Database** | PostgreSQL/CockroachDB |
| **Standalone** | Yes (balance delegated to Position Keeping) |

## Purpose

The Internal Account service manages accounts that are not customer-facing but are essential
for internal accounting and counterparty banking operations:

- **Clearing Accounts**: Settlement and clearing operations during transaction processing
- **Nostro Accounts**: "Our account at your bank" - accounts held at counterparty banks
- **Vostro Accounts**: "Your account at our bank" - accounts held by counterparty banks at us
- **Holding Accounts**: Temporary holding of funds during multi-step processes
- **Suspense Accounts**: Unidentified or pending transactions awaiting resolution
- **Revenue Accounts**: Income and revenue tracking for GL integration
- **Expense Accounts**: Cost and expense tracking for GL integration
- **Inventory Accounts**: Non-cash asset tracking (energy, commodities, compute resources)

## gRPC Methods

### Account Operations

| Method | HTTP | Purpose |
|--------|------|---------|
| `InitiateInternalAccount` | `POST /v1/internal-accounts` | Create new internal account |
| `UpdateInternalAccount` | `PATCH /v1/internal-accounts/{account_id}` | Modify account settings |
| `ControlInternalAccount` | `POST /v1/internal-accounts/{account_id}/control` | Lifecycle transitions |
| `RetrieveInternalAccount` | `GET /v1/internal-accounts/{account_id}` | Get account details |
| `ListInternalAccounts` | `GET /v1/internal-accounts` | List with filters |
| `GetBalance` | `GET /v1/internal-accounts/{account_id}/balance` | Query balance (from Position Keeping) |

### Method Details

#### InitiateInternalAccount

Creates a new internal account in ACTIVE status.

```go
req := &iba.InitiateInternalAccountRequest{
    AccountCode:    "NOSTRO-USD-HSBC",
    Name:           "USD Nostro at HSBC London",
    AccountType:    iba.INTERNAL_ACCOUNT_TYPE_NOSTRO,
    InstrumentCode: "USD",
    CounterpartyDetails: &iba.CounterpartyDetails{
        CounterpartyId:          "hsbc-london",
        CounterpartyName:        "HSBC London",
        CounterpartyExternalRef: "GB12HSBC12345678901234",
        CounterpartyType:        iba.COUNTERPARTY_TYPE_NOSTRO,
    },
    Description:    "Primary USD clearing account at HSBC London",
    IdempotencyKey: &common.IdempotencyKey{Key: "create-nostro-001"},
}

resp, err := client.InitiateInternalAccount(ctx, req)
// resp.AccountId = "2rPxMVkj3tNmqPwT5Wk8Lc4M9xZ"
// resp.Facility contains full account details
```

#### ControlInternalAccount

Performs lifecycle state transitions with audit trail.

```go
req := &iba.ControlInternalAccountRequest{
    AccountId:     "2rPxMVkj3tNmqPwT5Wk8Lc4M9xZ",
    ControlAction: iba.CONTROL_ACTION_SUSPEND,
    Reason:        "Quarterly compliance review - temporary suspension pending audit completion",
}

resp, err := client.ControlInternalAccount(ctx, req)
// resp.Facility.AccountStatus = INTERNAL_ACCOUNT_STATUS_SUSPENDED
// resp.ActionTimestamp = time of state change
```

#### GetBalance

Queries the current balance for an internal account by proxying to the
Position Keeping service, which is the single source of truth for all
balance data.

```go
req := &iba.GetBalanceRequest{
    AccountId: "2rPxMVkj3tNmqPwT5Wk8Lc4M9xZ",
}

resp, err := client.GetBalance(ctx, req)
// resp.CurrentBalance.Amount = "1500000.00"
// resp.CurrentBalance.InstrumentCode = "USD"
// resp.AsOf = timestamp from Position Keeping
```

**Prerequisites:**

- Position Keeping service must be deployed and reachable
- Account must be in `ACTIVE` status (suspended/closed accounts return `FailedPrecondition`)
- A position record must exist in Position Keeping for the account/instrument combination

**Behavior:**

- Returns `BALANCE_TYPE_CURRENT` from the Position Keeping response
- O(1) query: Position Keeping maintains pre-computed running balance totals
- 5-second timeout on the Position Keeping call (`context.WithTimeout`)
- Response includes `as_of` timestamp from Position Keeping (falls back to current time if absent)

**Error Scenarios:**

| gRPC Code | Condition | Description |
|-----------|-----------|-------------|
| `Unimplemented` | Position Keeping client not configured | Service constructed without PK client (e.g., `NewService` instead of `NewServiceWithClients`) |
| `FailedPrecondition` | Account not active | Account is suspended or closed |
| `NotFound` | Account does not exist | Account ID/code not found in local database |
| `Internal` | Position not found in PK | PK returned NotFound (position record missing) |
| `Unavailable` | PK service unreachable | PK unavailable, deadline exceeded, or resource exhausted |
| `Internal` | PK invalid argument | Request to PK was malformed (indicates a code defect) |
| `InvalidArgument` | Empty account_id | Request missing required `account_id` field |

**Monitoring:**

| Metric | Description |
|--------|-------------|
| `internal_account_operation_duration_seconds{operation="get_balance"}` | Total GetBalance RPC duration |
| `internal_account_balance_query_duration_seconds{status}` | Duration of the PK call specifically (target p99 < 50ms) |
| `internal_account_errors_total{category="position_keeping"}` | PK-related error counter |

The health check endpoint (`grpc.health.v1.Health/Check`) reports PK
connectivity as a degraded (not critical) dependency. The service
continues serving non-balance operations even when PK is unreachable.

**Local Development (grpcurl):**

```bash
# Query balance for an account
grpcurl -plaintext -d '{"account_id": "<account-id>"}' \
  localhost:50057 meridian.internal_account.v1.InternalAccountService/GetBalance

# Check health (includes PK connectivity status)
grpcurl -plaintext -d '{"service": "internal-account"}' \
  localhost:50057 grpc.health.v1.Health/Check
```

**Production Deployment:**

| Attribute | Value |
|-----------|-------|
| Service name | `internal-account` |
| gRPC port | `50057` |
| Health endpoint | `grpc.health.v1.Health/Check` (service: `internal-account`) |
| PK dependency | `position-keeping:50053` |

## Domain Model

```mermaid
classDiagram
    class InternalAccountFacility {
        +string AccountID
        +string AccountCode
        +string Name
        +InternalAccountType AccountType
        +ClearingPurpose ClearingPurpose
        +InternalAccountStatus AccountStatus
        +string InstrumentCode
        +CounterpartyDetails CounterpartyDetails
        +string Description
        +int32 Version
        +Timestamp CreatedAt
        +Timestamp UpdatedAt
    }

    class CounterpartyDetails {
        +string CounterpartyID
        +string CounterpartyName
        +string CounterpartyExternalRef
        +map Attributes
        +CounterpartyType CounterpartyType
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

    class CounterpartyType {
        <<enumeration>>
        NOSTRO
        VOSTRO
    }

    InternalAccountFacility --> InternalAccountType
    InternalAccountFacility --> ClearingPurpose
    InternalAccountFacility --> InternalAccountStatus
    InternalAccountFacility --> CounterpartyDetails
    CounterpartyDetails --> CounterpartyType
```

**Field Notes:**

- `AccountID`: System-generated KSUID (e.g., `2rPxMVkj3tNmqPwT5Wk8Lc4M9xZ`)
- `AccountCode`: Business-friendly code (e.g., `NOSTRO-USD-HSBC`, `CLR-001`)
- `InstrumentCode`: References instrument from Reference Data service (e.g., `USD`, `KWH`, `GPU_HOUR`)
- `CounterpartyDetails`: Required for NOSTRO/VOSTRO accounts, null for others

## Account Types

| Type | Description | Typical Use |
|------|-------------|-------------|
| `CLEARING` | Settlement and clearing operations | Interbank settlement, payment clearing |
| `NOSTRO` | Our account at another bank | Foreign currency holdings, counterparty banking |
| `VOSTRO` | Their account at our bank | Counterparty accounts for partner banks |
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
req := &iba.InitiateInternalAccountRequest{
    AccountCode:     "CLR-GBP-DEPOSIT",
    Name:            "GBP Deposit Clearing",
    AccountType:     iba.INTERNAL_ACCOUNT_TYPE_CLEARING,
    ClearingPurpose: iba.CLEARING_PURPOSE_DEPOSIT,
    InstrumentCode:  "GBP",
}

// Withdrawal clearing account
req := &iba.InitiateInternalAccountRequest{
    AccountCode:     "CLR-USD-WITHDRAW",
    Name:            "USD Withdrawal Clearing",
    AccountType:     iba.INTERNAL_ACCOUNT_TYPE_CLEARING,
    ClearingPurpose: iba.CLEARING_PURPOSE_WITHDRAWAL,
    InstrumentCode:  "USD",
}

// Settlement clearing account
req := &iba.InitiateInternalAccountRequest{
    AccountCode:     "CLR-EUR-SETTLE",
    Name:            "EUR Settlement Clearing",
    AccountType:     iba.INTERNAL_ACCOUNT_TYPE_CLEARING,
    ClearingPurpose: iba.CLEARING_PURPOSE_SETTLEMENT,
    InstrumentCode:  "EUR",
}
```

### Querying by Clearing Purpose

Use the `clearing_purpose_filter` in `ListInternalAccounts`:

```bash
# List all deposit clearing accounts
grpcurl -plaintext -d '{
  "account_type_filter": "INTERNAL_ACCOUNT_TYPE_CLEARING",
  "clearing_purpose_filter": "CLEARING_PURPOSE_DEPOSIT"
}' localhost:50057 meridian.internal_account.v1.InternalAccountService/ListInternalAccounts
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
    [*] --> ACTIVE: InitiateInternalAccount
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
req := &iba.InitiateInternalAccountRequest{
    AccountCode:    "INV-ENERGY-GRID1",
    Name:           "Grid 1 Energy Inventory",
    AccountType:    iba.INTERNAL_ACCOUNT_TYPE_INVENTORY,
    InstrumentCode: "KWH",  // Energy in kilowatt-hours
    Description:    "Energy inventory for Grid Region 1 - aggregated generation",
}
```

#### GPU Compute Allocation Account

```go
req := &iba.InitiateInternalAccountRequest{
    AccountCode:    "INV-GPU-POOL-A",
    Name:           "GPU Pool A Allocation",
    AccountType:    iba.INTERNAL_ACCOUNT_TYPE_INVENTORY,
    InstrumentCode: "GPU_HOUR",  // GPU compute hours
    Description:    "Allocated GPU compute for AI training cluster A",
}
```

#### Carbon Credit Suspense Account

```go
req := &iba.InitiateInternalAccountRequest{
    AccountCode:    "SUS-CARBON-UNMATCHED",
    Name:           "Carbon Credit Suspense",
    AccountType:    iba.INTERNAL_ACCOUNT_TYPE_SUSPENSE,
    InstrumentCode: "TONNE_CO2E",  // Carbon credits
    Description:    "Unmatched carbon credits pending verification",
}
```

See [ADR-0035: Multi-Asset Purity](../../docs/adr/0035-multi-asset-purity.md) for the architectural decision.

## Balance Delegation to Position Keeping

**Critical Design Decision**: Internal Account does NOT store balance locally.

```mermaid
sequenceDiagram
    participant Client
    participant IBA as InternalAccount
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

Postings flow through Financial Accounting, not directly through Internal Account:

```mermaid
sequenceDiagram
    participant CA as CurrentAccount
    participant FA as FinancialAccounting
    participant PK as PositionKeeping
    participant IBA as InternalAccount

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
| `counterparty_details` | Required for NOSTRO/VOSTRO types | "counterparty details required for nostro/vostro" |

## Database Schema

**Schema**: `internal_account`

```mermaid
erDiagram
    internal_account {
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

    counterparty_details {
        string counterparty_id "nullable"
        string counterparty_name "nullable"
        string counterparty_external_ref "nullable"
        string counterparty_type "NOSTRO|VOSTRO"
    }

    internal_account ||--o| counterparty_details : "has (nostro/vostro only)"
```

## Service Dependencies

| Service | Port | Purpose |
|---------|------|---------|
| Position Keeping | 50053 | Balance computation (required for GetBalance) |
| Reference Data | 50055 | Instrument validation (validates instrument_code) |
| Financial Accounting | 50052 | Posting entry point (transactions flow through FA) |

```mermaid
graph TD
    IBA[Internal Account<br>:50057] --> PK[Position Keeping<br>:50053]
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
| `OTEL_SERVICE_NAME` | `internal-account-service` | OpenTelemetry service name |

## Key Patterns

### Idempotency

Create operations accept an `idempotency_key` for exactly-once semantics:

```go
req := &iba.InitiateInternalAccountRequest{
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
account, _ := client.RetrieveInternalAccount(ctx, &iba.RetrieveInternalAccountRequest{
    AccountId: "2rPxMVkj3tNmqPwT5Wk8Lc4M9xZ",
})

// Update with expected version
_, err := client.UpdateInternalAccount(ctx, &iba.UpdateInternalAccountRequest{
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
// client.DeleteInternalAccount(...)  // Does not exist!

// CORRECT: Close the account
client.ControlInternalAccount(ctx, &iba.ControlInternalAccountRequest{
    AccountId:     "2rPxMVkj3tNmqPwT5Wk8Lc4M9xZ",
    ControlAction: iba.CONTROL_ACTION_CLOSE,
    Reason:        "Account consolidated into CLR-002 per finance directive FIN-2024-042",
})
```

### Counterparty Banking

Nostro and vostro accounts require counterparty details:

```go
// Nostro: Our account at their bank
nostro := &iba.InitiateInternalAccountRequest{
    AccountCode: "NOSTRO-EUR-DEUTSCHE",
    AccountType: iba.INTERNAL_ACCOUNT_TYPE_NOSTRO,
    InstrumentCode: "EUR",
    CounterpartyDetails: &iba.CounterpartyDetails{
        CounterpartyId:          "deutsche-frankfurt",
        CounterpartyName:        "Deutsche Bank Frankfurt",
        CounterpartyExternalRef: "DE89370400440532013000",
        CounterpartyType:        iba.COUNTERPARTY_TYPE_NOSTRO,
    },
}

// Vostro: Their account at our bank
vostro := &iba.InitiateInternalAccountRequest{
    AccountCode: "VOSTRO-JPY-MUFG",
    AccountType: iba.INTERNAL_ACCOUNT_TYPE_VOSTRO,
    InstrumentCode: "JPY",
    CounterpartyDetails: &iba.CounterpartyDetails{
        CounterpartyId:          "mufg-tokyo",
        CounterpartyName:        "MUFG Bank Tokyo",
        CounterpartyExternalRef: "VOSTRO-MUFG-001",
        CounterpartyType:        iba.COUNTERPARTY_TYPE_VOSTRO,
    },
}
```

## Performance Characteristics

| Operation | Complexity | Notes |
|-----------|------------|-------|
| `InitiateInternalAccount` | O(1) | Single insert with idempotency check |
| `RetrieveInternalAccount` | O(1) | Primary key lookup |
| `UpdateInternalAccount` | O(1) | Single update with version check |
| `ControlInternalAccount` | O(1) | Status update with audit entry |
| `ListInternalAccounts` | O(n) | Filtered query, paginated |
| `GetBalance` | O(m) | Delegated to Position Keeping (m = transactions) |

**Notes:**

- Account operations are fast (local database)
- Balance queries depend on Position Keeping performance
- Consider caching for high-traffic balance queries

## Development

### Building

```bash
# Build binary
go build -o internal-account ./services/internal-account/cmd

# Build Docker image
docker build -t internal-account:latest \
  -f services/internal-account/cmd/Dockerfile .
```

### Running Locally

```bash
# Set required environment variables
export DATABASE_URL="postgres://user:pass@localhost:5432/meridian?search_path=internal_account"
export POSITION_KEEPING_ADDR="localhost:50053"
export REFERENCE_DATA_ADDR="localhost:50055"
export LOG_LEVEL=debug

# Run the service
./internal-account
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
}' localhost:50057 meridian.internal_account.v1.InternalAccountService/InitiateInternalAccount

# List accounts
grpcurl -plaintext localhost:50057 meridian.internal_account.v1.InternalAccountService/ListInternalAccounts

# Get balance
grpcurl -plaintext -d '{"account_id": "2rPxMVkj3tNmqPwT5Wk8Lc4M9xZ"}' \
  localhost:50057 meridian.internal_account.v1.InternalAccountService/GetBalance
```

## Related Documentation

- [ADR-0002: Microservices per BIAN Domain](../../docs/adr/0002-microservices-per-bian-domain.md)
- [ADR-0003: Database Schema Migrations](../../docs/adr/0003-database-schema-migrations.md)
- [ADR-0013: Generic Asset Quantity Types](../../docs/adr/0013-generic-asset-quantity-types.md)
- [ADR-0015: Standard Service Directory Structure](../../docs/adr/0015-standard-service-directory-structure.md)
- [ADR-0023: Balance Delegation to Position Keeping](../../docs/adr/0023-balance-delegation-to-position-keeping.md)
- [ADR-0024: Internal Bank Account Service](../../docs/adr/0024-internal-bank-account-service.md)
- [ADR-0025: Clearing Purpose Specialization](../../docs/adr/0025-clearing-purpose-specialization.md)
- [Proto Definitions](../../api/proto/meridian/internal_account/v1/)
- [Position Keeping Service](../position-keeping/README.md)
- [Reference Data Service](../reference-data/README.md)
