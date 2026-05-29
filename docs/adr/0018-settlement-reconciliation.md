---
name: adr-018-settlement-reconciliation
description: Settlement lifecycle, temporal authority, reconciliation workflows, and dispute handling
triggers:
  - Implementing settlement runs with finality windows
  - Building reconciliation between settled and actual values
  - Handling provisional vs final settlement states
  - Managing disputes for locked positions
instructions: |
  Use this ADR when implementing settlement workflows that capture measurement state at settlement
  time, reconcile variances when better data arrives, and manage the transition from provisional
  to final settlement. For the underlying data model (measurements, supersession, quality ladder),
  see ADR-0017.
---

# 18. Settlement & Reconciliation (Lifecycle)

Date: 2025-12-14

## Status

Accepted (Implemented)

## Context

This ADR defines the **lifecycle** aspects of temporal asset management:

- **When** measurements become authoritative (Temporal Authority)
- **How** financial positions are captured for settlement (Settlement Snapshots)
- **How** variances are detected and resolved (Reconciliation)
- **What** happens after final settlement (Disputes)

This ADR builds on **ADR-0017 (Temporal Quality Ledger)** which defines the underlying
data physics: the measurement log, quality ladder, supersession, and wash/reload correction pattern.

### Real-World Settlement Patterns

Energy industry settlement runs demonstrate the need for lifecycle management:

| Run | Timing | Data Quality | Finality |
|-----|--------|--------------|----------|
| D+1 | Day after | Estimates, some actuals | Provisional |
| D+5 | 5 days after | Most actuals | Provisional |
| M+3 | 3 months after | Validated actuals | Near-final |
| M+14 | 14 months after | Fully reconciled | **Final** |

After final settlement, corrections become disputes rather than automatic adjustments.

### The Temporal Authority Problem

Most ledgers treat the database as "The State of Now." Meridian manages the **Entire Timeline
of Value** - past (audit, integrity, settlement) AND future (forecasts, commitments, hedging).

**The Key Insight:** A Forecast is the **Golden Source** for tomorrow, but **Garbage** for
yesterday. Without modeling this "Temporal Weighting," you get the classic billing disaster:

1. Bill customer based on Forecast (because Actuals weren't in yet)
2. Actuals arrive
3. System overwrites Forecast
4. **Bug:** You've lost the record of *why* you billed that amount. The variance is inexplicable.

### Relationship to Other ADRs

- **ADR-0017 (Temporal Quality Ledger):** Data model foundation - measurements, quality ladder, supersession
- **ADR-0013 (Universal Quantity Type System):** `Quantity[D]` with dimensional safety
- **ADR-0014 (Dynamic Asset Registry):** Tenant-defined assets with attribute schemas
- **ADR-0016 (Tenant Isolation):** Schema-per-tenant isolation

## Decision Drivers

* **Regulatory compliance**: Settlement run deadlines and finality rules
* **Reconciliation capability**: Variance detection between settled and actual
* **Provisional settlement**: Support forward-looking use cases (budgeting, hedging)
* **Audit trail**: Preserve the basis of financial decisions for variance explanation
* **Cross-service isolation**: Financial Accounting owns snapshots, Position Keeping owns measurements

## Decision Outcome

Implement settlement lifecycle management with four components:

1. **Temporal Authority**: Quality rankings that vary by time context (past vs future)
2. **Settlement Snapshots**: Capture measurement state with provenance at settlement
3. **Reconciliation Service**: Detect and value variances between settlements
4. **Dispute Workflow**: Handle corrections after final settlement

### 1. Temporal Authority

Authority is relative to time. Source Authority (from ADR-0017) is extended with temporal context:

```go
// SourceAuthority defines the quality ranking for a data source.
// Extended with TemporalContext for time-relative authority.
type SourceAuthority struct {
    Code            string    // "SMETS2_METER", "FORECAST_ML"
    AssetCode       string    // Source rankings can be asset-specific
    QualityScore    int       // Higher = more authoritative (0-100)
    TemporalContext string    // "PAST", "FUTURE", or "ANY" - when this source is authoritative
    Description     string
    ValidFrom       time.Time
    ValidTo         *time.Time // Null = currently valid
}
```

**Source Authority with Temporal Context:**

| Source Code | Quality Score | Temporal Context | Description |
|-------------|---------------|------------------|-------------|
| `FORECAST_ML` | 50 | **Future** | ML prediction. Authoritative for T+1 onwards |
| `FORECAST_BUDGET` | 40 | **Future** | Budget allocation. Authoritative until actual spend |
| `DEFAULT_PROFILE` | 10 | Past | Regulatory default when no data |
| `ESTIMATED_HISTORIC` | 20 | Past | Same period last year |
| `ESTIMATED_PROFILE` | 30 | Past | Profile coefficient calculation |
| `CUSTOMER_READ` | 50 | Past | Customer-submitted reading |
| `ACTUAL_UNVALIDATED` | 70 | Past | Meter reading, not yet validated |
| `ACTUAL_VALIDATED` | 90 | Past | Meter reading, passed validation |
| `ACTUAL_FINAL` | 100 | Past | Final settlement reading |

**Temporal Authority Rules:**

- For **Past periods** (T ≤ Now): Only `TemporalContext: Past` sources compete. Actuals always win.
- For **Future periods** (T > Now): Only `TemporalContext: Future` sources are valid. Forecasts win by default.
- When time advances and Actuals arrive, they supersede Forecasts automatically.

**Temporal Authority Evaluation:**

Authority is evaluated **at query time**, not proactively:

- When a measurement is ingested, `DeltaEngine` compares `Period.End` against `time.Now()`
  to determine temporal context (past vs future)
- When a settlement run executes, it evaluates authority for each period based on the
  run's effective date
- There is no scheduled process to "flip" authority when time advances

**Rationale:** Settlement runs are the natural business boundary where authority matters.
Proactive re-evaluation would require scheduling infrastructure and produce events that
may have no consumer. Components querying for "current authoritative value" must evaluate
temporal context at query time. The `SourceAuthority` registry provides the rules; the
consumer applies them.

### 2. Provisional Settlement

When a financial action is taken based on non-final data (Forecast, Estimate), we **Freeze**
the basis of that decision into the Settlement Snapshot. This enables accurate reconciliation:

```go
// ProvisionalSettlement creates a settlement based on forecast data
func (s *SettlementService) CreateProvisionalSettlement(
    ctx context.Context,
    forecast *Measurement,
) (*SettlementSnapshot, error) {
    if forecast.Source != "FORECAST_ML" && forecast.Source != "FORECAST_BUDGET" {
        return nil, errors.New("provisional settlement requires forecast source")
    }

    snapshot := &SettlementSnapshot{
        ID:              uuid.New(),
        MeasurementID:   forecast.ID,
        QuantitySettled: forecast.Quantity,
        QualityAtSettle: forecast.QualityScore,
        SourceAtSettle:  forecast.Source,        // "FORECAST_ML"
        SettlementType:  SettlementProvisional,  // Subject to reconciliation
        CreatedAt:       time.Now(),
    }

    return snapshot, s.snapshotRepo.Create(ctx, snapshot)
}
```

**Cross-Domain Applications:**

This "Provisional Settlement" pattern enables high-value capabilities across all target sectors:

| Domain | Forecast Source | Provisional Action | Reconciliation Trigger |
|--------|-----------------|-------------------|----------------------|
| **Energy** | ML demand forecast | Book hedge position | Smart meter actual |
| **NGO/Gov** | Refugee estimate | Allocate budget | Biometric headcount |
| **Compute** | Reservation request | Reserve GPU hours | Actual job runtime |
| **Treasury** | FX rate forecast | Book forward contract | Market settlement rate |

**Example: NGO Budget Allocation**

```
T-30 (Forecast):
  Source: FORECAST_BUDGET (Quality 40)
  Quantity: 1,000 refugees expected
  Action: Provisional Settlement - Transfer $10,000 to Field Wallet
  Snapshot: { source: "FORECAST_BUDGET", quantity: 1000, type: "PROVISIONAL" }

T+1 (Actual):
  Source: BIOMETRIC_COUNT (Quality 95)
  Quantity: 800 refugees arrived
  Action: Reconciliation
    - Current (800) vs Snapshot (1,000)
    - Variance: -200 refugees = -$2,000
    - Reason: "FORECAST_DEVIATION"
  Output: Payment Order - Return $2,000 to HQ
```

### 3. Settlement Snapshots

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

    // Provenance (The "Freeze") - enables variance analysis for provisional settlements
    SourceAtSettle   string            // e.g., "FORECAST_ML", "ACTUAL_VALIDATED"
    SettlementType   SettlementType    // PROVISIONAL or FINAL
}

type SettlementType string

const (
    // SettlementProvisional indicates settlement based on non-final data (forecasts, estimates).
    // Subject to reconciliation when actuals arrive.
    SettlementProvisional SettlementType = "PROVISIONAL"

    // SettlementFinal indicates settlement based on final, audited data.
    // No further reconciliation expected.
    SettlementFinal SettlementType = "FINAL"
)
```

### 4. Reconciliation Service

The Reconciliation Service compares settled positions to current positions and generates variances.

```go
// ReconciliationService compares settled positions to current positions.
type ReconciliationService struct {
    snapshotRepo    SettlementSnapshotRepository
    measurementRepo MeasurementRepository
    valuationEngine ValuationEngine
}

// Reconcile identifies variances between settled and current positions.
// Uses batch fetching to avoid N+1 query problem on large settlement runs.
// Reconcile compares settlement snapshots against current measurements.
// Note: tenant_id is extracted from ctx per project conventions (see ADR-0016).
// All repository methods and cross-service API calls enforce tenant isolation via ctx.
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

// ReconcileProvisional generates adjustments when actuals supersede forecasts
func (s *ReconciliationService) ReconcileProvisional(
    ctx context.Context,
    snapshot *SettlementSnapshot,
    actual *Measurement,
) (*Variance, error) {
    // The snapshot preserves WHY we settled that amount
    variance := &Variance{
        SnapshotID:         snapshot.ID,
        OriginalSource:     snapshot.SourceAtSettle,    // "FORECAST_ML"
        OriginalQuality:    snapshot.QualityAtSettle,   // 50
        OriginalQuantity:   snapshot.QuantitySettled,

        ActualSource:       actual.Source,              // "ACTUAL_VALIDATED"
        ActualQuality:      actual.QualityScore,        // 90
        ActualQuantity:     actual.Quantity,

        QuantityDelta:      actual.Quantity.Sub(snapshot.QuantitySettled),
        VarianceReason:     "FORECAST_DEVIATION",       // Audit trail!
    }

    return variance, nil
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

### 5. Settlement Finality

After final settlement (e.g., M+14 for UK energy), positions are locked:

```go
func (s *SettlementService) FinalizeRun(ctx context.Context, run string, cutoff time.Time) error {
    // CockroachDB does not support TSTZRANGE; use explicit start/end column comparisons
    return s.db.Model(&Measurement{}).
        Where("settlement_run = ? AND locked_at IS NULL", run).
        Where("period_start < ? AND period_end > ?", cutoff, cutoff.AddDate(0, -14, 0)).
        Update("locked_at", time.Now()).Error
}
```

Once locked, the Delta Engine (ADR-0017) returns `ActionCreateDispute` instead of `ActionWashAndReload`.

### 6. Dispute Workflow

When new data arrives for a locked position, it's routed to the dispute workflow rather than
automatic correction. Until a full Dispute Resolution system is implemented, use this fail-safe:

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

## Service Responsibilities

| Component | Service | Notes |
|-----------|---------|-------|
| Settlement Snapshots | Financial Accounting | Owns settlement lifecycle and snapshots |
| Reconciliation | Financial Accounting | Queries own snapshots, calls Position Keeping API for current measurements |
| Settlement Finality | Financial Accounting | Triggers position locking via Position Keeping |
| Adjustment Entry | Payment Order | Financial settlement of variance |
| Dispute Records | Financial Accounting | Until dedicated Dispute Resolution service exists |

**Note on Cross-Service Queries:** Per project rules (no cross-schema queries), Financial
Accounting owns Settlement Snapshots in its own schema. Reconciliation fetches current
measurements via Position Keeping's API, not direct database joins. This maintains service
isolation at the cost of additional API calls during reconciliation.

**Cross-Tenant Reconciliation:** By design, there is no special backdoor for cross-tenant
reconciliation. When two organizations need to reconcile positions (e.g., inter-company
settlements, counterparty reconciliation), they are treated as two external systems
communicating via standard APIs. Each tenant's data remains isolated per ADR-0016; any
cross-tenant data exchange happens through explicit integration contracts, not internal
shortcuts. This ensures tenant isolation guarantees are never compromised for convenience.

## Consequences

### Positive

* **Regulatory compliance**: Settlement runs and finality are first-class concepts
* **Reconciliation capability**: Clear variance detection between settlement runs
* **Audit trail**: Snapshots preserve why decisions were made, enabling variance explanation
* **Unified Treasury & Accounting**: By treating Forecasts as authoritative for future periods and Actuals as authoritative for past periods, the ledger enables real-time **Cashflow/Inventory Forecasting** (Treasury) and **Historical Audit** (Accounting) in a single view

### Negative

* **Storage overhead**: Snapshots duplicate measurement data at settlement time
* **Cross-service latency**: Reconciliation requires API calls to Position Keeping
* **Ongoing reconciliation**: Positions are never truly "final" until settlement locked

## Implementation Notes

### Database Indexes for Settlement

```sql
-- Reconciliation queries
CREATE INDEX idx_settlement_snapshots_run
    ON settlement_snapshots(settlement_run_id);

-- Settlement finality queries (FinalizeRun)
CREATE INDEX idx_measurements_settlement_run
    ON measurements(settlement_run)
    WHERE locked_at IS NULL;
```

### Performance Considerations

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

## Event Contracts

Per ADR-0004 (Event-Driven Architecture), the following domain events are published during
settlement lifecycle operations. All events include standard envelope fields (event_id,
timestamp, tenant_id, correlation_id).

### Settlement Events

| Event | Trigger | Payload | Consumers |
|-------|---------|---------|-----------|
| `SettlementRunStarted` | Settlement batch begins | run_id, run_type, period_start, period_end | Monitoring, Audit |
| `SettlementSnapshotCreated` | Position captured for settlement | snapshot_id, run_id, measurement_id | Financial Accounting |
| `SettlementRunCompleted` | Settlement batch finishes | run_id, positions_settled, total_value | Monitoring, Payment Order |
| `MeasurementLocked` | Settlement finalized | measurement_id, settlement_run, locked_at | Financial Accounting |

### Reconciliation Events

| Event | Trigger | Payload | Consumers |
|-------|---------|---------|-----------|
| `ReconciliationStarted` | Reconciliation batch begins | run_id, comparing_run_id | Monitoring |
| `VarianceDetected` | Settled vs current differs | variance_id, snapshot_id, delta_quantity, delta_value | Payment Order, Alerting |
| `ReconciliationCompleted` | Reconciliation batch finishes | run_id, total_variances, total_value | Monitoring |

### Dispute Events

| Event | Trigger | Payload | Consumers |
|-------|---------|---------|-----------|
| `DisputeCreated` | Locked position receives new data | dispute_id, incoming_measurement_id, existing_measurement_id | Dispute Resolution, Alerting |
| `DisputeResolved` | Dispute closed | dispute_id, resolution_type, adjustment_id | Financial Accounting, Audit |

### Event Publishing Pattern

Events are published using the transactional outbox pattern to ensure exactly-once delivery:

```go
// Example: Publishing SettlementRunCompleted event
func (s *SettlementService) CompleteRun(ctx context.Context, runID uuid.UUID) error {
    return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
        // ... settlement completion logic ...

        // Write event to outbox (same transaction)
        event := OutboxEvent{
            ID:        uuid.New(),
            EventType: "SettlementRunCompleted",
            Payload: mustMarshal(SettlementRunCompletedEvent{
                RunID:            runID,
                PositionsSettled: count,
                TotalValue:       totalValue,
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
| `settlement_run_duration_seconds` | Histogram | run_type | Settlement batch processing time |
| `settlement_snapshots_created_total` | Counter | tenant_id, asset_code | Snapshots created per run |
| `reconciliation_variance_total` | Gauge | tenant_id, asset_code | Current variance amount |
| `reconciliation_duration_seconds` | Histogram | run_type | Reconciliation processing time |
| `disputes_pending_total` | Gauge | tenant_id | Disputes awaiting resolution |
| `disputes_created_total` | Counter | tenant_id, reason | Disputes created |

### Tracing

Each settlement lifecycle operation should propagate trace context:

```go
func (s *ReconciliationService) Reconcile(ctx context.Context, runID uuid.UUID) ([]Variance, error) {
    ctx, span := tracer.Start(ctx, "ReconciliationService.Reconcile",
        trace.WithAttributes(
            attribute.String("settlement_run_id", runID.String()),
        ),
    )
    defer span.End()

    // ... reconciliation logic ...
    span.SetAttributes(
        attribute.Int("variances_found", len(variances)),
        attribute.String("total_delta", totalDelta.String()),
    )
    return variances, nil
}
```

### Alerting Thresholds

| Alert | Condition | Severity | Action |
|-------|-----------|----------|--------|
| High Dispute Rate | >10 disputes/hour/tenant | Warning | Investigate data source |
| Settlement Overdue | Run not completed T+2 hours | Critical | Page on-call |
| Variance Threshold | Single period variance >$10k | Warning | Review for fraud |
| Reconciliation Failure | Run fails 3 consecutive times | Critical | Page on-call |

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

## Scope and Boundaries

### In Scope

- Settlement snapshot creation and storage
- Reconciliation between settlement runs
- Settlement finality and position locking
- Dispute creation for locked positions
- Temporal authority (forecast vs actual)
- Provisional settlement workflow

### Out of Scope (Future ADRs)

| Topic | Notes |
|-------|-------|
| **Dispute Resolution Workflow** | Full investigation, resolution, and manual adjustment flows. Target: ADR-0019 or later. |
| **Valuation Engine** | Temporal tariff lookup, attribute-based pricing. ADR-0013's `Rate` type provides foundation. |
| **Real-time Streaming** | This ADR assumes batch-oriented settlement. Streaming ingestion addressed separately. |

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

### Valuation Integration

The Valuation Engine (future ADR) must support:
- Temporal rate lookup (rate at settlement period, not current)
- Attribute-based pricing (peak/off-peak, vintage, grade)
- Multi-period aggregation with different rates per sub-period
- **Cross-asset conversion**: Direct value translation between non-monetary asset classes
  (e.g., compute hours → carbon credits, commodity A → commodity B at market rate)

### Data Retention and Archival

At scale, storage will grow rapidly. Retention strategy for settlement data:

| Data Type | Hot Storage | Warm Storage | Cold/Archive |
|-----------|-------------|--------------|--------------|
| Settlement snapshots | Until final + 1 year | 7 years | 10+ years |
| Dispute records | Until resolved + 1 year | 7 years | 10+ years |
| Reconciliation results | 2 years | 7 years | 10+ years |

Archival preserves audit trail while keeping hot path performant. Consider partitioning
by settlement run date for efficient bulk archival operations.

## Error Taxonomy

Domain-specific errors for settlement and reconciliation:

```go
var (
    // ErrOutsideBackfillWindow indicates the measurement period is older than
    // the configured backfill window for this asset.
    ErrOutsideBackfillWindow = errors.New("measurement outside backfill window")

    // ErrInvalidSettlementRun indicates the settlement run identifier is malformed
    // or not in the asset's configured schedule.
    ErrInvalidSettlementRun = errors.New("invalid settlement run identifier")

    // ErrSettlementRunNotFound indicates the requested settlement run does not exist.
    ErrSettlementRunNotFound = errors.New("settlement run not found")

    // ErrDisputeRateLimitExceeded indicates too many disputes created for this tenant.
    ErrDisputeRateLimitExceeded = errors.New("dispute rate limit exceeded")

    // ErrUnauthorized indicates the caller is not authorized for this operation.
    ErrUnauthorized = errors.New("unauthorized")
)
```

## Glossary

| Term | Definition |
|------|------------|
| **Temporal Authority** | Principle that source quality depends on time context: Forecasts are authoritative for future periods, Actuals for past |
| **Provisional Settlement** | Financial action based on non-final data (forecast/estimate), subject to reconciliation when actuals arrive |
| **Freezing** | Capturing provenance (source, quality) in a snapshot when taking provisional action, enabling variance analysis |
| **Settlement Run** | Batch process that finalizes positions for a time period (e.g., D+1, M+14) |
| **Backfill Window** | Maximum age of measurements accepted before rejection |
| **Final Settlement** | Point after which positions are locked and changes become disputes |
| **Variance** | Difference between settled quantity and current quantity for a position |
| **Dispute** | Record created when new data arrives for a locked (finalized) position |

## BIAN v14 Alignment

This ADR's concepts map to BIAN (Banking Industry Architecture Network) v14 service domains:

| Meridian Concept | BIAN Service Domain | BIAN Entity | Notes |
|-----------------|---------------------|-------------|-------|
| `SettlementSnapshot` | Trade Settlement | `TradeSettlementProcedure` (CR) | "Final movement of cash and securities" |
| `ReconciliationService` | Account Reconciliation | `AccountReconciliationProcedure` (CR) | "Handles account reconciliation tasks" |
| Settlement lifecycle | Card Financial Settlement | `CardFinancialSettlement` | "Reconciliation of settlement against cleared charges" |
| `Variance` detection | Account Reconciliation | Reconciliation operations | Control, Exchange, Execute |
| Position locking | Position Keeping | `Control` operation | "Control the processing of the log" |

**BIAN Service Domain Descriptions:**

- **Trade Settlement**: "Handles the final movement of cash and securities between depositories
  as previously confirmed in the clearing process, in order to settle a market trade"
- **Account Reconciliation**: "Handles account reconciliation tasks" with operations for
  Control, Exchange, Execute, Initiate, Notify, Request
- **Card Financial Settlement**: "Orchestrates the settlement of transactions... used by
  Issuing and Acquiring banks to perform reconciliation of settlement instructions against
  cleared charges"

**Service Responsibility Alignment:**

| Meridian Service | BIAN Service Domain | Responsibility |
|-----------------|---------------------|----------------|
| Financial Accounting | Financial Accounting | `FinancialBookingLog`, `LedgerPosting` |
| Financial Accounting | Trade Settlement | Settlement snapshots and lifecycle |
| Financial Accounting | Account Reconciliation | Variance detection and reconciliation |
| Position Keeping | Position Keeping | `FinancialPositionLog`, position locking |
| Payment Order | (various) | Financial settlement of variances |

**Extensions Beyond BIAN Standard:**

The following Meridian concepts extend or have no direct BIAN equivalent:

- **Temporal Authority**: BIAN doesn't model time-relative source authority (Forecasts for
  future, Actuals for past)
- **Provisional Settlement**: BIAN settlement is typically final; Meridian supports provisional
  settlement with later reconciliation
- **Settlement Run scheduling** (D+1, M+14): Industry-specific patterns not in BIAN standard
- **Dispute workflow for locked positions**: BIAN has case management but not the specific
  pattern of archiving incoming data and creating disputes for finalized positions

## Links

### Internal ADRs

* [ADR-0004: Event-Driven Architecture](0004-event-schema-evolution.md) - Event contracts and publishing patterns
* [ADR-0013: Universal Quantity Type System](0013-generic-asset-quantity-types.md) - Quantity and rate types
* [ADR-0014: Financial Instrument Reference Data](0014-financial-instrument-reference-data.md) - Asset definitions and attributes
* [ADR-0016: Tenant Isolation](0016-tenant-id-naming-strategy.md) - Multi-tenancy and schema separation
* [ADR-0017: Temporal Quality Ledger](0017-temporal-quality-ladder.md) - Measurement log, quality ladder, supersession, corrections

### External References

* [UK Balancing and Settlement Code (BSC)](https://www.elexon.co.uk/bsc-and-codes/)
* [ELEXON Settlement Timetable](https://www.elexon.co.uk/operations-settlement/settlement-timetable/)
