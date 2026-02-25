---
name: adr-024-internal-account-service
description: Dedicated BIAN service for managing internal non-customer-facing accounts
triggers:
  - Designing internal account management
  - Implementing clearing, nostro, vostro, or suspense accounts
  - Replacing hardcoded counterparty account environment variables
  - Querying internal account registry
  - Understanding multi-asset internal accounts
instructions: |
  Use Internal Account service as the registry for non-customer-facing accounts
  (CLEARING, NOSTRO, VOSTRO, HOLDING, SUSPENSE, REVENUE, EXPENSE, INVENTORY).

  Balance is NOT stored here - delegate to Position Keeping (ADR-0023).
  Account types support multi-asset: CURRENCY, ENERGY, COMPUTE, CARBON.
  Lifecycle: ACTIVE -> SUSPENDED -> CLOSED (no PENDING state).
---

# 24. Internal Account Service Domain

Date: 2026-01-15

## Status

Accepted

## Context

Meridian's transaction processing requires internal accounts to serve as counterparties for
customer transactions. For example, when a customer deposits funds, the bank's CLEARING account
receives the debit. Currently, these internal accounts are hardcoded as environment variables:

```yaml
# Current approach - environment variables in Kubernetes deployments
env:
  - name: CLEARING_ACCOUNT_ID
    value: "00000000-0000-0000-0000-000000000001"
  - name: NOSTRO_USD_ACCOUNT_ID
    value: "00000000-0000-0000-0000-000000000002"
  - name: SUSPENSE_ACCOUNT_ID
    value: "00000000-0000-0000-0000-000000000003"
```

This approach has several problems:

1. **No lifecycle management**: Accounts cannot be suspended, audited, or closed
2. **Configuration drift**: Environment variables differ across environments
3. **No multi-tenancy**: Single static IDs cannot serve multiple tenants
4. **Limited metadata**: Cannot store account descriptions, owners, or audit trails
5. **Single-asset assumption**: No support for energy, compute, or carbon accounts
6. **No discoverability**: Services cannot query for available internal accounts

Additionally, Meridian's vision as a multi-asset transaction engine requires internal accounts
that can hold not just currency, but also energy (kWh), compute (GPU-hours), carbon credits,
and other asset classes.

## Decision Drivers

* **Multi-asset support**: Internal accounts must support CURRENCY, ENERGY, COMPUTE, CARBON,
  and future asset classes
* **O(1) balance queries**: Balance lookups must not scan transaction logs (delegation to
  Position Keeping provides this via materialized positions)
* **Account lifecycle management**: Ability to suspend accounts during audits, close accounts
  when no longer needed
* **Elimination of environment variables**: Dynamic registry replaces static configuration
* **BIAN compliance**: Alignment with BIAN Internal Account service domain
* **Multi-tenancy**: Schema-per-tenant isolation for internal accounts

## Considered Options

1. Environment variables (status quo)
2. Shared reference data service
3. Dedicated Internal Account service (BIAN-aligned)

## Decision Outcome

Chosen option: "Dedicated Internal Account service", because it aligns with BIAN service
domain patterns, provides proper lifecycle management, supports multi-asset accounts, and
eliminates configuration drift from environment variables.

### Positive Consequences

* **Multi-asset registry**: Single service manages internal accounts for all asset classes
* **Lifecycle management**: ACTIVE -> SUSPENDED -> CLOSED transitions with audit trail
* **Multi-tenancy**: Schema-per-tenant isolation (consistent with other Meridian services)
* **Discoverable**: Services query the registry instead of reading environment variables
* **BIAN alignment**: Follows BIAN Internal Account service domain patterns
* **Balance delegation**: Clean separation - registry owns metadata, Position Keeping owns
  balances (per ADR-0023)

### Negative Consequences

* **Additional service**: Increases operational complexity (one more service to deploy)
* **Dependency**: Transaction services depend on Internal Account availability
* **Migration effort**: Existing environment variable usage must migrate to registry queries

## Pros and Cons of the Options

### Option 1: Environment Variables (Status Quo)

Continue hardcoding internal account IDs as environment variables.

* Good, because no new service to deploy
* Good, because simple to understand
* Bad, because no lifecycle management (cannot suspend/close accounts)
* Bad, because configuration drift across environments
* Bad, because no multi-tenancy support
* Bad, because cannot add metadata or audit trails
* Bad, because single-asset assumption (no ENERGY, COMPUTE, CARBON)
* Bad, because not discoverable by other services

### Option 2: Shared Reference Data Service

Create a general-purpose reference data service for all static configuration.

* Good, because single service for all reference data
* Good, because reduces service count
* Bad, because violates BIAN service domain boundaries
* Bad, because internal accounts have different access patterns than other reference data
* Bad, because lifecycle management differs from static reference data
* Bad, because harder to reason about ownership and responsibilities

### Option 3: Dedicated Internal Account Service (Chosen)

Create BIAN-aligned Internal Account service as a multi-asset account registry.

* Good, because BIAN alignment (follows service domain patterns)
* Good, because proper lifecycle management (ACTIVE, SUSPENDED, CLOSED)
* Good, because multi-asset support from day one
* Good, because multi-tenancy via schema-per-tenant
* Good, because clear ownership of internal account metadata
* Good, because balance delegation to Position Keeping (no dual-write)
* Bad, because additional service to deploy and monitor
* Bad, because dependency for transaction processing services

## Implementation Notes

### Account Types

| Type | Description | Example Use Case |
|------|-------------|------------------|
| `CLEARING` | Settlement and clearing operations | Customer deposit counterparty |
| `NOSTRO` | "Our" account at another bank | Correspondent banking |
| `VOSTRO` | "Their" account at our bank | Correspondent banking |
| `HOLDING` | Temporary holding for in-flight transactions | Payment processing |
| `SUSPENSE` | Unidentified or disputed transactions | Exception handling |
| `REVENUE` | Income recognition | Fee collection |
| `EXPENSE` | Expense recognition | Operational costs |
| `INVENTORY` | Asset inventory tracking | Multi-asset holdings |

### Asset Classes

| Asset Class | Unit | Example |
|-------------|------|---------|
| `CURRENCY` | ISO 4217 code | USD, EUR, GBP |
| `ENERGY` | kWh | Renewable energy credits |
| `COMPUTE` | GPU-hours | Cloud compute allocation |
| `CARBON` | tonnes CO2e | Carbon credits |

### Account Lifecycle

```text
     +--------+
     | ACTIVE |
     +----+---+
          |
          | suspend()
          v
   +------+------+
   | SUSPENDED   |
   +------+------+
          |
          | close() or reactivate()
          v
  +-------+--------+
  | CLOSED | ACTIVE |
  +--------+--------+
```

* **ACTIVE**: Account is operational and can participate in transactions
* **SUSPENDED**: Account is temporarily disabled (audit, investigation)
* **CLOSED**: Account is permanently closed (cannot be reopened)

**Note**: There is no PENDING state. Accounts are created directly in ACTIVE status
because internal accounts are created by authorized operations, not customer requests.

### Balance Delegation

Balance is NOT stored in Internal Account service. Following ADR-0023 (Balance Delegation
to Position Keeping), all balance queries delegate to Position Keeping:

```go
// Internal Account stores metadata only
type InternalAccount struct {
    ID          string
    TenantID    string
    AccountType AccountType
    AssetClass  AssetClass
    AssetCode   string      // e.g., "USD", "kWh", "GPU-HOURS"
    Name        string
    Description string
    Status      AccountStatus
    CreatedAt   time.Time
    UpdatedAt   time.Time
    // NO balance fields - delegated to Position Keeping
}

// Balance queries delegate to Position Keeping
func (s *Service) GetAccountBalance(ctx context.Context, accountID string) (*Balance, error) {
    return s.positionKeepingClient.GetAccountBalances(ctx, &pk.GetAccountBalancesRequest{
        AccountId: accountID,
    })
}
```

### Database Schema

Schema-per-tenant with `internal_account` table:

```sql
CREATE TABLE internal_account (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    account_type    VARCHAR(20) NOT NULL,  -- CLEARING, NOSTRO, VOSTRO, etc.
    asset_class     VARCHAR(20) NOT NULL,  -- CURRENCY, ENERGY, COMPUTE, CARBON
    asset_code      VARCHAR(20) NOT NULL,  -- USD, kWh, GPU-HOURS, CO2e
    name            VARCHAR(255) NOT NULL,
    description     TEXT,
    status          VARCHAR(20) NOT NULL DEFAULT 'ACTIVE',
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),

    CONSTRAINT valid_status CHECK (status IN ('ACTIVE', 'SUSPENDED', 'CLOSED'))
);

CREATE INDEX idx_internal_account_type ON internal_account(account_type);
CREATE INDEX idx_internal_account_asset ON internal_account(asset_class, asset_code);
CREATE INDEX idx_internal_account_status ON internal_account(status);
```

### Registry Pattern

Other services query the registry to discover internal accounts:

```go
// Query internal accounts by type and asset
clearingAccounts, err := internalAccountClient.ListAccounts(ctx, &iba.ListAccountsRequest{
    AccountType: iba.ACCOUNT_TYPE_CLEARING,
    AssetClass:  iba.ASSET_CLASS_CURRENCY,
    AssetCode:   "USD",
    Status:      iba.ACCOUNT_STATUS_ACTIVE,
})

// Use first matching account as counterparty
if len(clearingAccounts.Accounts) == 0 {
    return fmt.Errorf("no active USD clearing account found")
}
counterpartyAccountID := clearingAccounts.Accounts[0].Id
```

### gRPC Service Interface

```protobuf
service InternalAccountService {
    // Create a new internal account
    rpc CreateAccount(CreateAccountRequest) returns (Account);

    // Retrieve account by ID
    rpc GetAccount(GetAccountRequest) returns (Account);

    // List accounts with optional filters
    rpc ListAccounts(ListAccountsRequest) returns (ListAccountsResponse);

    // Update account metadata
    rpc UpdateAccount(UpdateAccountRequest) returns (Account);

    // Suspend an account (ACTIVE -> SUSPENDED)
    rpc SuspendAccount(SuspendAccountRequest) returns (Account);

    // Reactivate a suspended account (SUSPENDED -> ACTIVE)
    rpc ReactivateAccount(ReactivateAccountRequest) returns (Account);

    // Close an account permanently (ACTIVE/SUSPENDED -> CLOSED)
    rpc CloseAccount(CloseAccountRequest) returns (Account);

    // Get account balance (delegates to Position Keeping)
    rpc GetAccountBalance(GetAccountBalanceRequest) returns (GetAccountBalanceResponse);
}
```

## Links

* [ADR-0002: Microservices Per BIAN Domain](0002-microservices-per-bian-domain.md)
* [ADR-0013: Generic Asset Quantity Types](0013-generic-asset-quantity-types.md)
* [ADR-0023: Balance Delegation to Position Keeping](0023-balance-delegation-to-position-keeping.md)
* [BIAN Internal Account Service Domain](https://bian.org/semantic-apis/internal-account/)

## Notes

### Migration from Environment Variables

Existing services using environment variables for internal account IDs will migrate to
registry queries:

1. Create internal accounts in registry via migration script
2. Update services to query registry at startup (with caching)
3. Remove environment variable configuration
4. Add circuit breaker for registry unavailability

### Future Considerations

* **Account hierarchies**: Support for parent-child account relationships
* **Account limits**: Maximum balance, transaction limits per account type
* **Regulatory reporting**: Export internal account data for regulatory submissions
* **Multi-currency accounts**: Single internal account holding multiple currencies
