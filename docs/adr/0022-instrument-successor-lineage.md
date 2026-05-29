---
name: adr-022-instrument-successor-lineage
description: Forward lineage tracking for deprecated instruments via successor_id enabling upgrade path discovery
triggers:
  - Deprecating an instrument and pointing clients to its replacement
  - Querying what replaced a deprecated instrument
  - Managing instrument version evolution with breaking changes
  - Migrating positions from old to new instrument versions
  - Trading deprecated instruments for successor instruments
  - Validating ledger entries against instrument status
instructions: |
  When deprecating an instrument, optionally set successor_id to point to the replacement
  instrument. This creates a 1-to-1 forward lineage chain (A -> B -> C). Use recursive
  CTE queries to traverse the chain to find the current active instrument. Successor must
  be ACTIVE and have the same dimension. Write-once semantics: once successor_id is set,
  it cannot be changed.

  MIGRATION RULE: Position migration is modeled as a trade (sell deprecated, buy successor).
  Deprecated instruments allow DEBITS (selling out) but REJECT CREDITS (buying in). This
  enforcement happens in Financial Accounting/Position Keeping, not Reference Data.
---

# 22. Instrument Successor Lineage

Date: 2026-01-05

## Status

Accepted (Implemented)

## Context

Meridian's multi-asset platform tracks diverse financial instruments (currencies, energy,
compute credits, carbon offsets) that evolve over time. The Financial Instrument Reference
Data Management service (ADR-0014) provides tenant-defined instrument definitions with
immutable versions and a lifecycle (DRAFT -> ACTIVE -> DEPRECATED).

### The Evolution Problem

Instruments evolve for various reasons:

| Reason | Example | Impact |
|--------|---------|--------|
| Regulatory changes | New ISO 4217 currency code | Old positions need migration path |
| Precision updates | BTC moving from 8 to 18 decimals | Incompatible with existing entries |
| Schema changes | New required attributes | Old positions cannot be upgraded in-place |
| Rebranding | Corporate merger renames instrument | Business continuity requirement |
| Error correction | Fixing misconfigured validation rules | Cannot edit ACTIVE instruments |

When an instrument transitions to DEPRECATED, existing ledger entries remain immutable -
they reference the old instrument ID forever. However, clients need to know:

1. **What replaces this instrument?** - UI/API consumers need to suggest alternatives
2. **Is this still a valid instrument?** - Prevent new entries using deprecated instruments
3. **How do I migrate positions?** - Operational guidance for moving to the successor

### Why Not Just Version Numbers?

ADR-0014 already provides version numbers (v1, v2, v3...) for schema evolution within
the same instrument code. However, version numbers solve a different problem:

| Scenario | Solution |
|----------|----------|
| Same instrument, backward-compatible changes | Version increment (USD v1 -> v2) |
| Same instrument, breaking changes | New instrument + successor link |
| Different instrument replaces old | New instrument + successor link |
| Instrument split into multiple | Manual intervention (no single successor) |

**Version numbers** handle gradual evolution where old and new can coexist.
**Successor links** handle deprecation where the old instrument should no longer be used.

### Relationship to Other ADRs

This ADR builds on:

- **ADR-0014 (Financial Instrument Reference Data):** Defines the `instrument_definition`
  table, lifecycle states, and CEL validation expressions
- **ADR-0016 (Tenant Isolation):** Ensures successor links respect tenant boundaries
- **ADR-0017 (Temporal Quality Ladder):** Position entries reference instruments by ID;
  those references must remain valid even when instruments are deprecated

## Decision Drivers

* **Forward discovery**: Clients need to find current replacement without scanning all instruments
* **Immutable ledger integrity**: Existing entries cannot be modified to point to new instruments
* **Simple model**: 1-to-1 succession covers 90% of cases; complex scenarios need manual handling
* **Dimension safety**: Prevent invalid successors that would break quantity type safety
* **Write-once semantics**: Once succession is established, it cannot be changed

## Considered Options

### Option 1: Successor ID Column (Self-Referential FK)

Add a `successor_id` column to `instrument_definition` that points to the replacement
instrument. Simple forward pointer, enforced at database level.

### Option 2: Separate Lineage Table

Create a `instrument_lineage` table with `deprecated_id`, `successor_id`, and `reason`.
More flexible but adds join complexity and potential consistency issues.

### Option 3: Bidirectional Links (predecessor + successor)

Track both `predecessor_id` and `successor_id` for full lineage traversal in both
directions.

### Option 4: Version Graph with Edges

Full graph model supporting 1-to-many (splits) and many-to-1 (merges) relationships.

## Decision Outcome

Chosen option: **Option 1 - Successor ID Column**, because:

- Simplest implementation with minimal schema changes
- Database-enforced referential integrity via FK constraint
- Forward traversal (old -> new) is the primary use case
- Complex scenarios (splits, merges) are rare and require manual intervention anyway

### Schema Changes

```sql
-- Add successor_id column (nullable - not all deprecated instruments have successors)
ALTER TABLE "instrument_definition"
  ADD COLUMN "successor_id" uuid NULL;

-- Self-referential foreign key ensures successor exists
ALTER TABLE "instrument_definition"
  ADD CONSTRAINT "fk_instrument_definition_successor"
  FOREIGN KEY ("successor_id") REFERENCES "instrument_definition" ("id");

-- Index for efficient reverse lookups (finding all predecessors of an instrument)
CREATE INDEX "idx_instrument_definition_successor_id" ON "instrument_definition" ("successor_id")
  WHERE "successor_id" IS NOT NULL;
```

### Validation Rules (Trigger-Enforced)

The `enforce_instrument_lifecycle` trigger validates successor relationships:

```sql
-- On transition to DEPRECATED with successor_id:
-- 1. Successor must exist
-- 2. Successor must be ACTIVE (not DRAFT or DEPRECATED)
-- 3. Successor must have same dimension (prevents type mismatch)

IF NEW."successor_id" IS NOT NULL THEN
  SELECT "id", "status", "dimension"
  INTO successor_record
  FROM "instrument_definition"
  WHERE "id" = NEW."successor_id";

  IF successor_record.id IS NULL THEN
    RAISE EXCEPTION 'Successor instrument does not exist: %', NEW."successor_id";
  END IF;

  IF successor_record.status != 'ACTIVE' THEN
    RAISE EXCEPTION 'Successor instrument must be ACTIVE, but is %', successor_record.status;
  END IF;

  IF successor_record.dimension != NEW."dimension" THEN
    RAISE EXCEPTION 'Successor instrument dimension (%) must match current instrument dimension (%)',
      successor_record.dimension, NEW."dimension";
  END IF;
END IF;
```

### Write-Once Semantics

Once `successor_id` is set, it cannot be changed:

```sql
-- Enforce write-once semantics regardless of status
IF OLD."successor_id" IS NOT NULL AND OLD."successor_id" IS DISTINCT FROM NEW."successor_id" THEN
  RAISE EXCEPTION 'Cannot modify successor_id once set (write-once semantics)';
END IF;
```

**Rationale**: Allowing successor changes would break client caching and could create
confusion in audit trails. If the wrong successor was set, create a new version instead.

### Lineage Traversal Query

To find the current active instrument from any point in the lineage chain:

```sql
-- Recursive CTE to traverse lineage chain
WITH RECURSIVE lineage AS (
  -- Start from the deprecated instrument
  SELECT id, code, version, status, successor_id, dimension, 1 as depth
  FROM instrument_definition
  WHERE id = $1  -- Starting instrument ID

  UNION ALL

  -- Follow successor chain
  SELECT i.id, i.code, i.version, i.status, i.successor_id, i.dimension, l.depth + 1
  FROM instrument_definition i
  JOIN lineage l ON i.id = l.successor_id
  WHERE l.depth < 10  -- Prevent infinite loops (defensive)
)
SELECT * FROM lineage
WHERE status = 'ACTIVE'
ORDER BY depth
LIMIT 1;
```

### API Integration

The `DeprecateInstrument` RPC accepts an optional `successor_id`:

```protobuf
message DeprecateInstrumentRequest {
  string code = 1;
  int32 version = 2;

  // Optional: UUID of the replacement instrument (must be ACTIVE, same dimension)
  string successor_id = 3;
}
```

### Domain Model

```go
// InstrumentDefinition includes successor reference for lineage tracking.
type InstrumentDefinition struct {
    ID          uuid.UUID
    Code        string
    Version     int32
    Dimension   Dimension
    Status      InstrumentStatus
    // ... other fields ...

    // SuccessorID points to the replacement instrument when deprecated.
    // Nil if no successor designated or instrument is not deprecated.
    SuccessorID *uuid.UUID
}

// GetCurrentSuccessor traverses the lineage chain to find the active replacement.
// Returns nil if the instrument is active or has no successor chain leading to active.
func (s *InstrumentService) GetCurrentSuccessor(
    ctx context.Context,
    instrumentID uuid.UUID,
) (*InstrumentDefinition, error) {
    // Implementation uses recursive CTE query
}
```

## Consequences

### Positive

* **Simple forward discovery**: Single column lookup to find replacement
* **Database integrity**: FK constraint ensures successor exists
* **Dimension safety**: Trigger prevents cross-dimension successors
* **Audit-friendly**: Write-once semantics create immutable lineage records
* **Query efficient**: Index on successor_id enables fast reverse lookups
* **Minimal schema change**: One nullable column, one FK, one index

### Negative

* **No split handling**: 1-to-many splits require manual intervention
* **No merge handling**: Many-to-1 merges require custom logic
* **Chain traversal cost**: Long chains require recursive queries (mitigated by depth limit)
* **No reason tracking**: Why an instrument was deprecated is not captured in the schema

## Architectural Considerations

### Tenant Isolation

The FK constraint is within the tenant schema (per ADR-0016), so successors must be
instruments within the same tenant. Cross-tenant succession is not possible, which
is the correct behavior for tenant isolation.

### Cache Invalidation

When an instrument is deprecated:

1. Cache entry for the deprecated instrument should be updated (not invalidated)
2. Position Keeping services should handle deprecated instruments gracefully
3. UI clients should show "deprecated, use X instead" based on successor_id

```go
// Example cache invalidation handler
func (c *InstrumentCache) OnInstrumentDeprecated(evt InstrumentDeprecatedEvent) {
    // Update cached entry with deprecated status and successor
    if entry, ok := c.Get(evt.InstrumentID); ok {
        entry.Status = StatusDeprecated
        entry.SuccessorID = evt.SuccessorID
        c.Set(evt.InstrumentID, entry)
    }

    // Optionally pre-warm successor if not cached
    if evt.SuccessorID != nil {
        c.EnsureLoaded(*evt.SuccessorID)
    }
}
```

### Position Keeping Integration

Position entries reference instruments by ID. When querying positions:

```go
// PositionWithLineage includes the current active instrument for deprecated instruments.
type PositionWithLineage struct {
    Position          Position
    Instrument        InstrumentDefinition
    CurrentSuccessor  *InstrumentDefinition  // Non-nil if instrument is deprecated
}

// GetPositionsWithLineage enriches positions with successor information.
func (s *PositionService) GetPositionsWithLineage(
    ctx context.Context,
    accountID uuid.UUID,
) ([]PositionWithLineage, error) {
    // For each position with deprecated instrument, resolve current successor
}
```

### Historical Ledger Entries

Existing ledger entries remain unchanged - they reference the original instrument ID.
This is correct behavior because:

1. **Immutability**: Ledger entries are append-only (ADR-0017)
2. **Audit trail**: Historical entries should reflect what was recorded at the time
3. **Position calculation**: Sum of entries gives position in the original instrument

### Position Migration via Trades

Migration from a deprecated instrument to its successor is modeled as a **trade** -
this keeps the ledger model consistent and leverages the existing Financial Accounting
infrastructure:

```
Deprecated Position          Trade                    New Position
─────────────────────────────────────────────────────────────────────────────
USD_V1: 1,000.00    →    Sell USD_V1 / Buy USD_V2    →    USD_V2: 1,000.00
                              ↑
                    Valuation Engine determines rate
                    (1:1 for same currency, or
                     10:1 for stock split, etc.)
```

**The key rule for deprecated instruments:**

| Operation | Allowed | Rationale |
|-----------|---------|-----------|
| Debit (sell out of) | ✅ Yes | This is how you exit positions in deprecated instruments |
| Credit (buy into) | ❌ No | Prevent new positions in deprecated instruments |

This asymmetric rule means:

1. **No new exposure**: Clients cannot acquire positions in deprecated instruments
2. **Clean exit path**: Existing positions can be sold/converted to the successor
3. **Valuation Engine decides rate**: The conversion factor (1:1, 10:1, etc.) is
   determined by the yet-to-be-defined Valuation Engine, not Reference Data

**Where this is enforced:**

- **Reference Data**: Only manages the `successor_id` pointer - does NOT enforce
  trading rules (it has no knowledge of ledger operations)
- **Financial Accounting / Position Keeping**: Validates instrument status on ledger
  entry creation - rejects credits to DEPRECATED instruments with clear error message
  pointing to the successor

```go
// In Financial Accounting ledger entry validation
func (s *LedgerService) ValidateEntry(ctx context.Context, entry *LedgerEntry) error {
    instrument, err := s.refData.GetDefinition(ctx, entry.InstrumentCode, entry.InstrumentVersion)
    if err != nil {
        return err
    }

    // Allow debits from deprecated instruments (this is the migration path)
    // Reject credits to deprecated instruments (no new positions)
    if instrument.Status == StatusDeprecated && entry.Amount.IsPositive() {
        return &DeprecatedInstrumentError{
            InstrumentID: instrument.ID,
            SuccessorID:  instrument.SuccessorID,
            Message:      "Cannot credit deprecated instrument; use successor instead",
        }
    }

    return nil
}
```

**Future: Valuation Engine ADR**

The Valuation Engine (future ADR/PRD) will provide:

- Conversion rates between deprecated and successor instruments
- Historical rate lookups for audit/reconciliation
- Rate validation rules (e.g., same-dimension instruments must have rate)
- Bulk migration rate schedules for planned deprecations

## Scenarios Not Handled

The following scenarios require manual intervention and cannot be expressed via
simple successor links:

### 1-to-Many Splits

Example: Company stock split (1 share -> 10 shares)

```
OLD_STOCK (DEPRECATED)
    └─> NEW_STOCK_A (ACTIVE)  -- Cannot express with single successor
    └─> NEW_STOCK_B (ACTIVE)
```

**Manual process**: Create transfer entries to move positions proportionally.

### Many-to-1 Merges

Example: Currency union (DEM, FRF, ITL -> EUR)

```
DEM (DEPRECATED) ─┐
FRF (DEPRECATED) ─┼─> EUR (ACTIVE)  -- Each can point to EUR individually
ITL (DEPRECATED) ─┘
```

**This works**: Each old currency can have EUR as its successor.

### Chain Deprecation

Example: Rapid iteration (A -> B -> C in quick succession)

```
A (DEPRECATED) -> B (DEPRECATED) -> C (ACTIVE)
```

**This works**: Recursive CTE traverses to find C as the current active instrument.
However, if B is deprecated before clients update from A, they may briefly see B.

### Circular Lineage Prevention

The trigger validation naturally prevents circular references:

```
Scenario: A → B, then attempt B → A

1. A is ACTIVE
2. Deprecate A with successor B (B must be ACTIVE) ✅
3. A is now DEPRECATED, B is ACTIVE
4. Attempt to deprecate B with successor A
   └─> FAILS: A is DEPRECATED, not ACTIVE ❌
```

The "successor must be ACTIVE" constraint prevents circular chains because an instrument
that is already in the lineage chain (A) would have to be DEPRECATED to be there. The
trigger also explicitly rejects self-referential successors (A → A).

### Chain Depth Limits

The recursive CTE query includes a depth limit of 10 to prevent runaway queries:

```sql
WHERE l.depth < 10  -- Defensive limit
```

**Note**: This limit is enforced on the read side only. There is no write-time
enforcement preventing chains longer than 10. In practice, chains this long indicate
a process problem (too many rapid deprecations) rather than a technical one. If
enforcement is needed, a trigger could be added to validate chain depth before
allowing a new successor to be set.

## Future Considerations

### Deprecation Reason

Consider adding a `deprecation_reason` column for audit purposes:

```sql
ALTER TABLE "instrument_definition"
  ADD COLUMN "deprecation_reason" TEXT;
```

### Bulk Migration API

For large-scale migrations, a bulk transfer API could automate position migration:

```protobuf
rpc MigratePositions(MigratePositionsRequest) returns (MigratePositionsResponse);

message MigratePositionsRequest {
  string from_instrument_id = 1;  // Must be DEPRECATED
  string to_instrument_id = 2;    // Must be ACTIVE, same dimension
  string conversion_factor = 3;   // e.g., "1.0" or "10.0" for stock splits
}
```

### Event Emission

Consider emitting domain events for lineage changes:

```go
type InstrumentDeprecatedEvent struct {
    InstrumentID uuid.UUID
    SuccessorID  *uuid.UUID
    DeprecatedAt time.Time
}
```

## Links

### Internal ADRs

* [ADR-0014: Financial Instrument Reference Data](0014-financial-instrument-reference-data.md) - Instrument definitions and lifecycle
* [ADR-0016: Tenant Isolation](0016-tenant-id-naming-strategy.md) - Schema-per-tenant isolation
* [ADR-0017: Temporal Quality Ladder](0017-temporal-quality-ladder.md) - Position entries and immutability

### External References

* [ISO 20022 Financial Instrument Identification](https://www.iso20022.org) - Standard instrument identifiers
* [BIAN Financial Instrument Reference Data Management](https://bian.org) - Service domain specification
