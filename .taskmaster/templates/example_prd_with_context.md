# PRD Template - Worked Example

This file demonstrates the `# Codebase Context` section of
`.taskmaster/templates/example_prd.txt` filled in for a realistic Meridian feature.

The example feature is **Dunning Escalation Trigger** - a scheduler-driven workflow
that automatically escalates overdue payment orders through a configurable sequence of
notifications and account actions.

---

<context>

# Overview

Add a dunning escalation trigger to the payment-order service. When a payment order
remains unpaid past configurable thresholds, the platform should automatically escalate
through a predefined sequence: initial overdue notice, suspension warning, and account
suspension. Tenants configure escalation schedules per account type via the manifest.

Target users: platform operators managing collections workflows and tenants running
consumer-facing billing.

# Core Features

**Threshold-based escalation stages**
- Configurable day offsets per stage (e.g. day 1, day 7, day 14)
- Each stage executes an action: send notice, send warning, suspend account
- Tenant configures stages in the manifest; platform runs them on schedule

**Per-order idempotent execution**
- Each stage fires at most once per order per threshold crossing
- Re-runs after service restarts do not re-trigger completed stages

**Audit trail**
- Every escalation action records a structured audit event attributed to
  `system:scheduler:payment-order`

# User Experience

Tenant operators configure escalation in the manifest:

```yaml
dunning:
  stages:
    - days_overdue: 1
      action: send_notice
    - days_overdue: 7
      action: send_warning
    - days_overdue: 14
      action: suspend_account
```

The platform evaluates open orders nightly and advances any order that has crossed
a threshold since the last evaluation.

</context>
<PRD>

# Technical Architecture

The dunning trigger runs as a scheduled job inside the payment-order service, using the
existing `shared/platform/scheduler` infrastructure. Each evaluation cycle:

1. Queries all open orders where `due_date + stage.days_overdue <= now` and the stage
   has not yet been executed for that order.
2. For each qualifying (order, stage) pair, executes the configured action via an
   internal saga step.
3. Records execution in a `dunning_executions` table to enforce idempotency.
4. Publishes a `dunning.escalated` event via the outbox for downstream consumers
   (correspondence-service, audit-worker).

Data model additions:
- `dunning_executions(id, order_id, stage_index, executed_at, action, tenant_id)`
- Index on `(order_id, stage_index)` for idempotency checks

# Codebase Context

## Affected Layers

- **Layer 5 - Lifecycle Orchestration**: `payment-order` is the primary service.
  The scheduler and escalation saga both live here.
- **Layer 4 - Core Ledger**: `current-account` receives the `SuspendAccount` RPC
  when the `suspend_account` action fires.
- **Layer 8 - Observability and Routing**: `audit-worker` consumes `dunning.escalated`
  events to produce tamper-evident audit records.
- **Layer 6 - Reference and Registry**: `reference-data` supplies the tenant manifest
  dunning configuration at startup.

See `docs/architecture-layers.md` for the full layer map.

## Primary Files

| File | Change Type | Purpose |
|------|-------------|---------|
| `services/payment-order/internal/dunning/scheduler.go` | Create | Scheduled job that evaluates open orders against dunning stages |
| `services/payment-order/internal/dunning/executor.go` | Create | Executes a single (order, stage) action and records completion |
| `services/payment-order/internal/dunning/repository.go` | Create | `dunning_executions` table queries |
| `services/payment-order/migrations/20260601000001_dunning_executions.sql` | Create | Atlas migration adding `dunning_executions` table |
| `services/payment-order/atlas/atlas.hcl` | Modify | Register new migration directory entry |
| `services/payment-order/internal/app/wire.go` | Modify | Wire dunning scheduler into application startup |
| `api/proto/meridian/payment_order/v1/dunning.proto` | Create | `DunningStage`, `DunningConfig`, `DunningEscalatedEvent` proto definitions |
| `shared/platform/scheduler/cron.go` | Modify | Expose `SchedulerName` on `Schedule` struct for actor attribution |

## Related Patterns

- **Outbox Pattern** (`shared/platform/events/`) - `dunning.escalated` events must be
  published transactionally with the `dunning_executions` insert to prevent dual-write
  loss. See `docs/patterns.md` section 1.
- **Saga Handler - Starlark** (`shared/pkg/saga/`) - if tenant overrides are needed,
  the `suspend_account` action can be expressed as a Starlark saga step. For the MVP,
  a direct Go call to current-account suffices; saga wrapping is the extension path.
  See `docs/patterns.md` section 3.
- **Tenant Scoping** (`shared/platform/tenant/`) - all queries and writes must be
  scoped to the active tenant schema. See `docs/patterns.md` section 4.
- **Idempotency** (`shared/pkg/idempotency/`) - the `(order_id, stage_index)` unique
  constraint on `dunning_executions` is the primary guard; the idempotency package
  provides the retry-safe wrapper for the outer RPC boundary. See `docs/patterns.md`
  section 5.

## Dependencies

- **`correspondence-service`** (existing, new consumer): subscribes to
  `dunning.escalated` to deliver outbound notices. No new gRPC calls from
  payment-order; the service is driven entirely by the Kafka event.
- **`current-account`** (existing, new RPC): `SuspendAccount` RPC called directly
  when `action = suspend_account`. Requires the payment-order service account to hold
  the `account:suspend` RBAC permission (grant via manifest).
- **`reference-data`** (existing): dunning stage configuration is read from the tenant
  manifest at scheduler startup via the existing `ManifestProvider`.

# Development Roadmap

**Phase 1 - Data foundation**
- Migration: `dunning_executions` table with idempotency index
- Repository: `FindPendingEscalations(ctx, now)` and `MarkExecuted(ctx, id)`

**Phase 2 - Scheduler and executor**
- `DunningScheduler` wired into `shared/platform/scheduler` with actor attribution
- `DunningExecutor` implementing `send_notice` (outbox event) and `suspend_account`
  (current-account RPC) actions
- Unit tests with table-driven cases for threshold boundary conditions

**Phase 3 - Manifest integration**
- Read dunning stage config from manifest via `reference-data`
- Default config (3-stage) applied when tenant manifest omits `dunning` block
- Integration test: end-to-end escalation from overdue order to audit record

# Logical Dependency Chain

1. Proto definitions and migration must land before any Go code references them
2. Repository layer before scheduler (scheduler depends on repository)
3. Executor before scheduler (scheduler calls executor)
4. Scheduler wiring before integration tests
5. Manifest integration last (can use hardcoded default config for phases 1-3)

# Risks and Mitigations

**Risk**: Scheduler fires while a large backlog of overdue orders exists, causing a
thundering herd of `SuspendAccount` RPCs to current-account.
**Mitigation**: Executor processes at most N orders per cycle (configurable, default
100) with a short sleep between RPC calls.

**Risk**: `suspend_account` RPC fails mid-batch; partially applied dunning stage leaves
some accounts suspended, others not.
**Mitigation**: Each (order, stage) pair is committed independently. A failed pair
remains `pending` and retries on the next scheduler cycle. The action is idempotent
on current-account (suspending an already-suspended account is a no-op).

**Risk**: Atlas migration adds `dunning_executions` and an index in the same file,
violating CockroachDB's constraint that partial indexes cannot reference a new column
in the same transaction.
**Mitigation**: The index is a plain unique index on existing-type columns, not a
partial index. If a partial index is needed later, it goes in a separate migration file
per `docs/development/migrations.md`.

# Appendix

- `docs/architecture-layers.md` - canonical layer definitions
- `docs/patterns.md` - Outbox, Saga Handler, Tenant Scoping, Idempotency patterns
- `shared/platform/scheduler/` - existing scheduler infrastructure
- PRD-060 (`docs/prd/060-per-tenant-scheduled-execution.md`) - actor attribution
  design that this PRD builds on

</PRD>
