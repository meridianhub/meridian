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
// All timestamps MUST be in UTC to ensure consistent comparisons and storage.
type Period struct {
    Start time.Time
    End   time.Time
}

// NewPeriod creates a validated Period. Returns error if timestamps are not UTC
// or if End is before Start.
func NewPeriod(start, end time.Time) (Period, error) {
    if start.Location() != time.UTC {
        return Period{}, errors.New("period start must be in UTC")
    }
    if end.Location() != time.UTC {
        return Period{}, errors.New("period end must be in UTC")
    }
    if end.Before(start) {
        return Period{}, errors.New("period end cannot be before start")
    }
    return Period{Start: start, End: end}, nil
}

// MustPeriod creates a Period, panicking on validation failure.
// Use only in tests or initialization where failure is fatal.
func MustPeriod(start, end time.Time) Period {
    p, err := NewPeriod(start, end)
    if err != nil {
        panic(err)
    }
    return p
}

func Instant(t time.Time) (Period, error) {
    if t.Location() != time.UTC {
        return Period{}, errors.New("instant must be in UTC")
    }
    return Period{Start: t, End: t}, nil
}

func (p Period) IsInstant() bool {
    return p.Start.Equal(p.End)
}

func (p Period) Duration() time.Duration {
    return p.End.Sub(p.Start)
}

// Overlaps returns true if this period shares any time with another.
// Uses half-open interval semantics [Start, End) for ranged periods.
// For instant events (Start == End), overlap requires containment by the other period,
// or both being the same instant.
func (p Period) Overlaps(other Period) bool {
    // Two instants overlap if and only if they're at the same moment
    if p.IsInstant() && other.IsInstant() {
        return p.Start.Equal(other.Start)
    }
    // Instant overlaps range if contained: a <= t < b
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

// Validate checks period invariants. Prefer NewPeriod() for construction.
func (p Period) Validate() error {
    if p.Start.Location() != time.UTC {
        return errors.New("period start must be in UTC")
    }
    if p.End.Location() != time.UTC {
        return errors.New("period end must be in UTC")
    }
    if p.End.Before(p.Start) {
        return errors.New("period end cannot be before start")
    }
    return nil
}
```

**Why different interval semantics?**

| Method | Semantics | Rationale |
|--------|-----------|-----------|
| `Overlaps()` | Half-open `[Start, End)` | Adjacent periods don't overlap: `[12:00, 12:30)` and `[12:30, 13:00)` are non-overlapping |
| `Contains()` | Closed `[Start, End]` | Boundary instants belong to their period: `12:30` is "in" `[12:00, 12:30]` |
| Instants | Point `[t, t]` | An instant at `12:30` overlaps `[12:00, 13:00)` but not `[13:00, 14:00)` |

This matches PostgreSQL's `TSTZRANGE` default behavior and ensures settlement periods
partition time without gaps or overlaps.

**Database representation using PostgreSQL range types:**

```sql
CREATE TABLE measurements (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id UUID NOT NULL,          -- Tenant isolation (see ADR-0016)
    account_id UUID NOT NULL,         -- References current_accounts.id
    asset_code VARCHAR(32) NOT NULL,  -- References asset_definitions.code
    quantity DECIMAL(38, 18) NOT NULL,

    -- Time as range (point-in-time has start = end)
    -- All timestamps MUST be in UTC (enforced at application layer)
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

    -- Prevent overlapping current positions for same tenant/account/asset/attributes
    -- See "Overlap Prevention" section below for enforcement details
    CONSTRAINT no_overlapping_current CHECK (
        superseded_by IS NOT NULL OR TRUE  -- Placeholder; enforced via application logic
    )
);

-- Tenant isolation index (critical for performance and security)
CREATE INDEX idx_measurements_tenant ON measurements(tenant_id);

-- Overlap prevention is enforced at the application layer using optimistic
-- concurrency via position_key_hash. PostgreSQL exclusion constraints cannot
-- handle the multi-column key (tenant_id, account_id, asset_code, attributes) with JSONB.
--
-- See the Overlap Prevention section in Implementation Notes for details.

CREATE INDEX idx_measurements_lookup
    ON measurements(tenant_id, account_id, asset_code, period)
    WHERE superseded_by IS NULL;
```

**TSTZRANGE Handling for Instant Events:**

PostgreSQL's `TSTZRANGE` with default bounds `[)` represents `[t, t)` as an empty range.
To correctly handle instants where `Start == End`:

```sql
-- Application creates instants with closed-closed bounds
INSERT INTO measurements (period, ...) VALUES (
    tstzrange('2025-01-15 12:00:00+00', '2025-01-15 12:00:00+00', '[]'),  -- Instant
    ...
);

-- GiST index handles both range and instant queries efficiently
-- Range containment: period @> tstzrange('2025-01-15 12:00', '2025-01-15 12:30', '[)')
-- Instant lookup: period @> '2025-01-15 12:15:00+00'::timestamptz
```

The application's `Instant(t)` function creates closed-closed ranges `[t, t]` which
PostgreSQL normalizes to a point. The GiST index supports both range overlap queries
and point containment queries efficiently.

**TSTZRANGE Integration Tests (Required):**

Before production deployment, verify PostgreSQL's TSTZRANGE behavior with integration tests:

```go
func TestTSTZRANGE_InstantHandling(t *testing.T) {
    db := setupTestDB(t)

    tests := []struct {
        name     string
        setup    string
        query    string
        expected bool
    }{
        {
            name:     "point-in-range query works",
            setup:    "INSERT INTO measurements (period) VALUES (tstzrange('2025-01-15 12:00+00', '2025-01-15 13:00+00', '[)'))",
            query:    "SELECT EXISTS(SELECT 1 FROM measurements WHERE period @> '2025-01-15 12:30+00'::timestamptz)",
            expected: true,
        },
        {
            name:     "instant-to-instant overlap detected",
            setup:    "INSERT INTO measurements (period) VALUES (tstzrange('2025-01-15 12:00+00', '2025-01-15 12:00+00', '[]'))",
            query:    "SELECT EXISTS(SELECT 1 FROM measurements WHERE period && tstzrange('2025-01-15 12:00+00', '2025-01-15 12:00+00', '[]'))",
            expected: true,
        },
        {
            name:     "instant at range boundary (exclusive end)",
            setup:    "INSERT INTO measurements (period) VALUES (tstzrange('2025-01-15 12:00+00', '2025-01-15 13:00+00', '[)'))",
            query:    "SELECT EXISTS(SELECT 1 FROM measurements WHERE period @> '2025-01-15 13:00+00'::timestamptz)",
            expected: false, // 13:00 is exclusive end
        },
        {
            name:     "GiST index used for range query",
            setup:    "INSERT INTO measurements (period) VALUES (tstzrange('2025-01-15 12:00+00', '2025-01-15 13:00+00', '[)'))",
            query:    "EXPLAIN SELECT * FROM measurements WHERE period && tstzrange('2025-01-15 12:00+00', '2025-01-15 12:30+00', '[)')",
            expected: true, // Should show "Index Scan using idx_measurements_period"
        },
    }

    for _, tt := range tests {
        t.Run(tt.name, func(t *testing.T) {
            // ... test implementation ...
        })
    }
}
```

These tests validate:
1. Point-in-range containment queries work correctly
2. Instant-to-instant overlap detection (critical for duplicate prevention)
3. Half-open interval boundary behavior (`[)` excludes end)
4. GiST index is actually used for range queries (query plan verification)

### 2. Source Authority Registry

The **Source Authority Registry** is the canonical source for quality rankings. Measurements
MAY carry a denormalized snapshot of the quality score at ingestion for query performance,
but this cached value can diverge if the registry is updated post-ingestion.

**Important:** All reconciliation or authoritative decisions (supersession, dispute routing,
settlement finality) must consult the registry rather than relying solely on denormalized
measurement values. The denormalized `QualityScore` field on measurements is an optimization
for read-heavy queries, not the source of truth.

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
    Source        string    // Lookup key into Source Authority Registry
    QualityScore  int       // Denormalized snapshot at ingestion (see Source Authority Registry
                            // section). May diverge from registry if rankings change post-ingestion.
                            // Authoritative decisions must consult registry, not this cached value.

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
    ActionError Action = iota - 1    // Sentinel for error state (never returned on success)
    ActionBookNew                    // No existing data, book this measurement
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
// Returns ActionError on failure - callers must check error before using Action.
func (e *DeltaEngine) Evaluate(ctx context.Context, incoming Measurement) (Action, *Measurement, error) {
    // Find existing current measurement for this position key
    existing, err := e.repo.FindCurrent(ctx,
        incoming.AccountID,
        incoming.AssetCode,
        incoming.Period,
        incoming.Attributes,
    )
    if err != nil && !errors.Is(err, ErrNotFound) {
        return ActionError, nil, err
    }

    // Case D: No existing measurement - book as new
    if existing == nil {
        return ActionBookNew, nil, nil
    }

    // Case: Position is locked (final settlement)
    if existing.IsLocked() {
        return ActionCreateDispute, existing, nil
    }

    // Case E: Duplicate detection - must match source, value, period, AND attributes
    // to be considered a true duplicate (idempotent retry)
    if incoming.Source == existing.Source &&
       incoming.Quantity.Equal(existing.Quantity) &&
       incoming.Period.Start.Equal(existing.Period.Start) &&
       incoming.Period.End.Equal(existing.Period.End) &&
       mapsEqual(incoming.Attributes, existing.Attributes) {
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

// mapsEqual checks if two string maps have identical keys and values.
func mapsEqual(a, b map[string]string) bool {
    if len(a) != len(b) {
        return false
    }
    for k, v := range a {
        if bv, ok := b[k]; !ok || bv != v {
            return false
        }
    }
    return true
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
// Respects context timeout/cancellation for long-running transactions.
func (s *CorrectionSaga) Execute(
    ctx context.Context,
    old *Measurement,
    new *Measurement,
) error {
    return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
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
// PositionKey uniquely identifies a position for lookup purposes.
// Includes Attributes per ADR-0014 fungibility requirements.
type PositionKey struct {
    AccountID  uuid.UUID
    AssetCode  string
    Period     Period
    Attributes map[string]string // Required: positions with different attributes are distinct
}

// SettlementSnapshot records which measurement was used for each period in a settlement run.
// Captures full position context including attributes for reconciliation.
type SettlementSnapshot struct {
    ID               uuid.UUID
    SettlementRunID  uuid.UUID         // References the settlement run
    AccountID        uuid.UUID         // References Current Account
    AssetCode        string            // References Asset Directory
    Period           Period
    Attributes       map[string]string // Fungibility context at settlement (from ADR-0014)
    MeasurementID    uuid.UUID         // The measurement used
    QuantitySettled  decimal.Decimal   // Snapshot of quantity at settlement time
    QualityAtSettle  int               // Quality score at settlement time
    CreatedAt        time.Time
}

// ReconciliationService compares settled positions to current positions.
type ReconciliationService struct {
    snapshotRepo    SettlementSnapshotRepository
    measurementRepo MeasurementRepository
    valuationEngine ValuationEngine
}

// Reconcile identifies variances between settled and current positions.
// Uses batch fetching to avoid N+1 query problem on large settlement runs.
func (s *ReconciliationService) Reconcile(ctx context.Context, runID uuid.UUID) ([]Variance, error) {
    snapshots, err := s.snapshotRepo.FindByRun(ctx, runID)
    if err != nil {
        return nil, err
    }

    // Extract position keys (including attributes) and batch fetch current measurements
    keys := make([]PositionKey, len(snapshots))
    for i, snap := range snapshots {
        keys[i] = PositionKey{
            AccountID:  snap.AccountID,
            AssetCode:  snap.AssetCode,
            Period:     snap.Period,
            Attributes: snap.Attributes, // Required for fungibility-aware lookup
        }
    }

    currentByKey, err := s.measurementRepo.BatchFindCurrent(ctx, keys)
    if err != nil {
        return nil, err
    }

    // Compare and collect variances
    var variances []Variance
    for _, snapshot := range snapshots {
        key := PositionKey{
            AccountID:  snapshot.AccountID,
            AssetCode:  snapshot.AssetCode,
            Period:     snapshot.Period,
            Attributes: snapshot.Attributes,
        }
        current, ok := currentByKey[key]
        if !ok {
            // No current measurement - position may have been fully reversed
            continue
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
// Matches on all PositionKey fields including Attributes for fungibility.
func (r *MeasurementRepository) BatchFindCurrent(
    ctx context.Context,
    keys []PositionKey,
) (map[PositionKey]*Measurement, error) {
    // Build a single query for all position keys (account, asset, period, attributes)
    // Uses position_key_hash for efficient matching
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

-- Settlement finality queries (FinalizeRun)
CREATE INDEX idx_measurements_settlement_run
    ON measurements(settlement_run)
    WHERE locked_at IS NULL;

-- Attribute-based reporting and analytics queries
CREATE INDEX idx_measurements_attributes
    ON measurements USING GIN (attributes);
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

### Hash Collision Risk Assessment

> **TL;DR:** SHA-256 collision probability is ~1 in 2^128. You will never see one.

| Factor | Value |
|--------|-------|
| Hash algorithm | SHA-256 (256-bit output) |
| Collision probability (birthday) | ~1 in 2^128 for 2^64 hashes |
| At 100k TPS | Would take ~5.8 billion years to reach 2^64 measurements |
| Operational impact if it occurred | `ErrOverlappingPosition` for non-overlapping position |
| Mitigation | Exact key comparison on collision, escalate to manual review |

```go
// BookMeasurement uses optimistic concurrency via unique constraint violation.
// On collision, verifies it's a true overlap vs hash collision before rejecting.
func (r *MeasurementRepository) BookMeasurement(ctx context.Context, m *Measurement) error {
    err := r.db.Create(m).Error
    if err != nil {
        if isUniqueViolation(err, "idx_measurements_position_unique") {
            // Verify true overlap vs hash collision (astronomically unlikely)
            existing, _ := r.findByExactKey(ctx, m.AccountID, m.AssetCode, m.Period, m.Attributes)
            if existing == nil {
                // Hash collision! Log for investigation, this should never happen
                log.Error("SHA-256 hash collision detected", "measurement", m.ID)
                return fmt.Errorf("%w: possible hash collision, escalate to support", ErrOverlappingPosition)
            }
            return ErrOverlappingPosition
        }
        return err
    }
    return nil
}
```

```go
// Supersede uses optimistic lock on the existing row
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
    return s.db.Transaction(func(tx *gorm.DB) error {
        // 1. Archive the incoming measurement (preserve for audit)
        incoming.SupersededBy = nil  // Not superseded, just disputed
        if err := tx.Create(incoming).Error; err != nil {
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
        if err := tx.Create(&dispute).Error; err != nil {
            return err
        }

        // 3. Write event to outbox table (same transaction)
        // Event will be published asynchronously by outbox processor
        outboxEvent := OutboxEvent{
            ID:          uuid.New(),
            EventType:   "DisputeCreated",
            Payload:     mustMarshal(DisputeCreatedEvent{DisputeID: dispute.ID}),
            CreatedAt:   time.Now(),
            ProcessedAt: nil,
        }
        if err := tx.Create(&outboxEvent).Error; err != nil {
            return err
        }

        return nil
    })
}
```

**Key principle:** Never silently drop data. If a measurement arrives for a locked position,
archive it and create a dispute record. The business can then decide to extend the settlement
window, adjust via dispute resolution, or acknowledge the variance.

**Transaction Boundary and Outbox Pattern:**

The `handleDispute()` function wraps all three operations (measurement, dispute, event) in a
single database transaction. This ensures atomicity—either all succeed or all roll back.

Events use the **transactional outbox pattern** rather than direct publishing because:

| Approach | Problem |
|----------|---------|
| Direct publish after commit | If publish fails, dispute exists but no alert fires |
| Direct publish before commit | If transaction rolls back, event was already sent (phantom event) |
| **Outbox table (chosen)** | Event written in same transaction; async processor publishes with at-least-once semantics |

The outbox processor (implementation out of scope) polls the `outbox_events` table and publishes
to the event bus. On successful publish, it sets `processed_at`. Failed publishes are retried
with exponential backoff. This guarantees eventual delivery without distributed transaction complexity.

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
// Note: Attributes are NOT stored on entries - they are always derived from the
// linked Measurement via MeasurementID. This avoids duplication and ensures
// attribute changes propagate correctly through the supersession chain.
type PositionEntry struct {
    ID            uuid.UUID
    MeasurementID uuid.UUID       // Source measurement (attributes derivable from here)
    AccountID     uuid.UUID
    AssetCode     string
    Period        Period
    Quantity      decimal.Decimal // Positive or negative
    EntryType     EntryType
    CorrectionRef *uuid.UUID      // Links wash/reload pairs
    CreatedAt     time.Time
}

// GetAttributes returns attributes from the source measurement.
func (e PositionEntry) GetAttributes(ctx context.Context, repo MeasurementRepository) (map[string]string, error) {
    m, err := repo.FindByID(ctx, e.MeasurementID)
    if err != nil {
        return nil, err
    }
    return m.Attributes, nil
}
```

## Event Contracts

Per ADR-0004 (Event-Driven Architecture), the following domain events are published during
measurement lifecycle operations. All events include standard envelope fields (event_id,
timestamp, tenant_id, correlation_id).

### Measurement Events

| Event | Trigger | Payload | Consumers |
|-------|---------|---------|-----------|
| `MeasurementReceived` | Any measurement ingested | measurement_id, account_id, asset_code, period, source, quality_score | Audit, Analytics |
| `MeasurementSuperseded` | Higher-quality data replaces existing | superseded_id, superseding_id, quality_delta | Position Keeping, Audit |
| `MeasurementLocked` | Settlement finalized | measurement_id, settlement_run, locked_at | Financial Accounting |

### Settlement Events

| Event | Trigger | Payload | Consumers |
|-------|---------|---------|-----------|
| `SettlementRunStarted` | Settlement batch begins | run_id, run_type, period_start, period_end | Monitoring, Audit |
| `SettlementSnapshotCreated` | Position captured for settlement | snapshot_id, run_id, measurement_id | Financial Accounting |
| `SettlementRunCompleted` | Settlement batch finishes | run_id, positions_settled, total_value | Monitoring, Payment Order |

### Correction Events

| Event | Trigger | Payload | Consumers |
|-------|---------|---------|-----------|
| `CorrectionInitiated` | Wash & Reload saga starts | correction_id, old_measurement_id, new_measurement_id | Audit |
| `CorrectionCompleted` | Wash & Reload saga succeeds | correction_id, wash_entry_id, reload_entry_id, delta | Financial Accounting, Audit |
| `DisputeCreated` | Locked position receives new data | dispute_id, incoming_measurement_id, existing_measurement_id | Dispute Resolution, Alerting |

### Event Publishing Pattern

Events are published using the transactional outbox pattern (see Dispute Fail-Safe section)
to ensure exactly-once delivery semantics:

```go
// Example: Publishing MeasurementSuperseded event
func (s *CorrectionSaga) Execute(ctx context.Context, old, new *Measurement) error {
    return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
        // ... correction logic ...

        // Write event to outbox (same transaction)
        event := OutboxEvent{
            ID:        uuid.New(),
            EventType: "MeasurementSuperseded",
            Payload: mustMarshal(MeasurementSupersededEvent{
                SupersededID:  old.ID,
                SupersedingID: new.ID,
                QualityDelta:  new.QualityScore - old.QualityScore,
            }),
            CreatedAt: time.Now(),
        }
        return tx.Create(&event).Error
    })
}
```

## Security Considerations

### Settlement Locking Authorization

The `LockedAt` field controls position finality. Only authorized services may lock positions:

| Actor | Can Lock? | Mechanism |
|-------|-----------|-----------|
| Settlement Service | Yes | Automatic after final run (M+14) |
| Tenant Admin | No | Must request via support ticket |
| Operator | No | Read-only access to locked positions |
| System Admin | Emergency only | Requires audit log entry + approval |

```go
// SettlementService is the only authorized caller
func (s *SettlementService) FinalizeRun(ctx context.Context, run string) error {
    // Verify caller identity via context (service account)
    if !auth.IsSettlementService(ctx) {
        return ErrUnauthorized
    }
    // ... locking logic ...
}
```

### Dispute Workflow Security

Dispute creation is rate-limited and audited:

```go
// Rate limiting per tenant to prevent abuse
const MaxDisputesPerHour = 100

func (s *MeasurementIngestionService) handleDispute(ctx context.Context, ...) error {
    tenantID := auth.TenantIDFromContext(ctx)

    // Check rate limit
    if s.rateLimiter.DisputesThisHour(tenantID) >= MaxDisputesPerHour {
        return ErrDisputeRateLimitExceeded
    }

    // ... dispute creation with audit logging ...
    s.auditLog.Record(ctx, AuditEntry{
        Action:    "DISPUTE_CREATED",
        TenantID:  tenantID,
        ActorID:   auth.ActorIDFromContext(ctx),
        Details:   map[string]any{"dispute_id": dispute.ID},
    })
}
```

## Observability

### Metrics

| Metric | Type | Labels | Description |
|--------|------|--------|-------------|
| `measurements_received_total` | Counter | tenant_id, asset_code, source | Total measurements ingested |
| `measurements_superseded_total` | Counter | tenant_id, asset_code | Measurements replaced by higher quality |
| `correction_saga_duration_seconds` | Histogram | tenant_id, outcome | Wash & Reload execution time |
| `settlement_run_duration_seconds` | Histogram | run_type | Settlement batch processing time |
| `reconciliation_variance_total` | Gauge | tenant_id, asset_code | Current variance amount |
| `disputes_pending_total` | Gauge | tenant_id | Disputes awaiting resolution |

### Tracing

Each measurement lifecycle operation should propagate trace context:

```go
func (e *DeltaEngine) Evaluate(ctx context.Context, incoming Measurement) (Action, *Measurement, error) {
    ctx, span := tracer.Start(ctx, "DeltaEngine.Evaluate",
        trace.WithAttributes(
            attribute.String("measurement.id", incoming.ID.String()),
            attribute.String("measurement.source", incoming.Source),
            attribute.Int("measurement.quality_score", incoming.QualityScore),
        ),
    )
    defer span.End()

    // ... evaluation logic ...
    span.SetAttributes(attribute.String("action", action.String()))
    return action, existing, nil
}
```

### Alerting Thresholds

| Alert | Condition | Severity | Action |
|-------|-----------|----------|--------|
| High Dispute Rate | >10 disputes/hour/tenant | Warning | Investigate data source |
| Settlement Overdue | Run not completed T+2 hours | Critical | Page on-call |
| Variance Threshold | Single period variance >$10k | Warning | Review for fraud |
| Quality Degradation | >50% measurements at lowest quality | Warning | Check meter connectivity |

## Placeholder Interfaces

Interfaces referenced but defined in future ADRs:

```go
// ValuationEngine converts quantities to settlement values.
// Full specification in ADR-0019 (Valuation Engine).
type ValuationEngine interface {
    Valuate(ctx context.Context, req ValuationRequest) (*ValuationResult, error)
}

type ValuationRequest struct {
    AssetCode  string
    Quantity   decimal.Decimal
    Period     Period
    Attributes map[string]string
}

type ValuationResult struct {
    SettlementAmount decimal.Decimal
    Currency         string
    RateApplied      decimal.Decimal
    RateEffectiveAt  time.Time
}
```

## Glossary

| Term | Definition |
|------|------------|
| **Quality Ladder** | Hierarchy of data sources ranked by authority (e.g., estimate < customer read < meter actual) |
| **Wash & Reload** | Correction pattern: reverse old position entry, book new entry, preserving audit trail |
| **Settlement Run** | Batch process that finalizes positions for a time period (e.g., D+1, M+14) |
| **Supersession** | Replacement of one measurement by another of higher quality for the same position |
| **Position Key** | Composite identifier: (tenant_id, account_id, asset_code, period, attributes) |
| **Backfill Window** | Maximum age of measurements accepted before rejection |
| **Final Settlement** | Point after which positions are locked and changes become disputes |
| **Delta Engine** | Decision component that evaluates incoming measurements against current state |

## Links

### Internal ADRs

* [ADR-0004: Event-Driven Architecture](0004-event-driven-architecture.md) - Event contracts and publishing patterns
* [ADR-0013: Universal Quantity Type System](0013-generic-asset-quantity-types.md) - Quantity and rate types
* [ADR-0014: Dynamic Asset Registry & Lifecycle](0014-dynamic-asset-registry.md) - Asset definitions and attributes
* [ADR-0016: Tenant Isolation](0016-tenant-isolation.md) - Multi-tenancy and schema separation

### External References

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

    // Validate measurement period is within backfill window
    backfillCutoff := time.Now().AddDate(0, 0, -rules.BackfillWindowDays)
    if m.Period.End.Before(backfillCutoff) {
        return fmt.Errorf("%w: period ends %s, backfill cutoff is %s for asset %s",
            ErrOutsideBackfillWindow, m.Period.End.Format(time.RFC3339),
            backfillCutoff.Format(time.RFC3339), m.AssetCode)
    }

    // Continue with normal ingestion (Delta Engine evaluation, etc.)
}
```

This ensures:
1. Settlement runs are consistent with asset configuration
2. Measurements outside the backfill window are rejected early
3. Free-form strings that would break reconciliation queries are prevented

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

### Design Decisions FAQ

**Q: Should attribute matching support "key subset" matching (e.g., match only on `tariff_zone`)?**

A: No. Exact equality is required for position key hashing. Rationale:
- Positions with different attributes ARE different positions
- Subset matching would require application-level logic that's harder to make consistent
- If you need "match on tariff_zone only", model tariff_zone as the asset code or use separate accounts

For flexible attribute querying (reporting, analytics), use the GIN index on attributes:
```sql
CREATE INDEX idx_measurements_attributes ON measurements USING GIN (attributes);
-- Then query: SELECT * FROM measurements WHERE attributes @> '{"tariff_zone": "peak"}'
```

**Q: Should `MaxDayOffset` and `MaxMonthOffset` be configurable per-tenant or per-asset?**

A: Currently hardcoded for simplicity. To make configurable:
```go
// In asset_settlement_rules table:
ALTER TABLE asset_settlement_rules ADD COLUMN max_day_offset INTEGER DEFAULT 30;
ALTER TABLE asset_settlement_rules ADD COLUMN max_month_offset INTEGER DEFAULT 24;

// Validation then becomes:
if offset > rules.MaxDayOffset { ... }
```

This adds complexity and is deferred until a tenant actually needs non-standard ranges.
The defaults (D+30, M+24) cover all known settlement conventions.

**Q: Where is `ErrOutsideBackfillWindow` enforced?**

A: In `MeasurementIngestionService.Ingest()` (see Settlement Run Validation section).
The check compares `m.Period.End` against `now - backfill_window_days` from the asset's
settlement rules.

**Q: If a measurement's attributes need correction (not the quantity), does this trigger Wash & Reload?**

A: Yes. Attributes are part of the **position key hash**—changing attributes means the
measurement belongs to a different position entirely. The correction path is:

1. Ingest new measurement with corrected attributes (books to the correct position)
2. Delta Engine evaluates the original position (old attributes) and sees no change
3. Original measurement remains current for its (incorrect) position
4. Manual intervention marks original as superseded with reason "ATTRIBUTE_CORRECTION"

If the incorrect-attribute position should have zero balance, explicitly book a reversal.
There's no "lighter-weight" path because attribute changes are semantically significant—
a measurement for `{"tariff_zone": "peak"}` vs `{"tariff_zone": "offpeak"}` affects
settlement values. The audit trail must show the correction explicitly.

**Q: When reconciliation finds variances across many periods, are adjustments created individually or batched?**

A: The `Reconcile()` function returns a slice of `Variance` objects—one per period with a
difference. The caller decides how to process them:

```go
// Option 1: Individual adjustments (simple, traceable)
for _, v := range variances {
    s.adjustmentService.CreateAdjustment(ctx, v)
}

// Option 2: Batched adjustment (efficient, single entry)
if len(variances) > 0 {
    totalDelta := sumVariances(variances)
    s.adjustmentService.CreateBatchAdjustment(ctx, BatchAdjustment{
        SettlementRunID: runID,
        Variances:       variances,
        TotalDelta:      totalDelta,
        // Individual variances stored as line items for audit
    })
}
```

**Recommendation:** Use batched adjustments per settlement run. This creates a single
financial entry referencing all period-level variances, reducing transaction volume while
preserving full audit detail in the line items.

**Q: How are retroactive Source Authority quality score changes handled?**

A: Quality scores are **denormalized at ingestion time** (`quality_score` column on
measurements). This is intentional:

| Scenario | Behavior |
|----------|----------|
| New source added | New measurements get the new source's score; existing unaffected |
| Existing source score increased | Existing measurements keep original score; new ones get higher |
| Existing source score decreased | Existing measurements keep original score (grandfathered) |

**Why not re-evaluate?** Retroactive score changes could cascade into mass Wash & Reload
operations, destabilizing settled positions and creating reconciliation nightmares.

If a regulatory change genuinely requires retroactive re-ranking:

```go
// Manual re-evaluation job (use with extreme caution)
func (s *MaintenanceService) ReEvaluateSourceQuality(
    ctx context.Context,
    sourceCode string,
    newScore int,
    periodStart, periodEnd time.Time,
) error {
    // 1. Find affected non-superseded measurements
    affected, _ := s.repo.FindBySourceAndPeriod(ctx, sourceCode, periodStart, periodEnd)

    for _, m := range affected {
        if m.QualityScore == newScore {
            continue // Already correct
        }
        // 2. Update denormalized score (rare exception to immutability)
        s.repo.UpdateQualityScore(ctx, m.ID, newScore)
        // 3. Re-run Delta Engine to check if supersession changes
        s.deltaEngine.ReEvaluate(ctx, m)
    }
    return nil
}
```

This is a maintenance operation requiring approval, not automatic behavior. The
`ValidFrom`/`ValidTo` on `SourceAuthority` is for **prospective** changes—"starting next
month, SMETS2 meters are quality 95 instead of 90."

### Reconsidering This Decision

Revisit if:
- Query performance degrades with measurement volume (consider event sourcing)
- Settlement rules require retroactive position mutation (regulatory change)
- Real-time streaming replaces batch settlement (architecture shift)
- Storage costs become prohibitive (consider snapshot compression)
