---
name: adr-029-settlement-scheduler-architecture
description: Retain in-process cron scheduling with leader election, augmented by database-backed audit trail
triggers:
  - Evaluating scheduler resilience for production settlement workloads
  - Choosing between in-process cron, CronJob, or database-polling patterns
  - Designing catch-up mechanisms for missed scheduled windows

instructions: |
  Use robfig/cron with Redis-based leader election for settlement scheduling.
  Persist execution records to the scheduler_execution table for audit and catch-up.
  On startup, compare last execution timestamps against expected cron fire times to
  detect and trigger missed windows within the configured lookback period.
---

# 29. Settlement Scheduler Architecture

Date: 2026-02-09

## Status

Accepted

## Context

The reconciliation service triggers settlement runs on cron schedules defined in
Reference Data. The initial implementation uses `robfig/cron` (in-process) with
Redis-based leader election. During PR review, questions were raised about whether
this is production-resilient or whether a Kubernetes CronJob or database-polling
approach would be more appropriate.

Key concerns:
- Missed settlement windows during pod restarts or deployments.
- No audit trail of which windows were triggered, skipped, or missed.
- No defence-in-depth against duplicate settlement runs at the database level.

## Decision Drivers

* Settlement windows must not be silently missed during deployments or outages.
* Operators need visibility into scheduler health (which runs triggered, which missed).
* The solution must work with CockroachDB (no LISTEN/NOTIFY).
* Minimal operational complexity -- avoid introducing new infrastructure (e.g., workflow engines).
* Leader election prevents duplicate execution across replicas.

## Considered Options

1. **In-process cron + leader election + DB audit trail** (current approach, augmented)
2. **Kubernetes CronJob per schedule**
3. **Pure database-polling scheduler**

## Decision Outcome

Chosen option: **Option 1 -- In-process cron with leader election, augmented with
database-backed audit trail and catch-up mechanism**, because it provides the required
resilience without introducing new infrastructure or operational complexity.

### Positive Consequences

* Scheduler state survives pod restarts via the `scheduler_execution` table.
* Catch-up mechanism detects and triggers missed windows on startup.
* Audit trail enables operators to query scheduler health.
* Unique constraint on `settlement_run(account_id, period_start, period_end)` provides
  defence-in-depth against duplicate runs.
* No new infrastructure required beyond what already exists (Redis, CockroachDB).
* Cron expressions are evaluated in-process with sub-second precision.

### Negative Consequences

* Catch-up on startup adds brief latency to pod readiness.
* The leader election mechanism depends on Redis availability.

## Pros and Cons of the Options

### Option 1: In-process cron + leader election + DB audit trail

Retain `robfig/cron` for schedule evaluation. Redis leader election ensures single
execution across replicas. A `scheduler_execution` table records each trigger event.
On startup, the scheduler compares current time against the last recorded execution
per schedule and triggers any missed windows within a configurable lookback period.

* Good, because it reuses existing infrastructure (Redis, CockroachDB).
* Good, because cron expressions are evaluated with sub-second precision.
* Good, because leader election is already proven in the codebase.
* Good, because catch-up is deterministic -- it walks forward from last execution
  using the cron parser to compute expected fire times.
* Bad, because the scheduler runs as part of the application process (not externally managed).

### Option 2: Kubernetes CronJob per schedule

Create Kubernetes CronJob resources dynamically from Reference Data schedules.
Each CronJob invokes a gRPC call to initiate the settlement run.

* Good, because Kubernetes handles scheduling, retries, and concurrency policies.
* Good, because decoupled from application lifecycle.
* Bad, because schedules are dynamic (driven by Reference Data), requiring runtime
  CronJob creation/deletion via Kubernetes API -- adds operational complexity.
* Bad, because CronJob has minute-level granularity with no sub-minute precision.
* Bad, because missed job handling (`startingDeadlineSeconds`) is coarse-grained.
* Bad, because requires RBAC for the service to manage CronJob resources.

### Option 3: Pure database-polling scheduler

Replace cron entirely with a polling loop that queries a `scheduled_window` table.
Each row represents a future window to trigger. The worker polls for due windows
and processes them.

* Good, because state is fully in the database -- inherently persistent.
* Good, because no dependency on Redis for leader election (use SELECT FOR UPDATE SKIP LOCKED).
* Bad, because requires pre-computing and inserting future windows into the table.
* Bad, because polling frequency creates a trade-off between latency and DB load.
* Bad, because cron expression parsing must be reimplemented to generate future windows.
* Bad, because significantly more complex than the in-process approach.

## Links

* [ADR-0018: Settlement Reconciliation](0018-settlement-reconciliation.md)
* [robfig/cron documentation](https://pkg.go.dev/github.com/robfig/cron/v3)

## Notes

If the scheduler evolves to require sub-second precision or complex DAG-style
dependencies between settlement windows, reconsider migrating to a lightweight
workflow engine (e.g., Temporal). The current approach is appropriate for
cron-driven periodic settlement at daily/weekly/monthly granularity.

The database-polling approach (Option 3) remains viable as a future evolution if
Redis availability becomes a concern. The `scheduler_execution` table schema is
compatible with either approach.
