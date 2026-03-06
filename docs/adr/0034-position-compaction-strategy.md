---
name: adr-0034-position-compaction-strategy
description: Background compaction strategy for the append-only position repository with deferral criteria
triggers:
  - Evaluating position table growth and query performance
  - Considering when to enable the compaction worker in production
  - Reviewing append-only position write patterns and read aggregation
  - Debugging position bucket fragmentation or slow aggregation queries
instructions: |
  The position table uses append-only writes for O(1) inserts without locks. Over time, this
  creates multiple rows per (account_id, instrument_code, bucket_key) bucket. Read-time
  aggregation via SQL GROUP BY handles this transparently. A CompactionWorker exists but is
  disabled by default. Enable it when fragmented buckets degrade query latency beyond 50ms
  p99, or when per-bucket row counts routinely exceed 1,000 rows. Monitor via
  meridian_position_keeping_fragmented_buckets and meridian_position_keeping_compaction_* metrics.
---

# 34. Position Compaction Strategy

Date: 2026-03-06

## Status

Accepted

## Context

The position-keeping service uses an **append-only write pattern** for the `position` table.
Each measurement creates a new row rather than updating an existing one, achieving O(1)
constant-time inserts without row-level locks. This is critical for high-throughput scenarios
where per-bucket locking would create bottlenecks on hot accounts.

Over time, append-only writes create fragmentation: multiple rows accumulate for the same
`(account_id, instrument_code, bucket_key)` combination. Read operations aggregate these
rows using SQL `GROUP BY` or in-memory summation. The question is whether and when to
consolidate (compact) fragmented buckets into single rows.

A `CompactionWorker` already exists in `services/position-keeping/worker/compaction_worker.go`
with full implementation including:

- Configurable run interval, fragment threshold, and batch size
- Row-level locking with `REPEATABLE READ` isolation for correctness
- Soft-deletion of original rows (preserving audit trail via `deleted_at`)
- Compaction metadata in attributes (`_compacted_from_count`, `_compaction_ref`)
- Optional audit table (`position_compaction_audit`) for traceability
- Prometheus metrics for runs, errors, duration, and fragmented bucket counts
- Graceful shutdown with in-flight operation draining

The worker is wired into the service main and **disabled by default** via
`config.Compaction.Enabled`.

## Decision Drivers

* **Write throughput**: Append-only inserts must remain O(1) with zero contention
* **Read latency**: SQL `GROUP BY` aggregation must stay under 50ms p99
* **Audit trail**: Original position records must be recoverable for regulatory queries
* **Operational simplicity**: Avoid premature optimization that adds operational burden
* **CockroachDB compatibility**: Compaction must work within CockroachDB's transaction semantics

## Considered Options

1. **Enable compaction now** with conservative thresholds
2. **Defer compaction** with explicit trigger criteria and monitoring
3. **Replace append-only with upsert** (merge-on-write)

## Decision Outcome

Chosen option: **"Defer compaction with explicit trigger criteria and monitoring"**, because
the current read-time aggregation pattern performs well for expected workloads, the
CompactionWorker implementation is ready when needed, and enabling it prematurely adds
operational complexity (monitoring compaction errors, tuning thresholds, managing the
audit table migration) without measurable benefit.

### Positive Consequences

* Zero operational overhead from compaction in early deployment stages
* Read-time aggregation is simpler to reason about and debug
* No risk of compaction bugs affecting production position data
* CompactionWorker is pre-built and tested, ready for immediate activation

### Negative Consequences

* Position table grows linearly with transaction volume (no consolidation)
* Read queries scan more rows per bucket over time (mitigated by SQL `GROUP BY` pushdown)
* Must actively monitor to detect when compaction becomes necessary

## Pros and Cons of the Options

### Option 1: Enable Compaction Now

Enable the `CompactionWorker` with conservative defaults (e.g., 100-row threshold,
5-minute interval, 50-bucket batch size).

* Good, because it proactively prevents unbounded row growth
* Good, because the implementation already exists and is tested
* Bad, because it requires the `position_compaction_audit` migration (not yet created)
* Bad, because compaction errors require monitoring and alerting infrastructure
* Bad, because there is no evidence that fragmentation is causing issues yet

### Option 2: Defer Compaction with Trigger Criteria

Keep compaction disabled. Define explicit criteria for when to enable it.
Monitor position growth via existing Prometheus metrics.

* Good, because it avoids premature optimization
* Good, because it keeps the operational surface area minimal
* Good, because trigger criteria make the decision repeatable and evidence-based
* Bad, because a sudden traffic spike could degrade reads before compaction is enabled

### Option 3: Replace Append-Only with Upsert

Switch from append-only inserts to upsert (merge-on-write) using
`INSERT ... ON CONFLICT DO UPDATE SET amount = amount + excluded.amount`.

* Good, because it eliminates fragmentation entirely
* Bad, because it introduces per-bucket row locks on the write path
* Bad, because it fundamentally changes the write performance model (O(1) to contended)
* Bad, because it loses individual position records (no audit trail of individual movements)
* Bad, because it breaks the append-only contract documented in PRD-001

## Trigger Criteria for Enabling Compaction

Compaction should be enabled when **any** of the following conditions are observed:

| Trigger | Metric / Query | Threshold |
|---------|---------------|-----------|
| Per-bucket row count | `SELECT account_id, instrument_code, bucket_key, COUNT(*) FROM position WHERE deleted_at IS NULL GROUP BY 1,2,3 ORDER BY 4 DESC LIMIT 10` | > 1,000 rows per bucket |
| Aggregation query latency | `meridian_position_keeping_get_aggregated_position_duration_seconds` p99 | > 50ms |
| Total position row count | `SELECT COUNT(*) FROM position WHERE deleted_at IS NULL` | > 10M rows per tenant |
| Fragmented bucket count | `meridian_position_keeping_fragmented_buckets` gauge (when worker runs in dry-run) | > 500 buckets above threshold |

### Recommended Activation Parameters

When trigger criteria are met, enable compaction with these initial values:

```yaml
compaction:
  enabled: true
  run_interval: 5m
  fragment_threshold: 100
  batch_size: 50
```

### Pre-Activation Checklist

Before enabling compaction in production:

1. Create the `position_compaction_audit` table migration
2. Verify compaction worker behavior in staging with production-like data
3. Set up alerting on `meridian_position_keeping_compaction_errors_total`
4. Confirm `REPEATABLE READ` isolation works correctly on CockroachDB for the locking pattern
5. Test soft-delete + consolidated insert atomicity under concurrent writes

## Links

* [PRD-001: Universal Asset System - Background Compaction Worker](../prd/001-universal-asset-system.md) (Section 6, Stream I.1)
* [CompactionWorker implementation](../../services/position-keeping/worker/compaction_worker.go)
* [Compaction metrics](../../services/position-keeping/worker/metrics.go)
* [CompactionWorker tests](../../services/position-keeping/worker/compaction_worker_test.go)

## Notes

* The `position_compaction_audit` table does not yet exist in migrations. The worker handles
  this gracefully (logs a warning, continues without audit). The migration should be created
  as part of the pre-activation checklist.
* The PRD mentions a threshold of 100 rows for read-side coalescing. The CompactionWorker's
  `FragmentThreshold` config maps to this value but is tunable per deployment.
* Soft-deleted rows (`deleted_at IS NOT NULL`) are excluded from all read queries and
  aggregation. A separate retention policy for purging soft-deleted rows is out of scope
  for this ADR but should be considered when compaction is enabled.
* If CockroachDB's `REPEATABLE READ` behavior differs from PostgreSQL's for `SELECT ... FOR UPDATE`
  patterns, the locking strategy in `compactBucket` may need adjustment. This should be validated
  during pre-activation testing.
