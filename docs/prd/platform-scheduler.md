# Platform Scheduler PRD

**Status:** Draft
**Owner:** Platform Team
**Last Updated:** 2026-02-11

## Executive Summary

Meridian has three independent cron scheduler implementations across payment-order
(BillingScheduler), reconciliation (SettlementScheduler), and forecasting
(CronScheduler). Each re-implements the same lifecycle machinery: cron engine
setup, graceful shutdown with in-flight work tracking, mutex-guarded state, and
Redis-based distributed locking. Only the reconciliation scheduler has
DB-backed execution tracking with missed-window catch-up - a capability that
every scheduled workload in a multi-tenant system requires.

This PRD defines a shared `platform/scheduler` package that extracts the
common patterns into a single, well-tested abstraction, and establishes
DB-backed execution awareness as the baseline for all scheduled work.

## Problem Statement

### Duplication

Three services independently implement the same scheduler skeleton:

| Component | payment-order | reconciliation | forecasting |
|-----------|:---:|:---:|:---:|
| `robfig/cron/v3` setup | Yes | Yes | Yes |
| `running`/`stopped`/`mu`/`wg` state | Yes | Yes | Yes |
| `Start(ctx)` blocking loop | Yes | Yes | Yes |
| Graceful shutdown with timeout | Yes | Yes | Yes |
| Execute guard (stopped check + WaitGroup) | Yes | Yes | Yes |
| Dynamic schedule reload via polling | No | Yes | Yes |
| Redis distributed locking | Idempotency keys | `RedisLeaderElector` | `LeaseManager` |
| DB execution audit trail | No | Yes | No |
| Missed-window catch-up on startup | No | Yes | No |

This is approximately 1,200 lines of duplicated lifecycle code across the
three services, with subtle divergences in shutdown ordering and error handling
that create maintenance risk.

### Missing Resilience in Forecasting and Billing

The forecasting scheduler (PR #888) and billing scheduler have no execution
persistence. If pods restart, scale to zero overnight, or experience
transient failures:

- **Forecasting**: Silently drops all missed forecast executions. Tenant A's
  `@every 15m` strategy loses 2 executions during a 30-minute deployment.
  No record, no recovery, no operator visibility.
- **Billing**: Uses Redis idempotency keys (48h TTL) for duplicate prevention
  but has no awareness of missed billing cycles beyond Redis TTL expiry.

In a multi-tenant system where different tenants have different schedules, any
gap in scheduler uptime creates invisible data loss with no mechanism for
detection or remediation.

### Inconsistent Distributed Locking

Two independent `bsm/redislock` wrappers exist:

- `services/reconciliation/worker/leader.go` - Single-lock leader election
  (148 lines)
- `services/forecasting/scheduler/lease_manager.go` - Multi-lock per-resource
  leasing (176 lines)

Both implement the same core: `redislock.New()`, `client.Obtain()`,
`lock.Refresh()` in a background ticker, `lock.Release()`, and
context-based cancellation for renewal goroutines. The reconciliation
implementation holds a single lock (leader election pattern), the forecasting
implementation holds multiple (per-strategy lease pattern), but the underlying
mechanics are identical.

## Goals

| Goal | Success Metric |
|------|---------------|
| Single scheduler lifecycle implementation | All three services use `shared/platform/scheduler` |
| DB-backed execution tracking for all schedulers | Every scheduled execution recorded with status lifecycle |
| Missed-window catch-up for all schedulers | On startup, detect and optionally re-execute missed windows |
| Unified distributed locking | Single Redis lock abstraction replaces both `leader.go` and `lease_manager.go` |
| Multi-tenant schedule isolation | Per-tenant execution tracking, per-tenant catch-up |
| Zero behaviour change for existing services | Migration is a pure refactor - no observable change in scheduling semantics |

## Non-Goals

- Replacing `robfig/cron/v3` with a different scheduling library
- Adding a scheduler UI or API (schedules are defined in service code or config)
- Event-driven scheduling via Kafka (CockroachDB has no LISTEN/NOTIFY; polling remains the pattern)
- Central scheduler service (each service owns its scheduling; the shared package is a library)

## Architecture

### Package Structure

```text
shared/platform/
├── scheduler/
│   ├── scheduler.go          # Core lifecycle: Start/Stop, poll loop, job tracking
│   ├── scheduler_test.go
│   ├── execution_store.go    # DB-backed execution audit trail (interface + CockroachDB impl)
│   ├── execution_store_test.go
│   ├── catchup.go            # Missed-window detection and re-execution
│   ├── catchup_test.go
│   ├── config.go             # Shared configuration types
│   └── metrics.go            # Common Prometheus metrics skeleton
└── redislock/
    ├── lock.go               # Unified distributed lock: single-lock + multi-lock modes
    ├── lock_test.go
    └── config.go             # TTL, renewal, key prefix configuration
```

### Core Abstraction

The scheduler package defines the lifecycle; services provide the domain logic
via interfaces:

```go
// ScheduleProvider loads the current set of schedules from the domain's
// source of truth (database, reference data service, config, etc.).
type ScheduleProvider interface {
    ListSchedules(ctx context.Context) ([]Schedule, error)
}

// Schedule represents a single cron-scheduled job.
type Schedule struct {
    // ID uniquely identifies this schedule for tracking and deduplication.
    ID string
    // CronExpr is the cron expression (5-field standard format).
    CronExpr string
    // TenantID scopes execution tracking and locking. Empty for global schedules.
    TenantID string
    // Metadata carries domain-specific data passed to the executor.
    Metadata any
}

// Executor performs the domain-specific work when a schedule fires.
type Executor interface {
    Execute(ctx context.Context, schedule Schedule) error
}
```

### Scheduler Lifecycle

```text
┌─────────┐    Start(ctx)     ┌──────────┐
│ Created ├──────────────────>│ Running  │
└─────────┘                   └────┬─────┘
                                   │
                        ┌──────────┼──────────┐
                        │          │          │
                   Poll loop   Cron fires  Catch-up
                   (reload     (execute     (startup
                   schedules)   with lock)   only)
                        │          │          │
                        └──────────┼──────────┘
                                   │
                            ctx.Done()
                                   │
                              ┌────▼─────┐
                              │ Stopping │
                              └────┬─────┘
                                   │
                        ┌──────────┼──────────┐
                        │          │          │
                   Stop cron   Wait for    Release
                   runner      in-flight   all locks
                               (timeout)
                        │          │          │
                        └──────────┼──────────┘
                                   │
                              ┌────▼─────┐
                              │ Stopped  │
                              └──────────┘
```

### Execution Audit Trail

Every cron fire is recorded in the database with a status lifecycle:

```text
TRIGGERED ──> COMPLETED   (happy path)
          ──> FAILED      (executor returned error)
          ──> SKIPPED     (duplicate detected - idempotency)

MISSED                    (window detected during catch-up but beyond MaxCatchUpAge)
```

**Schema** (per-tenant, in tenant schema):

```sql
CREATE TABLE scheduler_execution (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    scheduler_name  VARCHAR(100) NOT NULL,  -- e.g. 'forecasting', 'billing', 'settlement'
    schedule_id     VARCHAR(200) NOT NULL,  -- domain-specific schedule identifier
    tenant_id       VARCHAR(100) NOT NULL,
    scheduled_at    TIMESTAMPTZ NOT NULL,   -- expected cron fire time
    executed_at     TIMESTAMPTZ,            -- actual execution time (NULL for MISSED)
    completed_at    TIMESTAMPTZ,            -- when execution finished
    status          VARCHAR(20) NOT NULL DEFAULT 'TRIGGERED',
    result_ref      VARCHAR(200),           -- domain-specific reference (run ID, etc.)
    error_message   TEXT,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),

    CONSTRAINT chk_scheduler_execution_status
        CHECK (status IN ('TRIGGERED', 'COMPLETED', 'FAILED', 'MISSED', 'SKIPPED'))
);

CREATE INDEX idx_sched_exec_lookup
    ON scheduler_execution (scheduler_name, schedule_id, tenant_id, scheduled_at DESC);
```

This generalises the reconciliation service's existing `scheduler_execution`
table, adding `scheduler_name` and `tenant_id` columns so multiple schedulers
can share the same table within a tenant schema.

### Catch-Up on Startup

When a scheduler starts, it:

1. Queries `ListSchedules()` to get all active schedules
2. For each schedule, queries `LastExecution()` from the DB
3. Walks the cron expression forward from `last_execution.scheduled_at` to `now`
4. For each missed window:
   - If within `MaxCatchUpAge`: execute via `Executor` (records as TRIGGERED -> COMPLETED/FAILED)
   - If beyond `MaxCatchUpAge`: record as MISSED (audit trail only, no execution)

This is the exact pattern already proven in the reconciliation scheduler,
generalised for any domain.

### Distributed Locking

The `shared/platform/redislock` package provides two usage patterns from a
single implementation:

**Per-Execution Locking** (forecasting, billing):

```go
lock := redislock.New(redisClient, redislock.Config{
    KeyPrefix: "meridian:forecasting:strategy",
    LockTTL:   5 * time.Minute,
    RenewEvery: 30 * time.Second,
})

acquired, release, err := lock.Acquire(ctx, tenantID, strategyID)
if acquired {
    defer release()
    // execute
}
```

**Leader Election** (reconciliation):

```go
leader := redislock.NewLeader(redisClient, redislock.Config{
    KeyPrefix: "meridian:reconciliation:scheduler",
    LockTTL:   30 * time.Second,
    RenewEvery: 10 * time.Second,
})

isLeader, err := leader.TryAcquire(ctx)
```

Both patterns share the same core: `bsm/redislock` obtain/refresh/release
with background renewal goroutines.

## Migration Strategy

### Phase 1: Extract Shared Packages (No Behaviour Change)

1. Create `shared/platform/redislock/` from the union of `reconciliation/worker/leader.go`
   and `forecasting/scheduler/lease_manager.go`
2. Create `shared/platform/scheduler/` with the lifecycle extracted from all three schedulers
3. Create `shared/platform/scheduler/execution_store.go` generalised from
   `reconciliation/worker/execution_store.go`

### Phase 2: Migrate Forecasting Scheduler

1. Replace `forecasting/scheduler/cron_scheduler.go` with the shared scheduler
2. Add execution tracking (new Flyway migration for `scheduler_execution` in forecasting schema)
3. Add catch-up on startup
4. Remove `forecasting/scheduler/lease_manager.go` (use `shared/platform/redislock`)

### Phase 3: Migrate Reconciliation Scheduler

1. Replace `reconciliation/worker/scheduler.go` with the shared scheduler
2. Migrate existing `scheduler_execution` table to shared schema (or keep as-is with adapter)
3. Remove `reconciliation/worker/leader.go` (use `shared/platform/redislock`)
4. Remove `reconciliation/worker/execution_store.go` (use shared implementation)

### Phase 4: Migrate Billing Scheduler

1. Replace `payment-order/worker/billing_scheduler.go` with the shared scheduler
2. Add execution tracking (new migration)
3. Add catch-up for missed billing cycles
4. Redis idempotency keys become defence-in-depth alongside DB tracking

## Complexity Estimate

| Phase | Description | Points | Parallelisable |
|-------|-------------|--------|:-:|
| 1a | Extract `shared/platform/redislock` | 3 | Yes |
| 1b | Extract `shared/platform/scheduler` (lifecycle + config + metrics) | 5 | Yes |
| 1c | Extract `shared/platform/scheduler` (execution store + catch-up) | 5 | After 1b |
| 2 | Migrate forecasting scheduler | 3 | After 1a, 1b, 1c |
| 3 | Migrate reconciliation scheduler | 3 | After 1a, 1b, 1c |
| 4 | Migrate billing scheduler | 3 | After 1a, 1b, 1c |
| **Total** | | **22** | Critical path: 13 |

Phases 2, 3, 4 can run in parallel once Phase 1 is complete.

## Testing Strategy

- **Unit tests**: Mock `ScheduleProvider`, `Executor`, `ExecutionStore` to test
  lifecycle, catch-up logic, and locking behaviour in isolation
- **Integration tests**: CockroachDB testcontainers for execution store,
  miniredis for distributed locking
- **Migration tests**: Each service migration must pass existing test suites
  unchanged (zero behaviour change guarantee)
- **Multi-pod simulation**: Start 2 scheduler instances with shared Redis,
  verify only one executes per schedule (same pattern as existing forecasting tests)

## Risks and Mitigations

| Risk | Impact | Mitigation |
|------|--------|------------|
| Shared package becomes a coupling point | Changes to scheduler affect all services | Stable interface contract; services only implement `ScheduleProvider` + `Executor` |
| Catch-up storm on startup | Many missed windows trigger simultaneous executions | Rate-limit catch-up executions; `MaxCatchUpAge` bounds the window |
| Migration breaks existing behaviour | Scheduling regressions in production | Each phase is a separate PR with full test suite pass; feature-flagged rollout |
| Execution table growth | Unbounded audit trail | Add retention policy (configurable TTL, default 90 days) with periodic cleanup |

## Success Criteria

1. All three schedulers use `shared/platform/scheduler` - no service-local scheduler lifecycle code
2. All scheduled workloads have DB-backed execution tracking with operator-visible audit trail
3. All schedulers detect and handle missed windows on startup
4. Single `shared/platform/redislock` package replaces both leader election and per-resource locking implementations
5. Existing test suites pass without modification after migration
6. Net reduction of at least 500 lines of duplicated code
