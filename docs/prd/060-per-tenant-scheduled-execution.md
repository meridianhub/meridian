# PRD-060: Per-Tenant Scheduled Execution Architecture

## Status: Draft

## Problem Statement

Meridian's scheduler infrastructure has three gaps that compound as the platform scales:

1. **No execution identity.** All scheduled work (billing,
   forecasting, reconciliation) runs as bare `context.Background()`
   with implicit god-mode database access. Every entity mutation
   records `changed_by = "system"` - indistinguishable across billing
   runs, catch-up replays, background workers, migrations, and any
   unauthenticated code path. This fails SOC 2 CC6.1 (logical access
   controls) and ISO 27001 A.5.16 (identity management).

2. **Manifest contract is broken.** The manifest proto declares
   `scheduled:` triggers (`manifest.proto:289-298`) but has no cron
   expression field and no bridge to the `CronScheduler`
   infrastructure. A tenant writes
   `trigger: "scheduled:monthly_billing"`, the manifest validates,
   and nothing happens. The MCP server documentation
   (`reference.go:212`) contradicts the manifest examples -
   documenting `scheduled:<cron-expression>` while manifests use
   `scheduled:<name>`.

3. **Missing scaling guardrails.** `executeJob()` spawns unbounded
   goroutines via `lifecycle.ExecuteGuarded()` with no concurrency
   semaphore. Tenant suspension status is never checked before
   execution. No minimum cron interval is enforced. At the current
   scale (~3 schedules), these are invisible. At N tenants x M
   schedule types, they become resource exhaustion and data integrity
   risks.

## Background

### Current Architecture

The `shared/platform/scheduler` package provides a `CronScheduler` with:

- `ScheduleProvider` interface returning all schedules across all tenants
- `Schedule` struct carrying `TenantID` for per-tenant schema routing
- Redis-based distributed locking (`shared/platform/redislock`) preventing duplicate execution across replicas
- `ExecutionStore` for audit trail persistence
- Catch-up logic for missed windows on startup

Three services consume it:

| Service | Provider | Schedule Source | Multi-tenant? |
|---------|----------|----------------|---------------|
| payment-order | `BillingScheduleProvider` | Static env var | Single tenant per config |
| forecasting | `ForecastScheduleProvider` | `forecasting_strategy` DB table | Yes - per-tenant, per-strategy |
| reconciliation | `SettlementScheduleProvider` | Reference Data gRPC (stub) | Yes (planned) |

The forecasting service is the **existence proof** - it already does
dynamic per-tenant scheduling from a database table with per-tenant
`tenant_id` and per-strategy cron expressions.

### Identity Gap

The scheduler creates `context.Background()` at `cron.go:296` and
injects only tenant context for schema routing. No `UserIDContextKey`
is set. The audit system (`shared/platform/audit`) falls back to
`DefaultAuditUser = "system"` (`audit/context.go:11-13`).

The identity service uses `auth.GetUserIDFromContext(ctx)` as an
authentication gate in 5+ endpoints
(`grpc_identity_endpoints.go:129`, `grpc_role_endpoints.go:147`,
etc.). Any value in `UserIDContextKey` - including an attributed
string - passes these gates. This means **attributed identity MUST
NOT be injected into `UserIDContextKey`**.

### Existing Scaffolding

- A `"service"` RBAC role is defined
  (`shared/platform/auth/rbac.go:32`) with account/position/
  transaction permissions but is assigned to no identity -
  forward-looking scaffolding for system actors.
- OAuth2 client credentials exist for service-to-service token
  exchange (`shared/platform/auth/service_auth.go`) but are not used
  by the scheduler.

## Solution: Phased Approach

The original question - "per-tenant system user with auth token" - is
the right destination but wrong starting point. The dependency chain
is: attribution before authentication, scheduler hardening before
manifest bridge.

### Design Principle: Attributed Identity vs Authenticated Identity

**Attributed identity** is a structured string in context that appears
in audit trails. It answers "who did this?" without cryptographic
proof. Sufficient for SOC 2 Type I and most Type II audits with
compensating controls.

**Authenticated identity** is a verified principal (JWT) that passes
through the auth interceptor chain. It answers "who did this AND were
they authorized?" Required when scheduled work makes cross-service
authenticated gRPC calls.

Phase A provides attributed identity. Phase C provides authenticated
identity. The `Actor` struct is designed to support both without data
migration.

### Critical Design Decision: Separate Context Keys

Attributed system actor identity MUST use a separate
`SystemActorContextKey`, NOT the existing `UserIDContextKey`. This is
non-negotiable based on verified code evidence:

- `GetUserIDFromContext` is used as an auth gate in 5+ identity service endpoints
- A JWT `sub` claim could theoretically contain `system:scheduler:*` strings, creating namespace collision
- Separate context keys create a clean boundary: JWT path populates
  `UserIDContextKey`, scheduler path populates
  `SystemActorContextKey`, they can never collide

### The `Actor` Struct

A single typed struct replaces context key proliferation:

```go
type Actor struct {
    ID            string    // "system:scheduler:billing" or user UUID
    Type          ActorType // Human, Scheduler, Worker, Migration
    Authenticated bool      // true only if set by auth interceptor
    Source        string    // "grpc-interceptor", "cron-scheduler", "catch-up"
}
```

- The gRPC interceptor sets `Actor{ID: userID, Type: Human,
  Authenticated: true, Source: "grpc-interceptor"}`
- The scheduler sets `Actor{ID: "system:scheduler:billing",
  Type: Scheduler, Authenticated: false, Source: "cron-scheduler"}`
- Audit hooks read `actor.ID` for `changed_by` regardless of type
- Auth gates check `actor.Authenticated` - attributed strings never
  pass auth checks
- Future actor types (workers, migrations, webhooks) extend via
  `ActorType` without new context keys

### `changed_by` Format

`system:scheduler:{service}` - no tenant ID. The tenant is implicit
in the schema-scoped audit trail. Including tenant ID is redundant,
creates privacy leakage in cross-tenant audit views, and renders
poorly in the UI.

## Deliverables

### Deliverable A: Scheduler Hardening + Attribution

**Scope:** Standalone value for the 3 existing schedulers. No manifest changes, no proto changes, no API surface changes.

**Estimated complexity:** 5 story points

#### A.1: `Actor` Struct and Context Key

Create `shared/platform/auth/actor.go`:

- `Actor` struct with `ID`, `Type`, `Authenticated`, `Source` fields
- `ActorType` enum: `Human`, `Scheduler`, `Worker`, `Migration`
- `ActorContextKey` context key
- `WithActor(ctx, actor)` and `ActorFromContext(ctx)` helpers
- Update `audit.GetUserFromContext()` to check `ActorContextKey` first, then `UserIDContextKey`, then fall back to `DefaultAuditUser`

#### A.2: Scheduler Attribution

In `shared/platform/scheduler/cron.go`, `executeJob()`:

- Inject `Actor{ID: "system:scheduler:{schedulerName}", Type: Scheduler,
  Authenticated: false, Source: "cron-scheduler"}` into context
- Inject `audit.WithCorrelationID(ctx, execID.String())` to link all audit records from one execution
- For catch-up executions, use `Source: "catch-up"`

#### A.3: Tenant Status Check

In `executeJob()`, before calling the executor:

- Query tenant status (active/suspended/deprovisioned)
- Skip execution for non-active tenants, record as `SKIPPED` with reason
- Known limitation: tenant can be suspended mid-execution. Saga-level handling is a future concern.

#### A.4: Concurrency Limiter

In `executeJob()` or `CronScheduler`:

- Add a configurable semaphore (default max 20 concurrent executions)
- Excess executions are `SKIPPED` with reason "concurrency limit reached"
- Prevents DB connection pool exhaustion when schedules align (e.g., `0 0 1 * *` for all tenants)

#### A.5: Refresh Jitter

In `refreshSchedules()`:

- Add random jitter (0-10s) to the refresh ticker interval
- Prevents synchronized `ListSchedules()` bursts when multiple replicas start simultaneously

#### A.6: ADR

Document:

- Attribution vs authentication decision and rationale
- `SystemActorContextKey` separate from `UserIDContextKey` rationale (with code evidence)
- `Actor` struct design and forward-compatibility with Phase C
- Known limitations (attributed strings are not cryptographically verified)

### Deliverable B: Manifest-Driven Scheduling

**Scope:** Bridges manifest `scheduled:` trigger declarations to the
`CronScheduler`. Uses database-backed schedule storage (proven by
forecasting pattern).

**Estimated complexity:** 8 story points

**Prerequisite:** Deliverable A (attribution must be in place before scaling schedule count)

#### B.1: `tenant_schedule` Database Table

Per-tenant-schema table written by manifest application, read by `ScheduleProvider`:

```sql
CREATE TABLE tenant_schedule (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    schedule_name VARCHAR(128) NOT NULL,
    saga_name VARCHAR(128) NOT NULL,
    cron_expr VARCHAR(64) NOT NULL,
    enabled BOOLEAN NOT NULL DEFAULT true,
    manifest_version_id UUID,
    metadata JSONB,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE(schedule_name)
);
```

`manifest_version_id` provides traceability: which manifest version created this schedule.

#### B.2: Manifest Application Pipeline

During `ApplyManifest`, for `scheduled:` triggers:

- Parse schedule configuration from manifest
- Translate friendly abstractions to cron expressions (if applicable)
- Diff declared schedules against existing `tenant_schedule` rows
- Insert/update/delete schedule rows
- Return registered schedules with `next_execution` time in the apply response

#### B.3: Unified `ScheduleProvider`

A `TenantScheduleProvider` that queries `tenant_schedule` across all
tenant schemas. Replaces or supplements per-service providers:

- Billing: migrate from env var to `tenant_schedule`
- Reconciliation: implement against `tenant_schedule` instead of Reference Data stub
- Forecasting: can adopt `tenant_schedule` or keep `forecasting_strategy` (per-service decision)

#### B.4: Manifest Schedule Validation

Enforced at manifest validation time:

- Minimum cron interval: 15 minutes
- Maximum schedules per tenant manifest: 10-20 (configurable)
- Reject syntactically valid but semantically nonsensical expressions (e.g., `0 0 31 2 *`)
- Warn on very infrequent schedules (annually) as likely bugs

#### B.5: Per-Tenant Execution Limits

Runtime guardrails in `executeJob()`:

- Per-tenant concurrent execution limit (3-5, configurable)
- Excess executions recorded as `SKIPPED` with tenant-specific reason
- Distinct from the global semaphore in A.4

#### B.6: Schedule Health Monitoring

Observability (can ship in parallel):

- Execution latency histogram per scheduler/tenant
- Lock contention metrics
- Expected-vs-actual execution frequency check (alert when a schedule hasn't fired in 2x its expected interval)
- Redis health metric

#### B.7: Manifest DX Design Spike

Design decision (before B.2 implementation):

- Raw cron expressions only? (`schedule: "0 2 1 * *"`)
- Friendly abstractions? (`schedule: { every: "1h" }`,
  `schedule: { monthly: { day: 1, hour: 2 } }`)
- Named presets? (`schedule: "monthly_billing"` mapping to a reference
  data entry)
- Resolve MCP docs contradiction (`scheduled:<cron>` vs `scheduled:<name>`)

The `tenant_schedule` table decouples this decision from the scheduler -
manifest DX can evolve independently because the translation to cron
expressions happens at the application layer.

### Deliverable C: Authenticated System Identity

**Scope:** Per-tenant system user with JWT-scoped execution. Full auth
chain for scheduled work.

**Estimated complexity:** 13 story points

**Trigger:** Required when scheduled sagas need to make cross-service
authenticated gRPC calls, OR when a customer requires SOC 2 Type II
with cryptographically verifiable chain of custody.

#### C.1: Per-Tenant System User

- Created during tenant provisioning as a post-provisioning hook
- Assigned the existing `"service"` RBAC role
- Per-service role scoping if needed (billing doesn't need the same permissions as forecasting)
- Lifecycle: created on provision, suspended on tenant suspend, deprovisioned on tenant deprovision

#### C.2: Per-Execution Token Minting

- Scheduler mints short-lived JWTs per execution (not long-lived cached credentials)
- Token lifetime = `ExecutionTimeout` + buffer (e.g., 15 minutes)
- Minting is in-process (no external call that can fail)
- Token carries tenant ID, service role, and execution correlation ID

#### C.3: Token-Scoped Saga Execution

- Executor injects JWT into context before saga execution
- Saga steps that make gRPC calls carry the token through interceptors
- Token-expiry-mid-saga handling: design upfront (fail + compensate, or refresh mid-saga)

#### C.4: Credential Lifecycle

- No long-lived credentials to rotate (per-execution minting)
- System user suspension on tenant deactivation
- Monitoring: alert on system user token mint failures

## Out of Scope

- Removing `public.platform_saga_definition` table (control-plane uses it for `apply_manifest`)
- Changing the forecasting service's `forecasting_strategy` table (can adopt `tenant_schedule` or keep its own)
- Tenant-to-tenant data sharing / mesh scheduling (future architecture)
- Schedule-triggered notifications to tenants (future DX feature)

## Verification

### Deliverable A

1. Existing scheduler tests pass with `Actor` context injection
2. `changed_by` fields show `system:scheduler:{service}` instead of `"system"`
3. Audit records carry `correlation_id` linking to `scheduler_execution.id`
4. Suspended tenant's schedules are skipped with audit trail
5. Concurrent execution capped at semaphore limit

### Deliverable B

1. Manifest with `scheduled:` trigger creates `tenant_schedule` row
2. Schedule appears in `CronScheduler` within 60s refresh interval
3. Apply response includes registered schedules with next execution time
4. Cron expressions below 15-min floor are rejected at validation
5. Per-tenant schedule count exceeding cap is rejected

### Deliverable C

1. Scheduled saga steps can make authenticated gRPC calls to other services
2. Token carries correct tenant ID and service role
3. Token expiry is handled gracefully (saga fails cleanly, not silently)

## References

- Six Thinking Hats analysis: 5-person panel (security, distributed systems, SRE, product, compliance)
- Key code paths: `shared/platform/scheduler/cron.go`, `shared/platform/auth/`, `shared/platform/audit/`
- Existing patterns: forecasting `StrategyRepository.ListAllActive()` (DB-backed schedule provider)
- Unused scaffolding: `"service"` RBAC role (`shared/platform/auth/rbac.go:32`)
