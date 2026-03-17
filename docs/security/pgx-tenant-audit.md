# pgx Tenant Guard Audit

## Overview

This document audits all raw pgx (non-GORM) database access paths in the Meridian codebase and documents the tenant isolation strategy for each.

## Guard Infrastructure

| File | Type | Description |
|------|------|-------------|
| `shared/platform/db/pgx_tenant_guard.go` | Guard functions | `RequirePgxTenantContext` validates tenant context or bypass before queries |
| `shared/platform/db/guarded_pgx_pool.go` | Pool wrapper | `GuardedPgxPool` wraps `*pgxpool.Pool`, enforces tenant context on Exec/Query/QueryRow/Begin |

### Usage

```go
// Wrap an existing pool
guarded := db.NewGuardedPgxPool(pool)

// All operations require tenant context
ctx := tenant.WithTenant(ctx, tenantID)
guarded.Exec(ctx, "SELECT 1") // OK

// Or bypass for infrastructure operations
ctx := db.WithPgxTenantBypass(ctx)
guarded.Exec(ctx, "SELECT 1") // OK

// Start a tenant-scoped transaction with search_path set
tx, err := guarded.BeginTenantTx(ctx)
```

## Audit of Raw pgx Usage

### 1. `shared/platform/db/pool.go` — PostgresPool

**Risk**: Low. This is a `database/sql` wrapper using the pgx driver, not direct pgx pool usage. It is used for connection pooling infrastructure. Tenant scoping is handled at the GORM layer via `TenantGuard` plugin.

**Action**: No change needed. The GORM TenantGuard already protects this path.

### 2. `shared/platform/events/outbox_pgx.go` — PgxOutboxRepository

**Risk**: Medium. Uses `*pgxpool.Pool` directly for outbox event operations. The `Publish` method extracts `tenant_id` from context and stores it in the `tenant_id` column, but `FetchUnprocessed`, `FetchAndLockForProcessing`, `MarkCompleted`, `MarkFailed`, `MarkProcessing`, `ResetStuckEntries`, and `GetPendingCount` query across all tenants by `service_name` only.

**Analysis**: The outbox is an infrastructure table (not per-tenant schema). The outbox processor intentionally reads cross-tenant to process events for all tenants. Tenant ID is stored as a data column for routing, not for access control.

**Action**: No guard needed. The outbox is a platform-level infrastructure concern that intentionally operates cross-tenant. The `tenant_id` column provides audit traceability, not isolation.

### 3. `shared/platform/scheduler/execution_store.go` — PgExecutionStore

**Risk**: Medium. Uses `*pgxpool.Pool` directly. Already implements `setSearchPath` which sets `SET LOCAL search_path` to the tenant schema when tenant context is present. However, if no tenant is in context, it silently proceeds without schema scoping.

**Analysis**: The scheduler execution store already has tenant awareness via `setSearchPath`. The gap is that it doesn't enforce tenant context — it degrades gracefully to public schema. For multi-tenant deployments, this could allow cross-tenant visibility of scheduler executions if a tenant context is accidentally missing.

**Action**: The existing `setSearchPath` pattern provides the schema isolation. Adding `GuardedPgxPool` here would require callers to always provide tenant context, which may break platform-level scheduler operations that run without tenant scope. Recommend documenting the existing behavior and adding bypass context for platform schedulers if stricter enforcement is desired in the future.

### 4. `shared/pkg/idempotency/postgres_service.go` — PostgresService

**Risk**: Low. Uses `*pgxpool.Pool` for idempotency key management. The `_idempotency_keys` table is a platform-level infrastructure table, not per-tenant. Keys are namespaced by their composite key string (which can include tenant identifiers at the application layer).

**Analysis**: Idempotency is a cross-cutting infrastructure concern. The table lives in the public schema and keys are self-namespacing. Operations like `EnsureTable`, `StartCleanup`, and lock management are platform-level.

**Action**: No guard needed. This is infrastructure-level, similar to migrations or health checks.

### 5. `shared/platform/testdb/pgx.go` — Test utilities

**Risk**: None. Test-only code that creates containers and applies migrations.

**Action**: No guard needed. Test infrastructure operates outside the tenant model.

## Summary

| Component | pgx Usage | Tenant Isolation | Guard Needed | Rationale |
|-----------|-----------|-----------------|--------------|-----------|
| PostgresPool | database/sql via pgx driver | GORM TenantGuard | No | Protected at GORM layer |
| PgxOutboxRepository | Direct pgxpool.Pool | tenant_id column (data, not access control) | No | Infrastructure table, cross-tenant by design |
| PgExecutionStore | Direct pgxpool.Pool | setSearchPath (when tenant in context) | No (existing) | Already tenant-aware; platform schedulers need bypass |
| PostgresService | Direct pgxpool.Pool | Key namespacing | No | Infrastructure table, cross-cutting concern |
| testdb | Direct pgxpool.Pool | N/A (test only) | No | Test infrastructure |

## Recommendations

1. **New pgx-based repositories** should accept `*db.GuardedPgxPool` (or `db.PgxQuerier`) instead of raw `*pgxpool.Pool` to enforce tenant context by default.
2. **Existing infrastructure components** (outbox, scheduler, idempotency) are correctly operating at the platform level and do not need tenant guards.
3. The `GuardedPgxPool` and `PgxQuerier` interface are available for any future service-level pgx code that needs tenant enforcement.
