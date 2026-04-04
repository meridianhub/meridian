# Tenant Isolation Audit - 2026-04-04

**Scope:** All services under `services/` and `shared/` packages  
**Auditor:** Automated audit  
**Date:** 2026-04-04

## Summary

Three categories were audited:

1. Direct database connections bypassing `TenantGuard`
2. Optional tenant routes where strict tenant enforcement may be appropriate
3. `WithTenantGuardBypass` usage outside legitimate infrastructure operations

**One confirmed bug** was found and fixed in this PR:

- `forecasting/ComputeForwardCurve` fetched a strategy by UUID without verifying the result
  belongs to the requesting tenant, enabling cross-tenant strategy execution.

---

## Category 1: Direct Database Connections

Meridian's primary tenant isolation mechanism for GORM connections is `shared/platform/db.TenantGuard`,
registered via `shared/platform/bootstrap.NewDatabase()`. For pgx connections,
`shared/platform/db.GuardedPgxPool` is the canonical enforcer. The findings below identify
connections that bypass these mechanisms.

### Finding 1.1 - audit-worker: GORM without TenantGuard

**Risk:** LOW  
**Status:** Intentional - acceptable pattern

**Files:**

- `services/audit-worker/app/container.go:81`
- `services/audit-worker/cmd/main.go:112`

```go
gormDB, err := gorm.Open(postgres.Open(c.Config.Database.URL), &gorm.Config{
    SkipDefaultTransaction: true,
})
```

**Context:** The `audit-worker` receives Kafka messages and writes to tenant-scoped `audit_log` tables.
All writes go through `TenantAuditWriter.WriteAuditEvent()`, which uses
`db.WithGormTenantTransaction()` to set `search_path` per write. TenantGuard is not registered.

**Assessment:** Acceptable - the worker is single-purpose and all writes route through
`WithGormTenantTransaction`. Registering TenantGuard would provide defense-in-depth without
breaking behaviour. Not a bug, but a defense-in-depth gap.

---

### Finding 1.2 - api-gateway: identityDB without TenantGuard

**Risk:** MEDIUM  
**Status:** Intentional for SSO flows - defense-in-depth gap

**File:** `services/api-gateway/cmd/main.go:399`

```go
identityDB, err := gorm.Open(postgres.Open(config.DatabaseURL), &gorm.Config{
    SkipDefaultTransaction: true,
})
// ...
identityRepo := identitypersistence.NewRepository(identityDB)
```

**Context:** Used by the Dex SSO connector for identity lookups during OAuth callback flow.
`identity/adapters/persistence/repository.go:28` correctly uses `db.WithGormTenantTransaction()`
for all queries. TenantGuard is absent.

**Assessment:** Without TenantGuard, any future code using this connection without
`WithGormTenantTransaction` has no safety net. The current code is correct but lacks
defense-in-depth. Using `bootstrap.NewDatabase()` here would be more resilient.

---

### Finding 1.3 - api-gateway: outbox DB without TenantGuard

**Risk:** LOW  
**Status:** Dev/CI only - not a production concern

**File:** `services/api-gateway/cmd/main.go:322`

```go
// This is the dev/CI adapter; cross-service DB access is forbidden in production (ADR-0002).
gormDB, err := gorm.Open(postgres.Open(config.DatabaseURL), &gorm.Config{
    SkipDefaultTransaction: true,
})
```

**Assessment:** Explicitly marked as ADR-0002 violation acceptable only in dev/CI.
The comment correctly documents the intent. No action required.

---

### Finding 1.4 - position-keeping: raw pgxpool without GuardedPgxPool

**Risk:** MEDIUM  
**Status:** Custom pattern - silent fallthrough when tenant absent

**File:** `services/position-keeping/app/container.go:247`

```go
pool, err := pgxpool.NewWithConfig(ctx, poolConfig)
// ...
c.PositionLogRepository = persistence.NewPostgresRepository(c.DBPool)
c.MeasurementRepository = persistence.NewMeasurementRepository(c.DBPool)
```

**File:** `services/position-keeping/adapters/persistence/postgres_repository.go:42-65`

```go
func (r *PostgresRepository) setSearchPath(ctx context.Context, tx pgx.Tx) error {
    tenantID, ok := tenant.FromContext(ctx)
    if !ok {
        // Single-tenant mode: no scoping needed
        return nil  // <-- SILENT FALLTHROUGH
    }
    // ...
}
```

**Assessment:** Repositories implement their own `setSearchPath` rather than `GuardedPgxPool`.
The critical gap is that **missing tenant context silently skips isolation** rather than
returning an error. If a request context lacks a tenant (middleware failure, internal call
path), queries run against the connection's default `search_path` without error or log.

Compare with `reference-data` and `control-plane`, which return `tenant.ErrMissingTenantContext`.
The silent no-op is a latent risk; a defensive error is preferable.

---

### Finding 1.5 - market-information: raw pgxpool without GuardedPgxPool

**Risk:** MEDIUM  
**Status:** Custom pattern - same silent fallthrough as position-keeping

**File:** `services/market-information/app/container.go:112`  
**File:** `services/market-information/adapters/persistence/base_repository.go:32-50`

```go
func (r *baseRepository) setSearchPath(ctx context.Context, tx pgx.Tx) error {
    tenantID, ok := tenant.FromContext(ctx)
    if !ok {
        // Single-tenant mode: no scoping needed
        return nil  // <-- SILENT FALLTHROUGH
    }
    // ...
}
```

**Assessment:** Identical issue to position-keeping. No enforcement on missing tenant context.

---

### Finding 1.6 - forecasting: column-based isolation without schema isolation (FIXED)

**Risk:** HIGH (was pre-fix)  
**Status:** BUG FIXED - see code changes in this PR

**Files:**

- `services/forecasting/adapters/persistence/strategy_repository.go:144-161`
- `services/forecasting/handler/forecasting_handler.go:80-138`

**Problem:** The `forecasting` service uses **column-based tenant isolation** (`WHERE tenant_id = $1`)
rather than schema-based isolation used by the rest of Meridian. The `FindByID` method queried
only by UUID with no tenant filter:

```go
// BEFORE (buggy)
func (r *StrategyRepository) FindByID(ctx context.Context, id uuid.UUID) (domain.ForecastingStrategy, error) {
    query := `SELECT ... FROM forecasting_strategy WHERE id = $1`
    entity, err := r.scanStrategy(ctx, query, id)
    // ...
}
```

The `ComputeForwardCurve` handler extracted `tenantID` from context but never verified the
returned strategy belongs to that tenant:

```go
tenantID, err := tenant.RequireFromContext(ctx)  // extracts tenant
strategy, err := s.repo.FindByID(ctx, strategyID)  // no tenant filter!
// strategy.TenantID() never compared to tenantID
```

**Impact:** Any authenticated tenant could execute another tenant's forecasting strategy by
providing its UUID. Strategy content (Starlark code, input/output dataset codes, reference data
resolution keys) would be exposed and executed.

**Fix:** Added tenant ownership verification in the `ComputeForwardCurve` handler.
See the code change in this PR.

---

### Finding 1.7 - control-plane: raw pgxpool with fail-fast isolation

**Risk:** LOW  
**Status:** Acceptable - fail-fast on missing tenant

**File:** `services/control-plane/cmd/main.go:103`

```go
pool, err := pgxpool.New(ctx, dbURL)
```

**Assessment:** Repositories in `control-plane` use `setSearchPath` that returns
`tenant.ErrMissingTenantContext` when no tenant is in context (fail-fast). This is stricter
than position-keeping/market-information. The absence of `GuardedPgxPool` is a
defense-in-depth gap but not a bug.

---

### Finding 1.8 - reference-data: raw pgxpool with fail-fast isolation

**Risk:** LOW  
**Status:** Acceptable - fail-fast on missing tenant

**File:** `services/reference-data/cmd/main.go:145`

**Assessment:** All persistence layers (`mapping/repository.go`, `saga/postgres_registry.go`,
`registry/postgres_registry.go`, etc.) return `tenant.ErrMissingTenantContext` when tenant
is absent. This is the correct defensive pattern for raw pgxpool usage.

---

### Finding 1.9 - payment-order / reconciliation: pgxpool for scheduler execution store

**Risk:** LOW  
**Status:** Intentional - infrastructure use

**Files:**

- `services/payment-order/app/container.go:515`
- `services/reconciliation/app/container.go:468`

**Assessment:** Both services create a separate `pgxpool.Pool` exclusively for `PgExecutionStore`
(cron scheduler audit trail). The `PgExecutionStore` stores execution metadata in a
platform-level table, not tenant-scoped data. The scheduler sets tenant context when it has
one (optional scoping). This is a legitimate infrastructure use.

---

### Finding 1.10 - tenant/provisioner: gorm.Open without TenantGuard

**Risk:** LOW  
**Status:** Intentional - provisioner operates at platform level

**File:** `services/tenant/provisioner/provisioner_helpers.go:27`

**Assessment:** The provisioner creates per-service DB connections to provision schemas.
It explicitly uses `bypassCtx` (wrapping `WithTenantGuardBypass`) throughout
`provisioner_persistence.go`. This is legitimate infrastructure code that must operate
above the tenant level.

---

## Category 2: Optional Tenant Routes

### Finding 2.1 - api-gateway: HandlerOptionalTenant for /dex/

**Risk:** LOW  
**Status:** Intentional and documented

**File:** `services/api-gateway/server.go:374`

```go
func (s *Server) wrapWithTenantOnly(inner http.Handler) http.Handler {
    if s.tenantResolver != nil {
        return s.tenantResolver.HandlerOptionalTenant(inner)
    }
    return inner
}
```

Used only for `/dex/` route:

```go
func (s *Server) registerDexRoutes() {
    if s.dexHandler != nil {
        dexHandler := s.wrapWithTenantOnly(s.dexHandler)
        s.mux.Handle("/dex/", dexHandler)
    }
}
```

**Context:** Dex OIDC endpoints manage their own authentication. They need tenant context
(from subdomain) when available for credential lookups in the `MeridianConnector`, but
requests without a tenant subdomain must be allowed through. The code comment documents
this intent.

**Assessment:** Correct design. All other routes use `wrapWithAuthChain` which includes
strict tenant resolution. This is the only `HandlerOptionalTenant` usage in production code.

---

## Category 3: WithTenantGuardBypass Usage

### Finding 3.1 - tenant/provisioner/provisioner_persistence.go

**Status:** Intentional

```go
// The provisioner operates at platform level — all DB operations bypass tenant scoping.
func bypassCtx(ctx context.Context) context.Context {
    return dbpkg.WithTenantGuardBypass(ctx)
}
```

**Assessment:** Correct use. Provisioner writes to `tenant_provisioning` table in the
public/platform schema.

---

### Finding 3.2 - tenant/adapters/persistence/repository.go

**Status:** Intentional

```go
// The tenant service operates at the platform level (managing tenants),
// so all its queries bypass tenant-scoped search_path restrictions.
func (r *Repository) conn(ctx context.Context) *gorm.DB {
    return r.db.WithContext(dbpkg.WithTenantGuardBypass(ctx))
}
```

**Assessment:** Correct use. Tenant registry is a cross-tenant system table.

---

### Finding 3.3 - internal/bootstrap/master_tenant.go:220

**Status:** Intentional

```go
// Bypass tenant guard - this is a platform-level operation on the
// tenant_provisioning table in public schema, not tenant-scoped data.
bypassCtx := db.WithTenantGuardBypass(ctx)
```

**Assessment:** Correct use for bootstrap/provisioning operations.

---

### Finding 3.4 - shared/platform/db/gorm_tenant_scope.go:87

**Status:** Internal machinery

**Assessment:** `WithGormTenantTransaction` internally uses bypass context when executing
`SET LOCAL search_path` - this is the implementation mechanism, not a bypass of isolation.

---

## Isolation Pattern Comparison

| Service | Mechanism | Fail-safe on missing tenant | Pattern |
|---------|-----------|----------------------------|---------|
| current-account | `bootstrap.NewDatabase` + `TenantGuard` + `WithGormTenantTransaction` | Yes (TenantGuard blocks) | Schema-based (GORM) |
| financial-accounting | Same | Yes | Schema-based (GORM) |
| internal-account | Same | Yes | Schema-based (GORM) |
| party | Same | Yes | Schema-based (GORM) |
| operational-gateway | Same | Yes | Schema-based (GORM) |
| payment-order | Same (domain); pgxpool for scheduler | Yes (domain); N/A (infra) | Schema-based (GORM) |
| reconciliation | Same (domain); pgxpool for scheduler | Yes (domain); N/A (infra) | Schema-based (GORM) |
| reference-data | Raw pgxpool + fail-fast `setSearchPath` | Yes (errors on missing) | Schema-based (pgx) |
| control-plane | Raw pgxpool + fail-fast `setSearchPath` | Yes (errors on missing) | Schema-based (pgx) |
| position-keeping | Raw pgxpool + silent `setSearchPath` | **No (silent passthrough)** | Schema-based (pgx) |
| market-information | Raw pgxpool + silent `setSearchPath` | **No (silent passthrough)** | Schema-based (pgx) |
| forecasting | Raw pgxpool + column filter | Partial (FindByID had no filter - **fixed**) | Column-based |
| tenant | `bootstrap.NewDatabase` + explicit bypass | N/A (platform-level) | Cross-tenant |
| audit-worker | Raw GORM + `WithGormTenantTransaction` | Partial (no TenantGuard) | Schema-based (GORM) |
| identity | Raw GORM (via api-gateway) + `WithGormTenantTransaction` | Partial (no TenantGuard) | Schema-based (GORM) |

---

## Recommendations

Listed by priority:

### P1 - Fix (Done): forecasting FindByID cross-tenant access

Fixed in this PR. Handler now verifies `strategy.TenantID() == string(tenantID)`.

### P2 - Improve: position-keeping and market-information silent fallthrough

Change `setSearchPath` in both services to return `tenant.ErrMissingTenantContext` when no
tenant context is present, matching the stricter pattern used by `reference-data` and
`control-plane`. This prevents silent data exposure if middleware fails to inject a tenant.

### P3 - Improve: Migrate audit-worker and api-gateway identityDB to bootstrap.NewDatabase

Register `TenantGuard` on these connections for defense-in-depth. This is a no-op change
for current behavior but provides a safety net against future code changes that accidentally
bypass tenant scoping.

### P4 - Consider: Migrate position-keeping and market-information to GuardedPgxPool

Replace raw `*pgxpool.Pool` with `db.GuardedPgxPool` to enforce tenant context at the
connection level. This aligns these services with the platform standard and makes isolation
enforcement structural rather than per-method.
