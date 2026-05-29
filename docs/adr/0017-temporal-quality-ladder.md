---
name: adr-017-temporal-quality-ladder
description: Time-bound quality ladder pattern for temporal asset data with supersession and wash/reload corrections
triggers:
  - Implementing metered asset tracking (energy, compute, bandwidth)
  - Handling out-of-order data arrival with quality-based precedence
  - Storing measurements with audit trail and supersession
  - Designing correction patterns for financial adjustments
instructions: |
  Use the Time-Bound Quality Ladder pattern when tracking assets where the "true" value for a time
  period is not known immediately and may be revised as higher-quality data arrives. Model all
  positions as [Start, End] ranges where Start may equal End for point-in-time events. Implement
  supersession via the Delta Engine, corrections via Wash & Reload saga. For settlement snapshots
  and reconciliation, see ADR-0018.
---

# 17. Temporal Quality Ladder (Data Physics)

Date: 2025-12-14

## Status

Accepted (Implemented)

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

### Relationship to Other ADRs

This ADR builds on:

- **ADR-0013 (Universal Quantity Type System):** Provides `Quantity[D]` with dimensional safety
- **ADR-0014 (Dynamic Asset Registry):** Provides tenant-defined assets with attribute schemas
- **ADR-0016 (Tenant Isolation):** Provides schema-per-tenant isolation

This ADR defines:

- **Quality-based precedence** for competing measurements
- **Temporal modeling** with [Start, End] periods
- **Supersession tracking** for audit trails
- **Wash & Reload** correction pattern

For settlement snapshots and reconciliation workflows, see **ADR-0018 (Settlement & Reconciliation)**.

## Decision Drivers

* **Immutable audit trail**: All data received must be preserved, corrections via append not update
* **Financial accuracy**: Positions must reflect best available data
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
// Uses closed interval semantics [Start, End] matching the ADR's position model.
// Two periods overlap if neither starts after the other ends.
func (p Period) Overlaps(other Period) bool {
    // Closed-interval overlap: neither period starts after the other's end
    // This handles all cases uniformly including instants
    return !p.Start.After(other.End) && !other.Start.After(p.End)
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

**Interval Semantics (Closed `[Start, End]`):**

| Method | Semantics | Rationale |
|--------|-----------|-----------|
| `Overlaps()` | Closed `[Start, End]` | Inclusive boundaries: `[12:00, 12:30]` overlaps `[12:30, 13:00]` at the shared instant `12:30` |
| `Contains()` | Closed `[Start, End]` | Boundary instants belong to their period: `12:30` is "in" `[12:00, 12:30]` |
| Instants | Point `[t, t]` | An instant `[12:30, 12:30]` overlaps itself and any period containing `12:30` |

**Database Compatibility Note:** This ADR uses explicit `period_start` and `period_end` columns
rather than PostgreSQL's native `TSTZRANGE` type. While `TSTZRANGE` provides elegant range
operators (`&&`, `@>`) and GiST indexing, **CockroachDB does not support range types**
([issue #27791](https://github.com/cockroachdb/cockroach/issues/27791), open since 2018).
To maintain compatibility across PostgreSQL, CockroachDB, and YugabyteDB, we use explicit
timestamp columns. The query complexity difference is minimal:

```sql
-- With TSTZRANGE (PostgreSQL/YugabyteDB only)
WHERE period && tstzrange('2025-01-01', '2025-01-02')

-- With explicit columns (works on all databases)
WHERE period_start < '2025-01-02' AND period_end > '2025-01-01'
```

For deployments using PostgreSQL or YugabyteDB exclusively, a future optimization path
could add a computed `TSTZRANGE` column with GiST index as an enterprise-tier feature.

**Database representation using explicit period columns:**

```sql
CREATE TABLE measurements (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id UUID NOT NULL,          -- Tenant isolation (see ADR-0016)
    account_id UUID NOT NULL,         -- References current_accounts.id
    asset_code VARCHAR(32) NOT NULL,  -- References asset_definitions.code
    quantity DECIMAL(38, 18) NOT NULL,

    -- Time as explicit columns (point-in-time has start = end)
    -- All timestamps MUST be in UTC (enforced at application layer)
    period_start TIMESTAMPTZ NOT NULL,
    period_end TIMESTAMPTZ NOT NULL,

    -- Constraint: end must be >= start (equal for instant events)
    CONSTRAINT valid_period CHECK (period_end >= period_start),

    -- Attributes for fungibility
    attributes JSONB NOT NULL DEFAULT '{}',

    -- Quality ladder
    source VARCHAR(50) NOT NULL,
    quality_score INTEGER NOT NULL,

    -- Lifecycle
    received_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    superseded_by UUID REFERENCES measurements(id),
    settlement_run VARCHAR(20),
    locked_at TIMESTAMPTZ
);

-- Note: Overlap prevention is NOT enforced via database constraints.
-- See "Overlap Prevention" section below for application-level enforcement details.

-- Tenant isolation index (critical for performance and security)
CREATE INDEX idx_measurements_tenant ON measurements(tenant_id);

-- Overlap prevention is enforced at the application layer using optimistic
-- concurrency via position_key_hash. Database-level exclusion constraints would
-- require TSTZRANGE (not available on CockroachDB) and cannot handle our composite
-- key with JSONB attributes. The application layer (DeltaEngine) already evaluates
-- overlap as part of its supersession logic, making database-level enforcement redundant.
--
-- See the Overlap Prevention section in Implementation Notes for details.

CREATE INDEX idx_measurements_lookup
    ON measurements(tenant_id, account_id, asset_code, period_start, period_end)
    WHERE superseded_by IS NULL;
```

**Instant Event Handling with Explicit Columns:**

Instant events (where `Start == End`) are simply stored with identical `period_start` and
`period_end` values. No special handling is required:

```sql
-- Insert an instant event (point-in-time)
INSERT INTO measurements (period_start, period_end, ...) VALUES (
    '2025-01-15 12:00:00+00',
    '2025-01-15 12:00:00+00',  -- Same as start = instant
    ...
);

-- Find measurements containing a specific point in time
-- Uses closed interval semantics [start, end]
SELECT * FROM measurements
WHERE period_start <= '2025-01-15 12:15:00+00'
  AND period_end >= '2025-01-15 12:15:00+00';

-- Find overlapping periods (closed interval overlap)
-- Two periods overlap if neither starts after the other ends
SELECT * FROM measurements
WHERE period_start <= '2025-01-15 12:30:00+00'
  AND period_end >= '2025-01-15 12:00:00+00';
```

**Period Query Integration Tests (Required):**

Before production deployment, verify period query behavior with integration tests:

```go
func TestPeriodQueries(t *testing.T) {
    db := setupTestDB(t)

    tests := []struct {
        name     string
        setup    string
        query    string
        expected bool
    }{
        {
            name:     "point-in-range query works",
            setup:    "INSERT INTO measurements (period_start, period_end) VALUES ('2025-01-15 12:00+00', '2025-01-15 13:00+00')",
            query:    "SELECT EXISTS(SELECT 1 FROM measurements WHERE period_start <= '2025-01-15 12:30+00' AND period_end >= '2025-01-15 12:30+00')",
            expected: true,
        },
        {
            name:     "instant-to-instant overlap detected",
            setup:    "INSERT INTO measurements (period_start, period_end) VALUES ('2025-01-15 12:00+00', '2025-01-15 12:00+00')",
            query:    "SELECT EXISTS(SELECT 1 FROM measurements WHERE period_start <= '2025-01-15 12:00+00' AND period_end >= '2025-01-15 12:00+00')",
            expected: true,
        },
        {
            name:     "range overlap detection",
            setup:    "INSERT INTO measurements (period_start, period_end) VALUES ('2025-01-15 12:00+00', '2025-01-15 13:00+00')",
            query:    "SELECT EXISTS(SELECT 1 FROM measurements WHERE period_start <= '2025-01-15 12:30+00' AND period_end >= '2025-01-15 12:00+00')",
            expected: true,
        },
        {
            name:     "boundary point overlap (closed interval)",
            setup:    "INSERT INTO measurements (period_start, period_end) VALUES ('2025-01-15 12:00+00', '2025-01-15 13:00+00')",
            query:    "SELECT EXISTS(SELECT 1 FROM measurements WHERE period_start <= '2025-01-15 14:00+00' AND period_end >= '2025-01-15 13:00+00')",
            expected: true, // Periods [12:00, 13:00] and [13:00, 14:00] share boundary at 13:00
        },
        {
            name:     "non-overlapping ranges",
            setup:    "INSERT INTO measurements (period_start, period_end) VALUES ('2025-01-15 12:00+00', '2025-01-15 13:00+00')",
            query:    "SELECT EXISTS(SELECT 1 FROM measurements WHERE period_start <= '2025-01-15 11:00+00' AND period_end >= '2025-01-15 10:00+00')",
            expected: false,
        },
        {
            name:     "B-tree index used for period queries",
            setup:    "INSERT INTO measurements (period_start, period_end) VALUES ('2025-01-15 12:00+00', '2025-01-15 13:00+00')",
            query:    "EXPLAIN SELECT * FROM measurements WHERE period_start <= '2025-01-15 12:30+00' AND period_end >= '2025-01-15 12:00+00'",
            expected: true, // Should show "Index Scan using idx_measurements_lookup"
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
3. Range overlap detection with closed interval semantics (`<=` and `>=`)
4. Boundary point overlap (periods sharing only an endpoint are considered overlapping)
5. B-tree composite index is used for period queries

### 2. Source Authority Registry

The **Source Authority Registry** is the canonical source for quality rankings. Measurements
MAY carry a denormalized snapshot of the quality score at ingestion for query performance,
but this cached value can diverge if the registry is updated post-ingestion.

**Important:** All authoritative decisions (supersession, dispute routing) must consult
the registry rather than relying solely on denormalized measurement values. The denormalized
`QualityScore` field on measurements is an optimization for read-heavy queries, not the
source of truth.

```go
// SourceAuthority defines the quality ranking for a data source.
// Stored in the Asset Directory service.
type SourceAuthority struct {
    Code            string    // "SMETS2_METER", "FORECAST_ML"
    AssetCode       string    // Source rankings can be asset-specific
    QualityScore    int       // Higher = more authoritative (0-100)
    Description     string
    ValidFrom       time.Time
    ValidTo         *time.Time // Null = currently valid
}
```

**Example Source Quality Rankings:**

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

**Note:** For temporal authority (forecasts vs actuals based on time context), see ADR-0018.

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
    // Find existing current measurement for this position key.
    // Note: tenant_id is extracted from ctx per project conventions (see ADR-0016).
    // All repository methods enforce tenant isolation via ctx.
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

**Defensive Tests (Per ADR-0008):**

The CorrectionSaga requires comprehensive unhappy-path testing:

```go
func TestCorrectionSaga_Execute_DefensiveTests(t *testing.T) {
    tests := []struct {
        name        string
        scenario    string
        setup       func(*testing.T, *testDB) (*Measurement, *Measurement)
        wantErr     error
        wantRollback bool
    }{
        {
            name:     "old measurement already superseded",
            scenario: "Prevent double-supersession when concurrent process wins",
            setup: func(t *testing.T, db *testDB) (*Measurement, *Measurement) {
                old := createMeasurement(t, db, withSupersededBy(uuid.New()))
                new := createMeasurement(t, db)
                return old, new
            },
            wantErr:     ErrAlreadySuperseded,
            wantRollback: true,
        },
        {
            name:     "old and new have different account IDs",
            scenario: "Cross-account corruption check",
            setup: func(t *testing.T, db *testDB) (*Measurement, *Measurement) {
                old := createMeasurement(t, db, withAccountID(uuid.New()))
                new := createMeasurement(t, db, withAccountID(uuid.New())) // Different!
                return old, new
            },
            wantErr:     ErrCrossAccountCorrection,
            wantRollback: true,
        },
        {
            name:     "transaction timeout during wash entry creation",
            scenario: "Ensure full rollback on timeout mid-transaction",
            setup: func(t *testing.T, db *testDB) (*Measurement, *Measurement) {
                old := createMeasurement(t, db)
                new := createMeasurement(t, db)
                db.setLatency(10 * time.Second) // Trigger timeout
                return old, new
            },
            wantErr:     context.DeadlineExceeded,
            wantRollback: true,
        },
        {
            name:     "old measurement is locked (final settlement)",
            scenario: "Cannot correct locked positions",
            setup: func(t *testing.T, db *testDB) (*Measurement, *Measurement) {
                old := createMeasurement(t, db, withLockedAt(time.Now()))
                new := createMeasurement(t, db)
                return old, new
            },
            wantErr:     ErrPositionLocked,
            wantRollback: true,
        },
    }

    for _, tt := range tests {
        t.Run(tt.name, func(t *testing.T) {
            ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
            defer cancel()

            db := newTestDB(t)
            old, new := tt.setup(t, db)

            saga := NewCorrectionSaga(db)
            err := saga.Execute(ctx, old, new)

            require.ErrorIs(t, err, tt.wantErr, "scenario: %s", tt.scenario)

            if tt.wantRollback {
                // Verify no partial state: old not superseded, no entries created
                assertNotSuperseded(t, db, old.ID)
                assertNoEntriesFor(t, db, new.ID)
            }
        })
    }
}
```

## Service Responsibilities

| Component | Service | Notes |
|-----------|---------|-------|
| Source Authority Registry | Asset Directory | Quality rankings per source |
| Measurement Log | Position Keeping | Append-only, immutable |
| Delta Engine | Position Keeping | Supersession evaluation |
| Correction Saga | Position Keeping | Wash & Reload execution |
| Position Entries | Position Keeping | Net position calculation |

For settlement snapshots and reconciliation service responsibilities, see ADR-0018.

## Consequences

### Positive

* **Complete audit trail**: Every measurement preserved, corrections are explicit entries
* **Clear lineage**: Supersession chain shows estimate → read → actual progression
* **Financial accuracy**: Positions reflect best available data at any point in time
* **Universal pattern**: Same model for energy, advertising, aid, carbon, compute

### Negative

* **Storage growth**: All measurements kept forever (required for audit)
* **Query complexity**: "Current" position requires filtering superseded records
* **Materialized views**: May need for performance at scale

## Implementation Notes

### Database Indexes for Performance

```sql
-- Fast lookup of current measurement for a position
CREATE INDEX idx_measurements_current
    ON measurements(account_id, asset_code, period_start, period_end)
    WHERE superseded_by IS NULL;

-- Period overlap queries (B-tree composite index)
-- CockroachDB doesn't support GiST indexes for TSTZRANGE, so we use B-tree
-- Query pattern: WHERE period_start < ? AND period_end > ?
CREATE INDEX idx_measurements_period
    ON measurements(period_start, period_end);

-- Attribute-based reporting and analytics queries
-- Note: CockroachDB supports GIN indexes for JSONB
CREATE INDEX idx_measurements_attributes
    ON measurements USING GIN (attributes);
```

### Overlap Prevention

Preventing overlapping current positions uses **optimistic concurrency** rather than
pessimistic locking. This is critical for the 100k TPS throughput requirement—SERIALIZABLE
isolation would cause unacceptable contention.

**Strategy: Position Key Hash with Unique Constraint**

```sql
-- CockroachDB does not support PL/pgSQL in UDFs, so canonicalize_jsonb()
-- is implemented at the Go application layer. The position_key_hash column
-- is computed by the repository on INSERT rather than via GENERATED ALWAYS AS.

-- Migration 1: Add hash column (application-computed, not generated)
ALTER TABLE measurements ADD COLUMN position_key_hash BYTEA;
-- The hash is: sha256(account_id || '|' || asset_code || '|' ||
--   canonicalize_jsonb(attributes) || '|' || period_start || '|' || period_end)
-- Computed by MeasurementRepository.Insert() in Go.
```

```sql
-- Migration 2 (separate file): Add unique index after column is public
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

**High-Contention Position Locking (Optional):**

For positions with extremely high write frequency (e.g., real-time meter aggregations),
the optimistic retry loop may cause excessive contention. In these cases, use distributed
locking before Delta Engine evaluation:

```go
func (s *MeasurementIngestionService) IngestWithLock(ctx context.Context, m *Measurement) error {
    // 1. Compute position key hash (same as position_key_hash column)
    keyHash := computePositionKeyHash(m.AccountID, m.AssetCode, m.Period, m.Attributes)

    // 2. Acquire distributed lock with short TTL
    lockKey := fmt.Sprintf("position-lock:%x", keyHash)
    acquired, err := s.distributedLock.TryAcquire(ctx, lockKey, 5*time.Second)
    if err != nil {
        return fmt.Errorf("lock acquisition failed: %w", err)
    }
    if !acquired {
        return ErrConcurrentIngestion // Caller should retry with backoff
    }
    defer s.distributedLock.Release(ctx, lockKey)

    // 3. Now safe to evaluate - we have exclusive access to this position
    action, existing, err := s.deltaEngine.Evaluate(ctx, m)
    if err != nil {
        return err
    }

    // 4. Execute action under lock
    switch action {
    case ActionWashAndReload:
        return s.correctionSaga.Execute(ctx, existing, m)
    // ... other cases ...
    }
    return nil
}
```

**When to use distributed locking vs optimistic retry:**

| Scenario | Recommended Approach |
|----------|---------------------|
| Normal ingestion (<100 writes/sec/position) | Optimistic retry |
| High-frequency aggregation (>100 writes/sec/position) | Distributed lock |
| Batch imports | Distributed lock per position key |
| Settlement run locking | Distributed lock (prevents concurrent finalization) |

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
)
```

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

## Glossary

| Term | Definition |
|------|------------|
| **Quality Ladder** | Hierarchy of data sources ranked by authority (e.g., estimate < customer read < meter actual) |
| **Wash & Reload** | Correction pattern: reverse old position entry, book new entry, preserving audit trail |
| **Supersession** | Replacement of one measurement by another of higher quality for the same position |
| **Position Key** | Composite identifier: (tenant_id, account_id, asset_code, period, attributes) |
| **Delta Engine** | Decision component that evaluates incoming measurements against current state |

For settlement and reconciliation terms, see ADR-0018.

## BIAN v14 Alignment

This ADR's concepts map to BIAN (Banking Industry Architecture Network) v14 service domains:

| Meridian Concept | BIAN Service Domain | BIAN Entity | Notes |
|-----------------|---------------------|-------------|-------|
| `MeasurementLog` | Position Keeping | `FinancialPositionLog` (CR) | "Maintain a log of transactions" |
| `Measurement` | Position Keeping | `FinancialTransactionCapture` (BQ) | Transaction capture behavior |
| `Period [Start, End]` | Position Keeping | `datetimeperiod` | `FromDateTime` / `ToDateTime` |
| `TransactionStatus` | Position Keeping | `transactionstatustypevalues` | Initiated, Booked, Confirmed, etc. |
| `PositionEntry` | Financial Accounting | `LedgerPosting` (BQ) | Ledger posting behavior |
| Correction booking | Financial Accounting | `UpdateLedgerPosting` | Explicitly for "repair" |
| `EntryType.ADJUSTMENT` | Position Keeping | `InterestAdjustmentTransaction` | Adjustment transaction type |

**BIAN Types Referenced (from `PositionKeeping.yaml`):**

```yaml
# Transaction lifecycle status
transactionstatustypevalues:
  - Initiated, Executed, Cancelled, Confirmed
  - Suspended, Pending, Completed, Notified, Booked, Rejected

# Amount quality indicators (partial quality ladder)
amounttypevalues:
  - Estimated    # Low quality
  - Actual       # High quality
  - Principal, Maximum, Default, Replacement, Reserved, Available

# Adjustment transaction types
interesttransactiontypevalues:
  - InterestAllocationTransaction
  - InterestPaymentTransaction
  - InterestAdjustmentTransaction  # Correction pattern
```

**Extensions Beyond BIAN Standard:**

The following Meridian concepts have no direct BIAN equivalent:

- **Quality Ladder / Source Authority Registry**: BIAN's `amounttypevalues` has `Estimated` vs
  `Actual`, but lacks the full quality ranking hierarchy
- **Supersession chain** (`superseded_by`): BIAN transactions have status but not supersession
- **Quality-based precedence rules**: The Delta Engine's decision logic is Meridian-specific
- **Wash & Reload saga**: BIAN has adjustment transactions but not the atomic correction pattern

## Links

### Internal ADRs

* [ADR-0013: Universal Quantity Type System](0013-generic-asset-quantity-types.md) - Quantity and rate types
* [ADR-0014: Financial Instrument Reference Data](0014-financial-instrument-reference-data.md) - Asset definitions and attributes
* [ADR-0016: Tenant Isolation](0016-tenant-id-naming-strategy.md) - Multi-tenancy and schema separation
* [ADR-0018: Settlement & Reconciliation](0018-settlement-reconciliation.md) - Settlement snapshots, temporal authority, reconciliation workflows

### External References

* [UK Balancing and Settlement Code (BSC)](https://www.elexon.co.uk/bsc-and-codes/)
