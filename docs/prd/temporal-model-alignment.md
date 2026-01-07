---
name: prd-temporal-model-alignment
description: Align position-keeping temporal model with ADR-0017 explicit period columns
triggers:
  - Working on measurement period ranges or time bounds
  - Implementing temporal overlap or containment queries
  - Refactoring bucket_id or timestamp fields in position-keeping
instructions: |
  The current measurement model uses a single Timestamp field and opaque bucket_id hash.
  ADR-0017 specifies explicit period_start/period_end columns for temporal algebra.
  This PRD tracks the migration to align implementation with the ADR specification.
---

# Temporal Model Alignment with ADR-0017

Date: 2026-01-06

## Status

Draft

## Overview

### Problem Statement

The current `measurement` table and domain model in position-keeping diverge from ADR-0017's temporal design:

| Aspect | ADR-0017 Specification | Current Implementation |
|--------|------------------------|------------------------|
| Time fields | `period_start`, `period_end` | Single `timestamp` |
| Point-in-time | `start == end` | Implicit (single field) |
| Temporal queries | SQL algebra (`start <= ? AND end >= ?`) | Not possible |
| Indexing | B-tree on `(period_start, period_end)` | Index on `timestamp` only |

The opaque `bucket_id` hash serves fungibility grouping but prevents:

- Overlap detection: "Does period A overlap period B?"
- Containment queries: "Is instant T within period P?"
- Range aggregation: "Sum all measurements in time window W"

**Note:** The `Period` type already exists in `internal/audit-consumer/domain/measurement.go`
and aligns with ADR-0017. The position-keeping service needs to adopt this pattern.

### Goals

1. Add explicit `period_start` and `period_end` columns to `measurement` table
2. Migrate existing `timestamp` data (treat as instant: `start = end = timestamp`)
3. Update domain model with `Period` type per ADR-0017
4. Enable temporal SQL queries with proper indexing
5. Retain `bucket_id` for fungibility grouping (orthogonal concern)

### Non-Goals

- Removing `bucket_id` (still needed for fungibility aggregation)
- Implementing full bi-temporal model (valid time + transaction time)
- Adding PostgreSQL `TSTZRANGE` type (CockroachDB incompatible per ADR-0017)

## Requirements

### Functional Requirements

| ID | Requirement | Priority |
|----|-------------|----------|
| FR-1 | Measurements support explicit period ranges `[start, end]` | P0 |
| FR-2 | Point-in-time events stored as `start == end` | P0 |
| FR-3 | Repository supports overlap queries | P0 |
| FR-4 | Repository supports containment queries | P0 |
| FR-5 | Existing timestamp data migrated without data loss | P0 |
| FR-6 | API accepts both instant and period measurements | P1 |

### Non-Functional Requirements

| ID | Requirement | Target |
|----|-------------|--------|
| NFR-1 | Period queries use index (no full table scan) | EXPLAIN shows Index Scan |
| NFR-2 | Migration completes without downtime | Zero-downtime deployment |
| NFR-3 | Backward compatibility for existing API consumers | No breaking changes |

## Design

### Database Schema Changes

```sql
-- Migration: Add explicit period columns per ADR-0017
ALTER TABLE "measurement"
  ADD COLUMN "period_start" timestamptz NULL,
  ADD COLUMN "period_end" timestamptz NULL;

-- Backfill: Existing timestamp becomes instant (start = end)
UPDATE "measurement"
SET period_start = timestamp,
    period_end = timestamp
WHERE period_start IS NULL;

-- Enforce non-null after backfill
ALTER TABLE "measurement"
  ALTER COLUMN "period_start" SET NOT NULL,
  ALTER COLUMN "period_end" SET NOT NULL;

-- Validity constraint per ADR-0017
ALTER TABLE "measurement"
  ADD CONSTRAINT "chk_measurement_valid_period"
  CHECK (period_end >= period_start);

-- Composite index for temporal queries
-- Enables: WHERE period_start <= ? AND period_end >= ?
CREATE INDEX "idx_measurement_period"
  ON "measurement" (period_start, period_end);

-- Update existing index to include period columns
DROP INDEX IF EXISTS "idx_measurement_position_state_timestamp";
CREATE INDEX "idx_measurement_position_period"
  ON "measurement" ("financial_position_log_id", "period_start", "period_end");
```

### Domain Model Changes

The `Period` type is already implemented in `internal/audit-consumer/domain/measurement.go`.
This PRD refactors position-keeping to use the same pattern:

```go
// Period represents a time range per ADR-0017.
// For point-in-time events, Start equals End.
// All timestamps MUST be in UTC.
type Period struct {
    Start time.Time
    End   time.Time
}

// NewPeriod creates a validated Period.
func NewPeriod(start, end time.Time) (Period, error) {
    if start.Location() != time.UTC {
        return Period{}, errors.New("period start must be in UTC")
    }
    if end.Location() != time.UTC {
        return Period{}, errors.New("period end must be in UTC")
    }
    if end.Before(start) {
        return Period{}, ErrInvalidPeriod
    }
    return Period{Start: start, End: end}, nil
}

// Instant creates a point-in-time period where Start == End.
func Instant(t time.Time) (Period, error) {
    return NewPeriod(t, t)
}

// IsInstant returns true if this is a point-in-time (Start == End).
func (p Period) IsInstant() bool {
    return p.Start.Equal(p.End)
}

// Overlaps returns true if this period shares any time with another.
// Uses closed interval semantics [Start, End].
func (p Period) Overlaps(other Period) bool {
    return !p.Start.After(other.End) && !other.Start.After(p.End)
}

// Contains returns true if the given instant falls within this period.
func (p Period) Contains(t time.Time) bool {
    return !t.Before(p.Start) && !t.After(p.End)
}

// Measurement updated to use Period instead of Timestamp
type Measurement struct {
    ID                     uuid.UUID
    FinancialPositionLogID uuid.UUID
    MeasurementType        MeasurementType
    Value                  decimal.Decimal
    Unit                   string
    Period                 Period            // Replaces Timestamp
    Metadata               map[string]string
    BucketID               string            // Retained for fungibility
    CreatedAt              time.Time
    CreatedBy              string
    UpdatedAt              time.Time
    UpdatedBy              string
}
```

### Repository Query Patterns

```go
// FindOverlapping returns measurements whose period overlaps the given range.
// SQL: WHERE period_start <= ? AND period_end >= ?
func (r *MeasurementRepository) FindOverlapping(
    ctx context.Context,
    positionLogID uuid.UUID,
    queryPeriod Period,
) ([]*Measurement, error) {
    var measurements []*Measurement
    err := r.db.WithContext(ctx).
        Where("financial_position_log_id = ?", positionLogID).
        Where("period_start <= ?", queryPeriod.End).
        Where("period_end >= ?", queryPeriod.Start).
        Find(&measurements).Error
    return measurements, err
}

// FindContaining returns measurements whose period contains the given instant.
// SQL: WHERE period_start <= ? AND period_end >= ?
func (r *MeasurementRepository) FindContaining(
    ctx context.Context,
    positionLogID uuid.UUID,
    instant time.Time,
) ([]*Measurement, error) {
    var measurements []*Measurement
    err := r.db.WithContext(ctx).
        Where("financial_position_log_id = ?", positionLogID).
        Where("period_start <= ?", instant).
        Where("period_end >= ?", instant).
        Find(&measurements).Error
    return measurements, err
}
```

### API Backward Compatibility

The existing `RecordMeasurement` API accepts a single timestamp. For backward compatibility:

```go
// RecordMeasurementRequest - existing field retained
type RecordMeasurementRequest struct {
    // Existing: single timestamp (treated as instant)
    Timestamp time.Time `json:"timestamp"`

    // New: explicit period (optional, takes precedence if provided)
    PeriodStart *time.Time `json:"period_start,omitempty"`
    PeriodEnd   *time.Time `json:"period_end,omitempty"`
}

// Service logic for backward compatibility
func (s *Service) toPeriod(req RecordMeasurementRequest) (Period, error) {
    // New explicit period takes precedence
    if req.PeriodStart != nil && req.PeriodEnd != nil {
        return NewPeriod(*req.PeriodStart, *req.PeriodEnd)
    }
    // Fall back to timestamp as instant
    return Instant(req.Timestamp)
}
```

## Work Streams

### 1. Database Migration

| Task | Description | Effort |
|------|-------------|--------|
| 1.1 | Create migration adding `period_start`, `period_end` columns (nullable) | S |
| 1.2 | Create backfill migration (`period_start = period_end = timestamp`) | S |
| 1.3 | Create migration to set NOT NULL and add constraint | S |
| 1.4 | Create composite index `idx_measurement_period` | S |
| 1.5 | Update existing position+timestamp index to position+period | S |

**Testing Strategy:**

- Run migrations against test database with production-like data volume
- Verify zero rows have NULL period columns after backfill
- Verify constraint rejects `end < start` inserts
- Use `EXPLAIN ANALYZE` to confirm index usage

### 2. Domain Model Refactoring

| Task | Description | Effort |
|------|-------------|--------|
| 2.1 | Move/extract `Period` type to shared package or position-keeping domain | S |
| 2.2 | Add `Overlaps()`, `Contains()`, `IsInstant()` methods if not present | S |
| 2.3 | Update `Measurement` struct: replace `Timestamp` with `Period` | M |
| 2.4 | Update `NewMeasurement()` constructor to accept `Period` | S |
| 2.5 | Add `NewMeasurementInstant()` convenience constructor | S |

**Testing Strategy:**

```go
func TestPeriod_Overlaps(t *testing.T) {
    tests := []struct {
        name     string
        a, b     Period
        expected bool
    }{
        {"adjacent periods share boundary", period(12, 13), period(13, 14), true},
        {"non-overlapping", period(12, 13), period(14, 15), false},
        {"instant overlaps containing period", instant(12, 30), period(12, 13), true},
        {"instant-to-instant same time", instant(12, 0), instant(12, 0), true},
        {"instant-to-instant different time", instant(12, 0), instant(13, 0), false},
    }
    // ...
}
```

### 3. Repository Layer Updates

| Task | Description | Effort |
|------|-------------|--------|
| 3.1 | Update GORM model tags for `period_start`, `period_end` | S |
| 3.2 | Add `FindOverlapping()` repository method | S |
| 3.3 | Add `FindContaining()` repository method | S |
| 3.4 | Update existing queries to use period columns | M |
| 3.5 | Deprecate timestamp-only query methods | S |

**Testing Strategy:**

- Integration tests against real PostgreSQL with test data
- Verify `EXPLAIN` shows `Index Scan using idx_measurement_period`
- Test edge cases: boundary overlaps, instant containment, empty results

### 4. Service Layer Updates

| Task | Description | Effort |
|------|-------------|--------|
| 4.1 | Update `RecordMeasurement` to accept optional period | M |
| 4.2 | Add backward compatibility: timestamp-only → instant period | S |
| 4.3 | Update aggregation logic to use period-aware queries | M |
| 4.4 | Add validation for period inputs in request handlers | S |

**Testing Strategy:**

- Unit tests for `toPeriod()` backward compatibility logic
- Integration tests for full request→response flow
- Verify existing API consumers continue working (no timestamp-only failures)

### 5. Cleanup and Documentation

| Task | Description | Effort |
|------|-------------|--------|
| 5.1 | Remove deprecated `Timestamp` field from domain model | S |
| 5.2 | Drop legacy `timestamp` column (after migration verification) | S |
| 5.3 | Update API documentation with period fields | S |
| 5.4 | Add ADR-0017 implementation notes | S |

**Testing Strategy:**

- Full regression test suite passes
- API documentation matches implementation
- No references to removed `Timestamp` field in codebase

## Migration Strategy

### Zero-Downtime Deployment

1. **Phase 1: Additive** (backward compatible)
   - Deploy migration adding nullable `period_start`, `period_end`
   - Deploy code that writes both `timestamp` AND `period_*` columns
   - Existing reads continue using `timestamp`

2. **Phase 2: Backfill**
   - Run backfill migration in batches
   - `UPDATE measurement SET period_start = timestamp, period_end = timestamp WHERE period_start IS NULL LIMIT 10000`
   - Monitor for lock contention

3. **Phase 3: Switch Reads**
   - Deploy code that reads from `period_*` columns
   - Falls back to `timestamp` if period columns NULL (safety)

4. **Phase 4: Enforce**
   - Verify zero NULL period columns
   - Add NOT NULL constraint
   - Add validity CHECK constraint

5. **Phase 5: Cleanup**
   - Stop writing to `timestamp` column
   - Drop `timestamp` column in future release

## Success Metrics

| Metric | Target | Measurement |
|--------|--------|-------------|
| Period queries use index | 100% | `EXPLAIN ANALYZE` shows Index Scan |
| Migration data loss | 0 rows | `COUNT(*) WHERE period_start IS NULL` = 0 |
| API backward compatibility | 100% | Existing timestamp-only requests succeed |
| Test coverage for Period type | > 90% | `go test -cover` |

## Dependencies

- **ADR-0017**: Temporal Quality Ladder (defines the Period model)
- **ADR-0002**: Service isolation (no cross-schema joins)
- **Universal Asset System PRD**: bucket_id for fungibility (orthogonal)

## Risks and Mitigations

| Risk | Impact | Mitigation |
|------|--------|------------|
| Large table backfill causes locks | High | Batch updates with `LIMIT`, run during low traffic |
| Existing queries break | High | Phased rollout with fallback to timestamp |
| Index bloat from new columns | Medium | Monitor table size, consider partial index |
| CockroachDB compatibility | High | Use explicit columns per ADR-0017 (no TSTZRANGE) |

## Related Documents

- [ADR-0017: Temporal Quality Ladder](../adr/0017-temporal-quality-ladder.md)
- [Universal Asset System PRD](universal-asset-system.md)
