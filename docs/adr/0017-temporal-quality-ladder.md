---
name: adr-017-temporal-quality-ladder
description: Time-bound quality ladder pattern for temporal asset reconciliation with supersession and wash/reload corrections
triggers:
  - Implementing metered asset tracking (energy, compute, bandwidth)
  - Handling out-of-order data arrival with quality-based precedence
  - Building reconciliation between billed and actual values
  - Designing settlement systems with multiple data quality tiers
instructions: |
  Use the Time-Bound Quality Ladder pattern when tracking assets where the "true" value for a time
  period is not known immediately and may be revised as higher-quality data arrives. Model all
  positions as [Start, End] ranges where Start may equal End for point-in-time events. Implement
  supersession via the Delta Engine, corrections via Wash & Reload saga, and reconciliation via
  Bill Binding comparison.
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
- **Bill binding** for reconciliation

## Decision Drivers

* **Immutable audit trail**: All data received must be preserved, corrections via append not update
* **Financial accuracy**: Bills and positions must reflect best available data
* **Regulatory compliance**: Settlement run deadlines and finality rules
* **Reconciliation capability**: Variance detection between billed and actual
* **Universal applicability**: Same pattern for energy, advertising, aid, carbon

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

func (p Period) Overlaps(other Period) bool {
    return p.Start.Before(other.End) && other.Start.Before(p.End)
}

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
    account_id VARCHAR(50) NOT NULL,
    asset_code VARCHAR(32) NOT NULL,
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
    CONSTRAINT no_overlapping_current CHECK (
        superseded_by IS NOT NULL OR TRUE  -- Enforced via application logic
    )
);

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
    AccountID     string
    AssetCode     string
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
- Current position: 10 kWh (estimate, billed)
- New measurement: 12 kWh (actual)
- Action: Reverse old, book new, net effect +2 kWh

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

### 6. Bill Binding and Reconciliation

When a bill is generated, it captures which measurements were used. This enables
reconciliation when better data arrives later.

```go
// BillBinding records which measurement was used for each period in a bill.
type BillBinding struct {
    ID              uuid.UUID
    BillID          uuid.UUID
    AccountID       string
    AssetCode       string
    Period          Period
    MeasurementID   uuid.UUID       // The measurement used
    QuantityBilled  decimal.Decimal // Snapshot of quantity at billing time
    QualityAtBill   int             // Quality score at billing time
    CreatedAt       time.Time
}

// ReconciliationService compares billed positions to current positions.
type ReconciliationService struct {
    billBindingRepo BillBindingRepository
    measurementRepo MeasurementRepository
    valuationEngine ValuationEngine
}

// Reconcile identifies variances between billed and current positions.
func (s *ReconciliationService) Reconcile(ctx context.Context, billID uuid.UUID) ([]Variance, error) {
    bindings, err := s.billBindingRepo.FindByBill(ctx, billID)
    if err != nil {
        return nil, err
    }

    var variances []Variance
    for _, binding := range bindings {
        current, err := s.measurementRepo.FindCurrent(ctx,
            binding.AccountID,
            binding.AssetCode,
            binding.Period,
            nil, // All attributes
        )
        if err != nil {
            return nil, err
        }

        if !current.Quantity.Equal(binding.QuantityBilled) {
            delta := current.Quantity.Sub(binding.QuantityBilled)

            // Value the variance using the tariff at the original period
            value, err := s.valuationEngine.Valuate(ctx, ValuationRequest{
                AssetCode:  binding.AssetCode,
                Quantity:   delta,
                Period:     binding.Period,
                Attributes: current.Attributes,
            })
            if err != nil {
                return nil, err
            }

            variances = append(variances, Variance{
                BillID:          billID,
                Period:          binding.Period,
                QuantityBilled:  binding.QuantityBilled,
                QuantityCurrent: current.Quantity,
                QuantityDelta:   delta,
                ValueDelta:      value.SettlementAmount,
            })
        }
    }

    return variances, nil
}
```

**Reconciliation creates adjustments, not mutations:**

```
Bill #12345 Reconciliation:

┌─────────────────┬────────────┬─────────────┬───────────┬────────────┐
│ Period          │ Billed kWh │ Current kWh │ Delta kWh │ Delta £    │
├─────────────────┼────────────┼─────────────┼───────────┼────────────┤
│ 12:00-12:30     │ 10.00      │ 12.00       │ +2.00     │ +£0.30     │
│ 12:30-13:00     │ 11.00      │ 10.50       │ -0.50     │ -£0.08     │
│ 13:00-13:30     │ 9.00       │ 9.00        │ 0.00      │ £0.00      │
├─────────────────┼────────────┼─────────────┼───────────┼────────────┤
│ Total           │ 30.00      │ 31.50       │ +1.50     │ +£0.22     │
└─────────────────┴────────────┴─────────────┴───────────┴────────────┘

Action: Create adjustment invoice for £0.22
```

## Service Responsibilities

| Component | Service | Notes |
|-----------|---------|-------|
| Source Authority Registry | Asset Directory | Quality rankings per source |
| Measurement Log | Position Keeping | Append-only, immutable |
| Delta Engine | Position Keeping | Supersession evaluation |
| Correction Saga | Position Keeping | Wash & Reload execution |
| Position Entries | Position Keeping | Net position calculation |
| Bill Binding | Financial Accounting | Snapshot at billing time |
| Reconciliation | Valuation Service | Variance → adjustment value |
| Adjustment Invoice | Payment Order | Financial settlement of variance |

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
* **Ongoing reconciliation**: Bills are never truly "final" until settlement locked

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
CREATE INDEX idx_bill_bindings_bill
    ON bill_bindings(bill_id);
```

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

Use optimistic locking on supersession to prevent race conditions:

```go
func (r *MeasurementRepository) Supersede(ctx context.Context, oldID, newID uuid.UUID) error {
    result := r.db.Model(&Measurement{}).
        Where("id = ? AND superseded_by IS NULL", oldID).  // Only if not already superseded
        Update("superseded_by", newID)

    if result.RowsAffected == 0 {
        return ErrAlreadySuperseded
    }
    return result.Error
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

Configure per-asset in the Asset Directory.

### Valuation Integration

The Valuation Engine (future ADR) must support:
- Temporal tariff lookup (rate at settlement period, not current)
- Attribute-based pricing (peak/off-peak, vintage, grade)
- Multi-period aggregation with different rates per sub-period

### Reconsidering This Decision

Revisit if:
- Query performance degrades with measurement volume (consider event sourcing)
- Settlement rules require retroactive position mutation (regulatory change)
- Real-time streaming replaces batch settlement (architecture shift)
