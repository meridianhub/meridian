---
name: adr-023-balance-delegation-to-position-keeping
description: Delegate balance computation from Current Account to Position Keeping service per BIAN
triggers:
  - Designing balance query patterns
  - Understanding why Current Account doesn't store balance
  - Implementing balance-related features
  - Querying account balances (7 BIAN types)
  - Understanding Position Keeping as balance authority
instructions: |
  Balance is NOT stored in Current Account. All balance queries delegate to Position Keeping
  service via gRPC. Use GetAccountBalance for single type, GetAccountBalances for all 7 types.

  Balance types: OPENING, CLOSING, CURRENT, AVAILABLE, LEDGER, RESERVE, FREE.

  Pattern: positionKeepingClient.GetAccountBalances(ctx, accountID)
---

# 23. Balance Delegation to Position Keeping

Date: 2026-01-08

## Status

Accepted

## Context

Current Account originally stored balance locally in the database with fields `balance`,
`available_balance`, and `balance_updated_at`. This created several problems:

1. **Dual-write complexity**: Every transaction required updating both Position Keeping
   (transaction log) and Current Account (balance). This introduced synchronisation risks.

2. **Consistency challenges**: If Position Keeping succeeded but Current Account failed,
   the balance could become inconsistent with the transaction history.

3. **Single source of truth violation**: Position Keeping already has all the data needed
   to compute balance (the transaction log). Storing balance separately duplicates this
   responsibility.

4. **BIAN alignment**: Per BIAN service domain principles, Position Keeping owns the
   transaction log and is the authoritative source for position data, which includes balance.

## Decision Drivers

* BIAN compliance: Position Keeping is the authoritative domain for position/balance data
* Eliminate dual-write complexity and synchronisation bugs
* Single source of truth for balance computation
* Support for 7 BIAN balance types (not just current/available)
* Simpler Current Account schema (account metadata only)

## Considered Options

1. Keep balance in Current Account with synchronisation
2. Delegate balance computation to Position Keeping (chosen)
3. Event-sourced balance with eventual consistency

## Decision Outcome

Chosen option: "Delegate balance computation to Position Keeping", because it eliminates
dual-write complexity, aligns with BIAN service boundaries, and establishes a single
source of truth for balance data.

### Positive Consequences

* Single source of truth: Position Keeping computes balance from transaction log
* Simplified Current Account: Only stores account metadata, not derived data
* BIAN compliance: Follows BIAN service domain boundaries
* Richer balance types: 7 BIAN balance types (OPENING, CLOSING, CURRENT, AVAILABLE, etc.)
* Eliminated synchronisation bugs: No more dual-write consistency issues
* Cleaner migration: Opening balance support via Position Keeping initialisation

### Negative Consequences

* Additional network hop: Balance queries require gRPC call to Position Keeping
* Current Account depends on Position Keeping availability (mitigated with circuit breaker)
* Potential latency increase for balance-heavy operations

## Pros and Cons of the Options

### Option 1: Keep balance in Current Account with synchronisation

Store balance locally in Current Account and synchronize with Position Keeping.

* Good, because no network hop for balance queries
* Good, because Current Account is self-contained
* Bad, because dual-write complexity introduces synchronisation bugs
* Bad, because violates single source of truth principle
* Bad, because limited to 2 balance types (current, available)
* Bad, because Current Account must understand balance computation logic

### Option 2: Delegate balance computation to Position Keeping (chosen)

Remove balance storage from Current Account. Position Keeping computes balance on-demand
from the transaction log.

* Good, because single source of truth (transaction log = balance)
* Good, because eliminates dual-write synchronisation issues
* Good, because BIAN-compliant service boundaries
* Good, because supports 7 balance types
* Good, because Current Account schema is simpler
* Bad, because requires network call for balance queries
* Bad, because Current Account depends on Position Keeping availability

### Option 3: Event-sourced balance with eventual consistency

Publish balance change events from Position Keeping, consume in Current Account for caching.

* Good, because Current Account has locally cached balance
* Good, because eventually consistent without dual-write
* Bad, because added complexity of event consumption and cache invalidation
* Bad, because balance may be stale (eventual consistency window)
* Bad, because still duplicates balance data

## Implementation Notes

### Database Migration

Balance columns removed in `20260108000001_remove_balance_columns.sql`:

```sql
ALTER TABLE "account" DROP COLUMN IF EXISTS "balance";
ALTER TABLE "account" DROP COLUMN IF EXISTS "available_balance";
ALTER TABLE "account" DROP COLUMN IF EXISTS "balance_updated_at";
```

### Position Keeping Balance APIs

New gRPC methods in Position Keeping:

| Method | Purpose |
|--------|---------|
| `GetAccountBalance` | Query single balance type |
| `GetAccountBalances` | Query all 7 balance types |

### Balance Types

| Type | Description |
|------|-------------|
| `BALANCE_TYPE_OPENING` | Balance at start of accounting period |
| `BALANCE_TYPE_CLOSING` | Balance at end of accounting period |
| `BALANCE_TYPE_CURRENT` | Real-time balance (sum of POSTED transactions) |
| `BALANCE_TYPE_AVAILABLE` | Available for withdrawal (current - holds - reserves) |
| `BALANCE_TYPE_LEDGER` | Book balance |
| `BALANCE_TYPE_RESERVE` | Amount held in reserve |
| `BALANCE_TYPE_FREE` | Unencumbered balance |

### Usage Pattern

```go
// Current Account querying balance from Position Keeping
balances, err := positionKeepingClient.GetAccountBalances(ctx, &pk.GetAccountBalancesRequest{
    AccountId: accountID,
})
if err != nil {
    return err // Circuit breaker handles transient failures
}

// Access specific balance type
for _, b := range balances.Balances {
    if b.BalanceType == pk.BALANCE_TYPE_AVAILABLE {
        availableBalance = b.Amount
    }
}
```

### Resilience Patterns

* Circuit breaker on Position Keeping client (3 retries, exponential backoff)
* Graceful degradation when Position Keeping unavailable
* Optional balance caching for read-heavy workloads (future optimisation)

## Links

* [ADR-0002: Microservices Per BIAN Domain](0002-microservices-per-bian-domain.md)
* [ADR-0019: Resilient Client Patterns](0019-resilient-client-patterns.md)
* [BIAN Service Boundaries](../architecture/bian-service-boundaries.md)
* [Position Keeping README](../../services/position-keeping/README.md)
* [Current Account README](../../services/current-account/README.md)

## Notes

**Future Considerations:**

* If balance query latency becomes a bottleneck, consider materialized balance snapshots
  in Position Keeping (not in Current Account)
* Balance change events from Position Keeping could enable downstream caching
* Opening balance support enables account migration scenarios
