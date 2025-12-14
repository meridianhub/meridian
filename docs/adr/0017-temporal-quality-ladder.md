---
name: adr-017-temporal-quality-ladder
description: Time-bound quality ladder pattern for temporal asset reconciliation with supersession and wash/reload corrections
triggers:
  - Implementing metered asset tracking (energy, compute, bandwidth)
  - Handling out-of-order data arrival with quality-based precedence
  - Building reconciliation between settled and actual values
  - Designing settlement systems with multiple data quality tiers
instructions: |
  Use the Time-Bound Quality Ladder pattern when tracking assets where the "true" value for a time
  period is not known immediately and may be revised as higher-quality data arrives. Model all
  positions as [Start, End] ranges where Start may equal End for point-in-time events. Implement
  supersession via the Delta Engine, corrections via Wash & Reload saga, and reconciliation via
  Settlement Snapshot comparison.
---

# 17. Time-Bound Quality Ladder for Temporal Asset Reconciliation

Date: 2025-12-14

## Status

Proposed

## Context

Meridian must support assets where the "true" value for a time period is not known immediately
and may be revised over time as higher-quality data becomes available. This pattern appears
across multiple domains:

| Domain | Low Quality | Medium Quality | High Quality |
|--------|-------------|----------------|--------------|
| **Energy** | Profile estimate | Customer read | Smart meter actual |
| **Advertising** | Impression count | Click reconciliation | Audit-verified |
| **Aid/NGO** | Estimated delivery | Field report | Verified receipt |
| **Carbon** | Provisional certificate | Registry confirmed | Auditor validated |

### The Core Problem

For any specific **Time Window** (e.g., 12:00–12:30), there may be multiple conflicting "truths":

1. **Estimate (Low Quality):** 10 kWh (Received T+1 hour)
2. **Customer Read (Medium Quality):** 11 kWh (Received T+1 day)
3. **Smart Meter (High Quality):** 10.8 kWh (Received T+3 days)

**The Rule:** The Ledger must store *all* of them (for audit), but the **Financial Position**
must only reflect the *highest quality* available at the current moment.

### Real-World Settlement Patterns

Energy industry settlement runs demonstrate this pattern:

| Run | Timing | Data Quality | Finality |
|-----|--------|--------------|----------|
| D+1 | Day after | Estimates, some actuals | Provisional |
| D+5 | 5 days after | Most actuals | Provisional |
| M+3 | 3 months after | Validated actuals | Near-final |
| M+14 | 14 months after | Fully reconciled | **Final** |

After final settlement, corrections become disputes rather than automatic adjustments.

### Relationship to ADR-0013 and ADR-0014

This ADR builds on:

- **ADR-0013 (Universal Quantity Type System):** Provides `Quantity[D]` with dimensional safety
- **ADR-0014 (Dynamic Asset Registry):** Provides tenant-defined assets with attribute schemas

This ADR adds:

- **Quality-based precedence** for competing measurements
- **Temporal modeling** with [Start, End] periods
- **Supersession tracking** for audit trails
- **Wash & Reload** correction pattern
- **Settlement snapshots** for reconciliation

## Decision Drivers

* **Immutable audit trail**: All data received must be preserved, corrections via append not update
* **Financial accuracy**: Settlements and positions must reflect best available data
* **Regulatory compliance**: Settlement run deadlines and finality rules
* **Reconciliation capability**: Variance detection between settled and actual
* **Universal applicability**: Same pattern for energy, advertising, aid, carbon
* **100k TPS throughput**: Must support high-frequency metering without lock contention
* **No cross-schema queries**: Services cannot join across tenant schemas

## Considered Options

### Option 1: Event Sourcing with Projection

Store measurements as events, project current positions into read models.

**Rejected because:**
- Adds complexity (event store + projector + read model)
- Snapshot management for long histories (14 months of half-hourly = 24k events/meter)
- Project rules prefer simpler append-only tables over event sourcing infrastructure

### Option 2: Bi-Temporal Tables (Valid Time + Transaction Time)

Track both when data was true (valid time) and when we learned it (transaction time).

**Rejected because:**
- Over-engineering for this use case—we only need supersession, not time-travel queries
- PostgreSQL temporal tables (SQL:2011) have limited tooling
- `superseded_by` pointer achieves the audit goal more simply

### Option 3: Separate Measurement vs Position Tables

Measurements in one table, aggregated positions in another (materialized).

**Partially adopted:**
- Measurements table is the source of truth (adopted)
- Position Entries table tracks movements for financial reporting (adopted)
- Avoided: separate "current position" table (adds sync complexity)

### Option 4: Mutable Positions with Audit Log

Allow position updates, log changes separately.

**Rejected because:**
- Violates immutability rule
- Audit log can drift from actual state
- Harder to reason about during disputes

## Decision Outcome

Implement the **Time-Bound Quality Ladder** pattern with five components:

1. **Universal Time Model**: All positions as [Start, End] ranges
2. **Source Authority Registry**: Quality rankings for data sources
3. **Measurement Log**: Append-only record of all inputs
4. **Delta Engine**: Supersession evaluation logic
5. **Correction Saga**: Wash & Reload for financial adjustments

### 1. Universal Time Model

**Every position is a time range where Start may equal End.**

This unifies point-in-time events (transactions) with period-based measurements (metering):

| Scenario | Start | End | Interpretation |
|----------|-------|-----|----------------|
| Bank transaction | 14:35:22 | 14:35:22 | Instant event |
| Energy period | 12:00:00 | 12:30:00 | 30-minute consumption |
| Voucher validity | 2025-01-01 | 2025-12-31 | Year-long validity |
| Compute usage | 09:00:00 | 09:47:23 | Actual duration |

```go
// Period represents a time range. For point-in-time events, Start equals End.
type Period struct {
    Start time.Time
    End   time.Time
}

func Instant(t time.Time) Period {
    return Period{Start: t, End: t}
}

func (p Period) IsInstant() bool {
    return p.Start.Equal(p.End)
}

func (p Period) Duration() time.Duration {
    return p.End.Sub(p.Start)
}

// Overlaps returns true if this period shares any time with another.
// Uses half-open interval semantics [Start, End) for ranged periods.
// For instant events (Start == End), overlap requires containment by the other period.
func (p Period) Overlaps(other Period) bool {
    // Handle instant events: [t, t] overlaps [a, b) if a <= t < b
    if p.IsInstant() {
        return !p.Start.Before(other.Start) && p.Start.Before(other.End)
    }
    if other.IsInstant() {
        return !other.Start.Before(p.Start) && other.Start.Before(p.End)
    }
    // Standard half-open interval overlap
    return p.Start.Before(other.End) && other.Start.Before(p.End)
}

// Contains returns true if the given instant falls within this period.
// Uses closed interval semantics [Start, End] for point containment.
func (p Period) Contains(t time.Time) bool {
    return !t.Before(p.Start) && !t.After(p.End)
}

func (p Period) Validate() error {
    if p.End.Before(p.Start) {
        return errors.New("period end cannot be before start")
    }
    return nil
}
```

**Database representation using PostgreSQL range types:**

```sql
CREATE TABLE measurements (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    account_id UUID NOT NULL,        -- References current_accounts.id
    asset_code VARCHAR(32) NOT NULL, -- References asset_definitions.code
    quantity DECIMAL(38, 18) NOT NULL,

    -- Time as range (point-in-time has start = end)
    period TSTZRANGE NOT NULL,

    -- Attributes for fungibility
    attributes JSONB NOT NULL DEFAULT '{}',

    -- Quality ladder
    source VARCHAR(50) NOT NULL,
    quality_score INTEGER NOT NULL,

    -- Lifecycle
    received_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    superseded_by UUID REFERENCES measurements(id),
    settlement_run VARCHAR(20),
    locked_at TIMESTAMPTZ,

    -- Prevent overlapping current positions for same account/asset/attributes
    -- See "Overlap Prevention" section below for enforcement details
    CONSTRAINT no_overlapping_current CHECK (
        superseded_by IS NOT NULL OR TRUE  -- Placeholder; enforced via application logic
    )
);

-- Overlap prevention is enforced at the application layer using SERIALIZABLE
-- isolation or explicit row locking. PostgreSQL exclusion constraints cannot
-- handle the multi-column key (account_id, asset_code, attributes) with JSONB.
--
-- See the Overlap Prevention section in Implementation Notes for details.

CREATE INDEX idx_measurements_lookup
    ON measurements(account_id, asset_code, period)
    WHERE superseded_by IS NULL;
```

### 2. Source Authority Registry

Quality scores are derived from a Source Authority Registry, not stored on each measurement.
This ensures consistency and allows ranking changes without touching historical data.

```go
// SourceAuthority defines the quality ranking for a data source.
// Stored in the Asset Directory service.
type SourceAuthority struct {
    Code         string    // "SMETS2_METER", "PROFILE_ESTIMATE"
    AssetCode    string    // Source rankings can be asset-specific
    QualityScore int       // Higher = more authoritative (0-100)
    Description  string
    ValidFrom    time.Time
    ValidTo      *time.Time // Null = currently valid
}
```

**Default energy hierarchy:**

| Source Code | Quality Score | Description |
|-------------|---------------|-------------|
| `DEFAULT_PROFILE` | 10 | Regulatory default when no data |
| `ESTIMATED_HISTORIC` | 20 | Same period last year |
| `ESTIMATED_PROFILE` | 30 | Profile coefficient calculation |
| `CUSTOMER_READ` | 50 | Customer-submitted reading |
| `ACTUAL_UNVALIDATED` | 70 | Meter reading, not yet validated |
| `ACTUAL_VALIDATED` | 90 | Meter reading, passed validation |
| `ACTUAL_FINAL` | 100 | Final settlement reading |

**Lookup at measurement ingestion:**

```go
func (r *SourceAuthorityRegistry) GetQualityScore(
    ctx context.Context,
    assetCode string,
    sourceCode string,
    asOf time.Time,
) (int, error) {
    var authority SourceAuthority
    err := r.db.Where(
        "asset_code = ? AND code = ? AND valid_from <= ? AND (valid_to IS NULL OR valid_to > ?)",
        assetCode, sourceCode, asOf, asOf,
    ).First(&authority).Error

    if err != nil {
        return 0, fmt.Errorf("unknown source %s for asset %s: %w", sourceCode, assetCode, err)
    }
    return authority.QualityScore, nil
}
```

### 3. Measurement Log

The Measurement Log is an append-only record in Position Keeping. It stores everything
received, regardless of quality. Nothing is deleted or updated (except supersession pointers).

```go
// Measurement represents a single data point received for a position.
// Immutable after creation except for SupersededBy pointer.
type Measurement struct {
    ID            uuid.UUID
    AccountID     uuid.UUID   // References Current Account
    AssetCode     string      // References Asset Directory
    Quantity      decimal.Decimal

    // Temporal
    Period        Period    // [Start, End], Start may equal End

    // Fungibility attributes (from ADR-0014)
    Attributes    map[string]string

    // Quality ladder
    Source        string    // Lookup key for quality score
    QualityScore  int       // Denormalized at ingestion for query performance

    // Lifecycle
    ReceivedAt    time.Time
    SupersededBy  *uuid.UUID  // Points to replacement measurement

    // Settlement
    SettlementRun string      // "D+1", "D+5", "M+14", "FINAL"
    LockedAt      *time.Time  // Non-null = cannot be superseded
}

// IsCurrent returns true if this measurement has not been superseded.
func (m Measurement) IsCurrent() bool {
    return m.SupersededBy == nil
}

// IsLocked returns true if this measurement cannot be superseded.
func (m Measurement) IsLocked() bool {
    return m.LockedAt != nil
}
```

### 4. Delta Engine

The Delta Engine evaluates incoming measurements and determines the appropriate action.

```go
type Action int

const (
    ActionBookNew Action = iota      // No existing data, book this measurement
    ActionArchiveOnly                // Lower quality, keep for audit only
    ActionWashAndReload              // Higher quality, trigger correction
    ActionIgnoreDuplicate            // Same source, same value, idempotent skip
    ActionCreateDispute              // Position is locked, route to dispute workflow
)

// DeltaEngine evaluates incoming measurements against existing positions.
type DeltaEngine struct {
    repo MeasurementRepository
}

// Evaluate determines what action to take for an incoming measurement.
func (e *DeltaEngine) Evaluate(ctx context.Context, incoming Measurement) (Action, *Measurement, error) {
    // Find existing current measurement for this position key
    existing, err := e.repo.FindCurrent(ctx,
        incoming.AccountID,
        incoming.AssetCode,
        incoming.Period,
        incoming.Attributes,
    )
    if err != nil && !errors.Is(err, ErrNotFound) {
        return 0, nil, err
    }

    // Case D: No existing measurement - book as new
    if existing == nil {
        return ActionBookNew, nil, nil
    }

    // Case: Position is locked (final settlement)
    if existing.IsLocked() {
        return ActionCreateDispute, existing, nil
    }

    // Case E: Duplicate detection (same source, same value)
    if incoming.Source == existing.Source &&
       incoming.Quantity.Equal(existing.Quantity) {
        return ActionIgnoreDuplicate, existing, nil
    }

    // Case A: New data is lower quality - archive only
    if incoming.QualityScore < existing.QualityScore {
        return ActionArchiveOnly, existing, nil
    }

    // Case B: New data is higher quality - wash and reload
    if incoming.QualityScore > existing.QualityScore {
        return ActionWashAndReload, existing, nil
    }

    // Case C: Same quality, different value - latest wins
    // Note: For sub-millisecond collision handling, see compareSameQuality()
    // in the Concurrency Handling section.
    if incoming.ReceivedAt.After(existing.ReceivedAt) {
        return ActionWashAndReload, existing, nil
    }

    // Stale same-quality data
    return ActionArchiveOnly, existing, nil
}
```

**Decision flow diagram:**

```
New Measurement Arrives
         │
         ▼
┌─────────────────────┐
│ Save to Measurement │
│ Log (always)        │
└─────────────────────┘
         │
         ▼
┌─────────────────────┐
│ Find existing       │
│ current measurement │
└─────────────────────┘
         │
    ┌────┴────┐
    │ Exists? │
    └────┬────┘
    No ──┴── Yes
    │         │
    ▼         ▼
  BOOK    ┌──────────┐
  NEW     │ Locked?  │
          └────┬─────┘
          Yes ─┴─ No
          │       │
          ▼       ▼
       DISPUTE  Compare Quality
                │
                ├── New < Existing → ARCHIVE ONLY
                │
                ├── New > Existing → WASH & RELOAD
                │
                └── New = Existing
                    │
                    ├── Same value → IGNORE (duplicate)
                    │
                    └── Different → WASH & RELOAD (latest wins)
```

### 5. Correction Saga: Wash & Reload

When higher-quality data arrives, the Correction Saga creates financial adjustments
without mutating historical records.

**Scenario:**
- Current position: 10 units (estimate, settled)
- New measurement: 12 units (actual)
- Action: Reverse old, book new, net effect +2 units

```go
// CorrectionSaga handles the atomic wash and reload of positions.
type CorrectionSaga struct {
    db                *gorm.DB
    measurementRepo   MeasurementRepository
    positionEntryRepo PositionEntryRepository
}

// Execute performs an atomic wash (reversal) and reload (booking).
func (s *CorrectionSaga) Execute(
    ctx context.Context,
    old *Measurement,
    new *Measurement,
) error {
    return s.db.Transaction(func(tx *gorm.DB) error {
        // 1. Mark old measurement as superseded
        if err := tx.Model(old).Update("superseded_by", new.ID).Error; err != nil {
            return fmt.Errorf("failed to mark supersession: %w", err)
        }

        // 2. Create reversal entry (the "Wash")
        wash := PositionEntry{
            ID:            uuid.New(),
            MeasurementID: old.ID,
            AccountID:     old.AccountID,
            AssetCode:     old.AssetCode,
            Period:        old.Period,
            Quantity:      old.Quantity.Neg(),  // Negative to reverse
            EntryType:     EntryTypeCorrectionReversal,
            CorrectionRef: new.ID,
            CreatedAt:     time.Now(),
        }
        if err := tx.Create(&wash).Error; err != nil {
            return fmt.Errorf("failed to create wash entry: %w", err)
        }

        // 3. Create booking entry (the "Reload")
        reload := PositionEntry{
            ID:            uuid.New(),
            MeasurementID: new.ID,
            AccountID:     new.AccountID,
            AssetCode:     new.AssetCode,
            Period:        new.Period,
            Quantity:      new.Quantity,
            EntryType:     EntryTypeCorrectionBooking,
            CorrectionRef: old.ID,
            CreatedAt:     time.Now(),
        }
        if err := tx.Create(&reload).Error; err != nil {
            return fmt.Errorf("failed to create reload entry: %w", err)
        }

        return nil
    })
}
```

**Audit trail after correction:**

```
Position Entries for Account METER-001, Period 12:00-12:30:

┌──────────────┬──────────┬───────────────────────┬─────────────────┐
│ Entry ID     │ Quantity │ Type                  │ Measurement Ref │
├──────────────┼──────────┼───────────────────────┼─────────────────┤
│ entry_001    │ +10.00   │ BOOKING               │ meas_001 (est)  │
│ entry_047    │ -10.00   │ CORRECTION_REVERSAL   │ meas_001 (est)  │
│ entry_048    │ +12.00   │ CORRECTION_BOOKING    │ meas_089 (act)  │
├──────────────┼──────────┼───────────────────────┼─────────────────┤
│ Net Position │ +12.00   │                       │                 │
└──────────────┴──────────┴───────────────────────┴─────────────────┘
```

### 6. Settlement Snapshots and Reconciliation

When a settlement run executes, it captures which measurements were used. This enables
reconciliation when better data arrives in subsequent runs.

```go
// SettlementSnapshot records which measurement was used for each period in a settlement run.
type SettlementSnapshot struct {
    ID               uuid.UUID
    SettlementRunID  uuid.UUID       // References the settlement run
    AccountID        uuid.UUID       // References Current Account
    AssetCode        string          // References Asset Directory
    Period           Period
    MeasurementID    uuid.UUID       // The measurement used
    QuantitySettled  decimal.Decimal // Snapshot of quantity at settlement time
    QualityAtSettle  int             // Quality score at settlement time
    CreatedAt        time.Time
}

// ReconciliationService compares settled positions to current positions.
type ReconciliationService struct {
    snapshotRepo    SettlementSnapshotRepository
    measurementRepo MeasurementRepository
    valuationEngine ValuationEngine
}

// Reconcile identifies variances between settled and current positions.
func (s *ReconciliationService) Reconcile(ctx context.Context, runID uuid.UUID) ([]Variance, error) {
    snapshots, err := s.snapshotRepo.FindByRun(ctx, runID)
    if err != nil {
        return nil, err
    }

    var variances []Variance
    for _, snapshot := range snapshots {
        current, err := s.measurementRepo.FindCurrent(ctx,
            snapshot.AccountID,
            snapshot.AssetCode,
            snapshot.Period,
            nil, // All attributes
        )
        if err != nil {
            return nil, err
        }

        if !current.Quantity.Equal(snapshot.QuantitySettled) {
            delta := current.Quantity.Sub(snapshot.QuantitySettled)

            // Value the variance using the tariff at the original period
            value, err := s.valuationEngine.Valuate(ctx, ValuationRequest{
                AssetCode:  snapshot.AssetCode,
                Quantity:   delta,
                Period:     snapshot.Period,
                Attributes: current.Attributes,
            })
            if err != nil {
                return nil, err
            }

            variances = append(variances, Variance{
                SettlementRunID:  runID,
                Period:           snapshot.Period,
                QuantitySettled:  snapshot.QuantitySettled,
                QuantityCurrent:  current.Quantity,
                QuantityDelta:    delta,
                ValueDelta:       value.SettlementAmount,
            })
        }
    }

    return variances, nil
}
```

**Reconciliation Error Handling:**

The `Reconcile()` function uses fail-fast semantics (immediate return on error).
This is intentional for settlement reconciliation where partial results could lead
to incorrect financial adjustments. Alternative strategies considered:

| Strategy | Pros | Cons | Verdict |
|----------|------|------|---------|
| Fail-fast | Simple, atomic | No partial results | **Chosen** |
| Accumulate errors | Partial progress visible | Risk of partial adjustments | Rejected |
| Skip + log | Maximizes results | Silent failures | Rejected |

For large reconciliation runs, errors are typically transient (API timeouts to Position
Keeping). The caller should implement retry with exponential backoff at the run level,
not the individual snapshot level.

**Reconciliation creates adjustments, not mutations:**

```
Settlement Run D+5 Reconciliation:

┌─────────────────┬─────────────┬─────────────┬───────────┬────────────┐
│ Period          │ Settled Qty │ Current Qty │ Delta Qty │ Delta Val  │
├─────────────────┼─────────────┼─────────────┼───────────┼────────────┤
│ 12:00-12:30     │ 10.00       │ 12.00       │ +2.00     │ +$0.30     │
│ 12:30-13:00     │ 11.00       │ 10.50       │ -0.50     │ -$0.08     │
│ 13:00-13:30     │ 9.00        │ 9.00        │ 0.00      │ $0.00      │
├─────────────────┼─────────────┼─────────────┼───────────┼────────────┤
│ Total           │ 30.00       │ 31.50       │ +1.50     │ +$0.22     │
└─────────────────┴─────────────┴─────────────┴───────────┴────────────┘

Action: Create adjustment entry for $0.22
```

**Performance considerations for high-volume settlement runs:**

For runs with many periods (e.g., 48 half-hours × 365 days = 17,520 snapshots/year):

```go
// FindByRunPaginated returns snapshots in batches for large settlement runs.
func (r *SettlementSnapshotRepository) FindByRunPaginated(
    ctx context.Context,
    runID uuid.UUID,
    limit, offset int,
) ([]SettlementSnapshot, error) {
    var snapshots []SettlementSnapshot
    return snapshots, r.db.Where("settlement_run_id = ?", runID).
        Order("period_start ASC").
        Limit(limit).
        Offset(offset).
        Find(&snapshots).Error
}

// BatchFindCurrent reduces round trips when checking many positions.
func (r *MeasurementRepository) BatchFindCurrent(
    ctx context.Context,
    keys []PositionKey,
) (map[PositionKey]*Measurement, error) {
    // Build a single query for all position keys
    // Returns map keyed by position for efficient lookup
}
```

For most use cases (monthly energy settlements with ~1,440 half-hours),
the simple iteration approach is sufficient. Consider pagination when:
- Annual settlement runs exceed 10,000 snapshots
- Reconciliation jobs process runs in parallel

## Service Responsibilities

| Component | Service | Notes |
|-----------|---------|-------|
| Source Authority Registry | Asset Directory | Quality rankings per source |
| Measurement Log | Position Keeping | Append-only, immutable |
| Delta Engine | Position Keeping | Supersession evaluation |
| Correction Saga | Position Keeping | Wash & Reload execution |
| Position Entries | Position Keeping | Net position calculation |
| Settlement Snapshots | Financial Accounting | Owns settlement lifecycle and snapshots |
| Reconciliation | Financial Accounting | Queries own snapshots, calls Position Keeping API for current measurements |
| Adjustment Entry | Payment Order | Financial settlement of variance |

**Note on Cross-Service Queries:** Per project rules (no cross-schema queries), Financial
Accounting owns Settlement Snapshots in its own schema. Reconciliation fetches current
measurements via Position Keeping's API, not direct database joins. This maintains service
isolation at the cost of additional API calls during reconciliation.

## Consequences

### Positive

* **Complete audit trail**: Every measurement preserved, corrections are explicit entries
* **Clear lineage**: Supersession chain shows estimate → read → actual progression
* **Financial accuracy**: Positions reflect best available data at any point in time
* **Regulatory compliance**: Settlement runs and finality are first-class concepts
* **Universal pattern**: Same model for energy, advertising, aid, carbon, compute

### Negative

* **Storage growth**: All measurements kept forever (required for audit)
* **Query complexity**: "Current" position requires filtering superseded records
* **Materialized views**: May need for performance at scale
* **Ongoing reconciliation**: Positions are never truly "final" until settlement locked

## Implementation Notes

### Database Indexes for Performance

```sql
-- Fast lookup of current measurement for a position
CREATE INDEX idx_measurements_current
    ON measurements(account_id, asset_code, period)
    WHERE superseded_by IS NULL;

-- Period overlap queries
CREATE INDEX idx_measurements_period
    ON measurements USING GIST (period);

-- Reconciliation queries
CREATE INDEX idx_settlement_snapshots_run
    ON settlement_snapshots(settlement_run_id);
```

### Overlap Prevention

Preventing overlapping current positions uses **optimistic concurrency** rather than
pessimistic locking. This is critical for the 100k TPS throughput requirement—SERIALIZABLE
isolation would cause unacceptable contention.

**Strategy: Position Key Hash with Unique Constraint**

```sql
-- Helper function for deterministic JSONB hashing (sorted keys)
CREATE OR REPLACE FUNCTION canonicalize_jsonb(j JSONB) RETURNS TEXT AS $$
DECLARE
    result TEXT := '{';
    key TEXT;
    val JSONB;
    first BOOLEAN := TRUE;
BEGIN
    FOR key, val IN SELECT * FROM jsonb_each(j) ORDER BY 1
    LOOP
        IF NOT first THEN
            result := result || ',';
        END IF;
        result := result || '"' || key || '":' || val::text;
        first := FALSE;
    END LOOP;
    RETURN result || '}';
END;
$$ LANGUAGE plpgsql IMMUTABLE;

-- Add position key hash for optimistic concurrency
-- Uses canonicalize_jsonb() for deterministic attribute ordering
ALTER TABLE measurements ADD COLUMN position_key_hash BYTEA GENERATED ALWAYS AS (
    sha256(
        account_id::text || '|' ||
        asset_code || '|' ||
        canonicalize_jsonb(COALESCE(attributes, '{}'::jsonb)) || '|' ||
        lower(period)::text || '|' ||
        upper(period)::text
    )
) STORED;

-- Partial unique index on non-superseded measurements
CREATE UNIQUE INDEX idx_measurements_position_unique
    ON measurements(position_key_hash)
    WHERE superseded_by IS NULL;
```

**Note on Hash Collisions:** SHA-256 collision probability is astronomically low (~1 in 2^128
for a random collision). If a collision ever occurred, it would manifest as `ErrOverlappingPosition`
for a non-overlapping position. The operational impact is a rejected measurement that should
succeed. Mitigation: the application can detect this via exact match on the composite key
and escalate for manual review. This is acceptable for 100k TPS—probability of ever seeing
a collision is effectively zero over the system's lifetime.

```go
// BookMeasurement uses optimistic concurrency via unique constraint violation.
// No locks, no SERIALIZABLE - relies on database enforcing uniqueness.
func (r *MeasurementRepository) BookMeasurement(ctx context.Context, m *Measurement) error {
    err := r.db.Create(m).Error
    if err != nil {
        // Check for unique constraint violation
        if isUniqueViolation(err, "idx_measurements_position_unique") {
            return ErrOverlappingPosition
        }
        return err
    }
    return nil
}

// For supersession, use optimistic lock on the existing row
func (r *MeasurementRepository) Supersede(ctx context.Context, oldID, newID uuid.UUID) error {
    result := r.db.Model(&Measurement{}).
        Where("id = ? AND superseded_by IS NULL", oldID).
        Update("superseded_by", newID)

    if result.RowsAffected == 0 {
        return ErrAlreadySuperseded  // Lost race - caller should retry or archive
    }
    return result.Error
}
```

**Why not SERIALIZABLE?**
- At 100k TPS, serialization failures would cause cascading retries
- Position Key Hash gives O(1) conflict detection via B-tree index
- Retry logic only needed for actual conflicts, not phantom reads

**Attribute Matching:**

Attributes are included in the position key hash, meaning **exact equality** is required
for collision detection. This is the correct default—positions with different attributes
are different positions (e.g., peak vs off-peak tariff periods).

### Settlement Finality

After final settlement (e.g., M+14 for UK energy), positions are locked:

```go
func (s *SettlementService) FinalizeRun(ctx context.Context, run string, cutoff time.Time) error {
    return s.db.Model(&Measurement{}).
        Where("settlement_run = ? AND locked_at IS NULL", run).
        Where("period && tstzrange(?, ?)", cutoff.AddDate(0, -14, 0), cutoff).
        Update("locked_at", time.Now()).Error
}
```

Once locked, the Delta Engine returns `ActionCreateDispute` instead of `ActionWashAndReload`.

### Concurrency Handling

Supersession uses optimistic locking via the `WHERE superseded_by IS NULL` clause
(see `Supersede()` in Overlap Prevention section above).

**Retry Strategy for `ErrAlreadySuperseded`:**

When concurrent processes attempt to supersede the same measurement, one wins and
the other receives `ErrAlreadySuperseded`. The losing caller should:

```go
func (s *MeasurementIngestionService) IngestWithRetry(ctx context.Context, m *Measurement) error {
    const maxRetries = 3
    for attempt := 0; attempt < maxRetries; attempt++ {
        action, existing, err := s.deltaEngine.Evaluate(ctx, m)
        if err != nil {
            return err
        }

        switch action {
        case ActionWashAndReload:
            err = s.correctionSaga.Execute(ctx, existing, m)
            if errors.Is(err, ErrAlreadySuperseded) {
                // Another process superseded first - re-evaluate
                continue
            }
            return err
        // ... other cases ...
        }
    }
    return fmt.Errorf("failed to ingest after %d retries", maxRetries)
}
```

This is safe because re-evaluation will see the new current measurement and make
the correct decision (likely `ActionArchiveOnly` if the winner had higher quality).

**Simultaneous arrival of same-quality measurements:**

When two measurements with identical quality scores arrive at the same millisecond for
the same position, `ReceivedAt` comparison becomes non-deterministic. Handle this with:

```go
// Measurement includes optional source-specific sequence for tiebreaking.
type Measurement struct {
    // ... existing fields ...

    // SourceSequence provides deterministic ordering when ReceivedAt is identical.
    // For smart meters: cumulative read count. For APIs: request sequence number.
    // Null if source doesn't provide sequencing.
    SourceSequence *int64
}

// compareSameQuality determines winner when quality scores are equal.
func compareSameQuality(a, b *Measurement) *Measurement {
    // Primary: ReceivedAt
    if !a.ReceivedAt.Equal(b.ReceivedAt) {
        if a.ReceivedAt.After(b.ReceivedAt) {
            return a
        }
        return b
    }

    // Secondary: SourceSequence (if both have it)
    if a.SourceSequence != nil && b.SourceSequence != nil {
        if *a.SourceSequence > *b.SourceSequence {
            return a
        }
        return b
    }

    // Tertiary: Lexicographic ID comparison (deterministic fallback)
    if a.ID.String() > b.ID.String() {
        return a
    }
    return b
}
```

## Error Taxonomy

Domain-specific errors for the temporal quality ladder:

```go
var (
    // ErrNotFound indicates no measurement exists for the given position key.
    ErrNotFound = errors.New("measurement not found")

    // ErrAlreadySuperseded indicates the measurement was superseded by another
    // process between read and write (optimistic lock failure).
    ErrAlreadySuperseded = errors.New("measurement already superseded")

    // ErrPositionLocked indicates the position is in final settlement and cannot
    // be modified. New data should route to dispute workflow.
    ErrPositionLocked = errors.New("position is locked for final settlement")

    // ErrInvalidPeriod indicates the period end is before start.
    ErrInvalidPeriod = errors.New("invalid period: end before start")

    // ErrUnknownSource indicates the source code is not registered in the
    // Source Authority Registry.
    ErrUnknownSource = errors.New("unknown source authority")

    // ErrOverlappingPosition indicates an attempt to book a measurement that
    // overlaps with an existing non-superseded measurement (same account/asset/attributes).
    ErrOverlappingPosition = errors.New("overlapping position exists")

    // ErrOutsideBackfillWindow indicates the measurement period is older than
    // the configured backfill window for this asset.
    ErrOutsideBackfillWindow = errors.New("measurement outside backfill window")

    // ErrInvalidSettlementRun indicates the settlement run identifier is malformed
    // or not in the asset's configured schedule.
    ErrInvalidSettlementRun = errors.New("invalid settlement run identifier")
)
```

## Scope and Boundaries

### In Scope

- Quality-based supersession logic (Delta Engine)
- Measurement lifecycle (append, supersede, lock)
- Correction pattern (Wash & Reload)
- Settlement snapshots for reconciliation
- Settlement finality windows

### Out of Scope (Future ADRs)

| Topic | Notes |
|-------|-------|
| **Dispute Resolution Workflow** | When `ActionCreateDispute` is returned, the dispute workflow handles investigation, resolution, and potential manual adjustments. This includes dispute SLAs, escalation paths, and operator UI. Target: ADR-0018. |
| **Valuation Engine** | The `ValuationEngine.Valuate()` call in reconciliation is a placeholder. A dedicated ADR will define temporal tariff lookup, attribute-based pricing tiers, and rate schedule management. ADR-0013's `Rate` type provides the foundation. Target: ADR-0019. |
| **Attribute Schema Validation** | Measurement attributes (`map[string]string`) are opaque in this ADR. Integration with ADR-0014's Schema-on-Write validation for attribute keys/values is implementation detail. |
| **Event Streaming** | This ADR assumes batch-oriented settlement runs. Real-time streaming ingestion with micro-batching may be addressed in a performance optimization ADR. |

### Dispute Fail-Safe Behavior

Until ADR-0018 (Dispute Resolution Workflow) is implemented, handle `ActionCreateDispute` with
a fail-safe that preserves the incoming measurement for manual review:

```go
func (s *MeasurementIngestionService) handleDispute(
    ctx context.Context,
    incoming *Measurement,
    existing *Measurement,
) error {
    // 1. Archive the incoming measurement (preserve for audit)
    incoming.SupersededBy = nil  // Not superseded, just disputed
    if err := s.repo.Create(ctx, incoming); err != nil {
        return err
    }

    // 2. Create dispute record for manual review
    dispute := Dispute{
        ID:                    uuid.New(),
        IncomingMeasurementID: incoming.ID,
        ExistingMeasurementID: existing.ID,
        Reason:                "Position locked after final settlement",
        Status:                DisputeStatusPendingReview,
        CreatedAt:             time.Now(),
    }
    if err := s.disputeRepo.Create(ctx, &dispute); err != nil {
        return err
    }

    // 3. Emit event for alerting/monitoring
    s.events.Publish(ctx, DisputeCreatedEvent{DisputeID: dispute.ID})

    return nil  // Success - measurement preserved, dispute logged
}
```

**Key principle:** Never silently drop data. If a measurement arrives for a locked position,
archive it and create a dispute record. The business can then decide to extend the settlement
window, adjust via dispute resolution, or acknowledge the variance.

## Entry Types

Position entries track all movements including corrections:

```go
type EntryType string

const (
    // EntryTypeBooking is the initial booking of a measurement.
    EntryTypeBooking EntryType = "BOOKING"

    // EntryTypeCorrectionReversal is the "wash" - negates a previous booking.
    EntryTypeCorrectionReversal EntryType = "CORRECTION_REVERSAL"

    // EntryTypeCorrectionBooking is the "reload" - books the replacement value.
    EntryTypeCorrectionBooking EntryType = "CORRECTION_BOOKING"

    // EntryTypeTransfer moves quantity between accounts (same asset).
    EntryTypeTransfer EntryType = "TRANSFER"

    // EntryTypeAdjustment is a manual correction by an operator.
    EntryTypeAdjustment EntryType = "ADJUSTMENT"

    // EntryTypeDispute is a correction resulting from dispute resolution.
    EntryTypeDispute EntryType = "DISPUTE"
)

// PositionEntry represents a single change to an account's position.
type PositionEntry struct {
    ID            uuid.UUID
    MeasurementID uuid.UUID       // Source measurement
    AccountID     uuid.UUID
    AssetCode     string
    Period        Period
    Quantity      decimal.Decimal // Positive or negative
    EntryType     EntryType
    CorrectionRef *uuid.UUID      // Links wash/reload pairs
    CreatedAt     time.Time
}
```

## Links

* [ADR-0013: Universal Quantity Type System](0013-generic-asset-quantity-types.md)
* [ADR-0014: Dynamic Asset Registry & Lifecycle](0014-dynamic-asset-registry.md)
* [UK Balancing and Settlement Code (BSC)](https://www.elexon.co.uk/bsc-and-codes/)
* [ELEXON Settlement Timetable](https://www.elexon.co.uk/operations-settlement/settlement-timetable/)

## Notes

### Backfill Window

Different industries have different backfill windows:

| Industry | Backfill Window | Final Settlement |
|----------|-----------------|------------------|
| UK Energy | 14 months | R3 (M+14) |
| Advertising | 30-90 days | Varies by network |
| Banking | T+1 to T+3 | Same day to 3 days |
| Carbon | Years | Registry-dependent |

**Configuration schema (extends ADR-0014 asset definitions):**

```sql
-- Settlement rules table linked to asset definitions
CREATE TABLE asset_settlement_rules (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    asset_code VARCHAR(32) NOT NULL REFERENCES asset_definitions(code),

    -- Backfill limits
    backfill_window_days INTEGER NOT NULL DEFAULT 365,

    -- Settlement run schedule (cron-like or explicit)
    settlement_schedule JSONB NOT NULL DEFAULT '["D+1", "D+5", "M+3", "M+14"]',

    -- Finality: after which run positions lock
    final_settlement_run VARCHAR(20) NOT NULL DEFAULT 'M+14',

    -- Grace period after final before disputes auto-close
    dispute_window_days INTEGER NOT NULL DEFAULT 30,

    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),

    UNIQUE(asset_code)
);

-- Example: UK half-hourly electricity
INSERT INTO asset_settlement_rules (asset_code, backfill_window_days, final_settlement_run)
VALUES ('ELEC_HH_KWH', 425, 'M+14');  -- 14 months = ~425 days

-- Example: Real-time gross settlement (banking)
INSERT INTO asset_settlement_rules (asset_code, backfill_window_days, final_settlement_run)
VALUES ('GBP', 0, 'D+0');  -- Same-day finality

-- Example: Compute time (hourly periods)
INSERT INTO asset_settlement_rules (asset_code, backfill_window_days, final_settlement_run)
VALUES ('COMPUTE_HOURS', 30, 'M+1');  -- Monthly finality
```

### Settlement Run Validation

The `settlement_run` field on measurements uses structured identifiers validated at ingestion:

```go
// SettlementRunID represents a validated settlement run identifier.
type SettlementRunID string

const (
    MaxDayOffset   = 30  // D+0 through D+30
    MaxMonthOffset = 24  // M+1 through M+24 (2 years)
)

// ParseSettlementRun validates and parses a settlement run string.
// Valid formats: "D+N" (days, 0-30), "M+N" (months, 1-24), "FINAL"
func ParseSettlementRun(s string) (SettlementRunID, error) {
    if s == "FINAL" {
        return SettlementRunID(s), nil
    }

    re := regexp.MustCompile(`^(D|M)\+(\d+)$`)
    matches := re.FindStringSubmatch(s)
    if matches == nil {
        return "", fmt.Errorf("invalid settlement run format: %s", s)
    }

    unit := matches[1]
    offset, _ := strconv.Atoi(matches[2])

    switch unit {
    case "D":
        if offset > MaxDayOffset {
            return "", fmt.Errorf("day offset %d exceeds maximum %d", offset, MaxDayOffset)
        }
    case "M":
        if offset < 1 || offset > MaxMonthOffset {
            return "", fmt.Errorf("month offset %d outside valid range 1-%d", offset, MaxMonthOffset)
        }
    }

    return SettlementRunID(s), nil
}

// IsScheduled checks if this run is in the asset's settlement schedule.
func (r SettlementRunID) IsScheduled(schedule []string) bool {
    for _, s := range schedule {
        if s == string(r) {
            return true
        }
    }
    return false
}
```

**Validation at measurement ingestion:**

```go
func (s *MeasurementIngestionService) Ingest(ctx context.Context, m *Measurement) error {
    // Validate settlement_run against asset's configured schedule
    rules, err := s.rulesRepo.FindByAsset(ctx, m.AssetCode)
    if err != nil {
        return err
    }

    runID, err := ParseSettlementRun(m.SettlementRun)
    if err != nil {
        return fmt.Errorf("%w: %v", ErrInvalidSettlementRun, err)
    }

    if !runID.IsScheduled(rules.SettlementSchedule) {
        return fmt.Errorf("%w: %s not in schedule for %s",
            ErrInvalidSettlementRun, m.SettlementRun, m.AssetCode)
    }

    // Continue with normal ingestion...
}
```

This ensures settlement runs are consistent with asset configuration and prevents
free-form strings that would break reconciliation queries.

### Valuation Integration

The Valuation Engine (future ADR) must support:
- Temporal rate lookup (rate at settlement period, not current)
- Attribute-based pricing (peak/off-peak, vintage, grade)
- Multi-period aggregation with different rates per sub-period
- **Cross-asset conversion**: Direct value translation between non-monetary asset classes
  (e.g., compute hours → carbon credits, commodity A → commodity B at market rate)

### Data Retention and Archival

At 100k TPS of measurement ingestion, storage will grow rapidly. Retention strategy:

| Data Type | Hot Storage | Warm Storage | Cold/Archive |
|-----------|-------------|--------------|--------------|
| Current measurements | Indefinite | N/A | N/A |
| Superseded measurements | 90 days | 2 years | 7+ years |
| Settlement snapshots | Until final + 1 year | 7 years | 10+ years |
| Position entries | 2 years | 7 years | 10+ years |

**Implementation (out of scope for this ADR):**

```sql
-- Example: Move superseded measurements older than 90 days to archive schema
INSERT INTO archive.measurements SELECT * FROM measurements
WHERE superseded_by IS NOT NULL AND received_at < NOW() - INTERVAL '90 days';

DELETE FROM measurements
WHERE superseded_by IS NOT NULL AND received_at < NOW() - INTERVAL '90 days';
```

Archival preserves audit trail while keeping hot path performant. Consider partitioning
by `received_at` month for efficient bulk archival operations.

### Settlement Snapshot Denormalization Justification

Settlement snapshots duplicate `quantity` from measurements. For a UK energy supplier
with 1M meters, annual snapshots: 1M × 17,520 periods × 32 bytes ≈ **560 GB/year**.

This denormalization is justified because:

1. **Query locality**: Reconciliation needs snapshot + current measurement. Without
   denormalization, every reconciliation requires joining to measurements table.

2. **Immutability**: Snapshots are write-once. The measurement's quantity at snapshot
   time is preserved even if the measurement is later superseded.

3. **Cross-service isolation**: Financial Accounting owns snapshots in its schema.
   Without denormalization, it would need to query Position Keeping for every reconciliation.

4. **Acceptable cost**: 560 GB/year is modest for a financial system handling 1M meters.
   Storage is cheap; cross-service latency at scale is not.

### Reconsidering This Decision

Revisit if:
- Query performance degrades with measurement volume (consider event sourcing)
- Settlement rules require retroactive position mutation (regulatory change)
- Real-time streaming replaces batch settlement (architecture shift)
- Storage costs become prohibitive (consider snapshot compression)
