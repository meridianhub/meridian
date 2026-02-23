# PostgreSQL 16 Migration Compatibility Report

**Generated:** 2026-02-23
**Scope:** All Atlas migration files across `services/*/migrations/*.sql`
**Target:** PostgreSQL 16 (demo deployment on DigitalOcean)
**Source:** CockroachDB (production)
**Total files audited:** 113 SQL migration files across 12 services

---

## Executive Summary

| Category | Count |
|----------|-------|
| Total migration files | 113 |
| Files with incompatibilities | 3 |
| Files with notable differences (compatible after correction) | 1 |
| Files fully compatible as-is | 109 |

The migration corpus is in **good shape for PostgreSQL 16**. Most migrations were written with
standard SQL that runs on both CockroachDB and PostgreSQL. Three files contain a CRDB-specific
syntax (`ADD CONSTRAINT IF NOT EXISTS`) that PostgreSQL does not support. One file uses a
schema-qualified `DROP INDEX … CASCADE` pattern that requires replacement with the PostgreSQL
equivalent. The `gen_random_uuid()` function, widely used across all services, is natively
available in PostgreSQL 13+ with no extension required.

No CRDB-exclusive features were found: no hash-sharded indexes, no `SPLIT AT`, no zone
configurations, no `crdb_internal` references, no `SHOW CLUSTER SETTING`, no PL/pgSQL `DO $$`
blocks, no `LANGUAGE plpgsql` functions or triggers, no range types, no interleaved tables.

---

## Issue 1 (BLOCKING): `ADD CONSTRAINT IF NOT EXISTS` — Not Supported in PostgreSQL

**Severity:** Blocking — migrations will fail with a syntax error

`ALTER TABLE … ADD CONSTRAINT IF NOT EXISTS` is not valid syntax in any PostgreSQL release
including PostgreSQL 16. PostgreSQL raises `ERROR: syntax error at or near "IF"`.

### Affected Files

#### `services/reference-data/migrations/20260128000002_versioned_platform_sagas_constraints.sql`

Lines 16 and 20:

```sql
ALTER TABLE public.platform_saga_definition
  ADD CONSTRAINT IF NOT EXISTS chk_platform_saga_definition_status
    CHECK (status IN ('ACTIVE', 'DEPRECATED'));

ALTER TABLE public.platform_saga_definition
  ADD CONSTRAINT IF NOT EXISTS chk_platform_saga_definition_previous_version
    CHECK (previous_version ~ '^[0-9]+\.[0-9]+\.[0-9]+$' OR previous_version IS NULL);
```

#### `services/reference-data/migrations/20260129000002_bitemporal_platform_sagas_constraints.sql`

Line 15:

```sql
ALTER TABLE public.platform_saga_definition
  ADD CONSTRAINT IF NOT EXISTS chk_platform_saga_definition_validity_range
    CHECK (valid_to IS NULL OR valid_to > valid_from);
```

### Required Fix

Remove `IF NOT EXISTS` from all `ADD CONSTRAINT` statements. The idempotency guard is
unnecessary for Atlas-managed migrations (each migration runs exactly once).

```sql
-- Before (CRDB-only):
ALTER TABLE public.platform_saga_definition
  ADD CONSTRAINT IF NOT EXISTS chk_platform_saga_definition_status
    CHECK (status IN ('ACTIVE', 'DEPRECATED'));

-- After (PostgreSQL-compatible):
ALTER TABLE public.platform_saga_definition
  ADD CONSTRAINT chk_platform_saga_definition_status
    CHECK (status IN ('ACTIVE', 'DEPRECATED'));
```

Apply the same change for `chk_platform_saga_definition_previous_version` and
`chk_platform_saga_definition_validity_range`.

---

## Issue 2 (BLOCKING): Schema-Qualified `DROP INDEX` for Constraint-Backed Unique Index

**Severity:** Blocking — behavior diverges between CRDB and PostgreSQL

**File:**
`services/reference-data/migrations/20260127000001_fix_platform_saga_unique_constraint.sql`
(line 28)

```sql
-- CockroachDB requires DROP INDEX CASCADE for unique constraints.
-- PostgreSQL requires ALTER TABLE DROP CONSTRAINT (handled by migration runner).
-- This migration targets CockroachDB (production). The PostgreSQL test helper
-- applies the equivalent ALTER TABLE DROP CONSTRAINT before running this file.
DROP INDEX IF EXISTS "public"."uq_platform_saga_definition_name" CASCADE;
```

The migration's own comment acknowledges this divergence. In CockroachDB, `DROP INDEX CASCADE`
is required to drop a unique index that backs a constraint. In PostgreSQL, the correct method
is `ALTER TABLE … DROP CONSTRAINT`.

`DROP INDEX IF EXISTS "public"."uq_platform_saga_definition_name" CASCADE` will succeed on
PostgreSQL **only if** the index was created with `CREATE UNIQUE INDEX`. If the index was
created implicitly by a table `UNIQUE` constraint (as is the case here), PostgreSQL requires
`ALTER TABLE DROP CONSTRAINT` instead.

The source migration (`20260125000001_platform_saga_definition.sql`) created this constraint
via a `UNIQUE(name)` table constraint. Dropping it on PostgreSQL requires:

```sql
ALTER TABLE public.platform_saga_definition
  DROP CONSTRAINT IF EXISTS uq_platform_saga_definition_name;
```

### Required Fix

Replace the CRDB-specific `DROP INDEX … CASCADE` with the PostgreSQL-compatible form:

```sql
-- Before (CRDB-only for constraint-backed unique indexes):
DROP INDEX IF EXISTS "public"."uq_platform_saga_definition_name" CASCADE;

-- After (PostgreSQL-compatible):
ALTER TABLE public.platform_saga_definition
  DROP CONSTRAINT IF EXISTS uq_platform_saga_definition_name;
```

> **Note:** PostgreSQL 16 supports `DROP CONSTRAINT IF EXISTS` syntax.

---

## Issue 3 (INFORMATIONAL): `SERIAL` vs Explicit Sequence Semantics

**Severity:** Informational — functionally compatible, but semantics differ

**File:** `services/tenant/migrations/20251216000001_initial.sql` (line 131)

```sql
id SERIAL PRIMARY KEY,
```

In CockroachDB, `SERIAL` generates unique but non-sequential values using `unique_rowid()`.
In PostgreSQL, `SERIAL` creates a traditional auto-incrementing sequence with sequential values.

For the `tenant_provisioning_status` table, which uses `SERIAL` as a surrogate primary key
that is never exposed externally, this difference is immaterial. No fix required.

---

## Issue 4 (INFORMATIONAL): `ALTER TABLE … ALTER COLUMN TYPE` Workarounds

**Severity:** Informational — already handled correctly

Several migrations take a drop-and-recreate approach because CockroachDB rejected
`ALTER COLUMN TYPE` inside transactions. These workarounds are valid on PostgreSQL 16.

| File | Tables Affected | Pattern |
|------|-----------------|---------|
| `financial-accounting/20251219000001_fix_audit_schema_compatibility.sql` | `audit_log`, `audit_outbox` | DROP + CREATE |
| `payment-order/20251223000001_fix_audit_schema_compatibility.sql` | `audit_log`, `audit_outbox` | DROP + CREATE |
| `position-keeping/20251223000002_fix_audit_schema_compatibility.sql` | `audit_log`, `audit_outbox` | DROP + CREATE |
| `tenant/20260223000001_fix_audit_outbox_column_types.sql` | `audit_outbox`, `audit_log` | RENAME + ADD |

The `financial-accounting/20260105000001_multi_asset_support.sql` migration uses standard
`ALTER COLUMN "currency" TYPE character varying(32)`, which is fully supported in PostgreSQL 16.

---

## Compatible Patterns (No Changes Required)

The following patterns are used extensively and are fully compatible with PostgreSQL 16:

| Pattern | Occurrences | Notes |
|---------|-------------|-------|
| `gen_random_uuid()` | ~90 | Built-in in PostgreSQL 13+; no extension needed |
| `TIMESTAMPTZ` | Ubiquitous | Alias for `TIMESTAMP WITH TIME ZONE` |
| `JSONB` columns | ~30 | Fully supported |
| `INET` type | ~10 | Fully supported |
| Partial indexes (`WHERE` clause) | ~25 | Fully supported |
| `CREATE INDEX IF NOT EXISTS` | ~20 | Fully supported |
| `ADD CONSTRAINT … CHECK` (no `IF NOT EXISTS`) | Many | Fully supported |
| `COMMENT ON TABLE/COLUMN` | Many | Fully supported |
| `CREATE OR REPLACE VIEW` | Several | Fully supported |
| `ON DELETE RESTRICT/CASCADE` | Many | Fully supported |
| `BIGINT`, `VARCHAR`, `TEXT`, `BOOLEAN` | Ubiquitous | Standard SQL |
| `now()` / `NOW()` / `CURRENT_TIMESTAMP` | Ubiquitous | Fully supported |
| `RENAME COLUMN` | Several | Fully supported |
| `DROP CONSTRAINT` | Several | Fully supported |
| `DROP INDEX IF EXISTS name` (unqualified) | Several | Fully supported |

### Multi-Migration Split Patterns (CRDB Workarounds That Work on PostgreSQL Too)

Many services split schema changes into two migrations to satisfy CRDB's requirement that new
columns be "public" before indexes or DML can reference them. These patterns are valid on
PostgreSQL (PostgreSQL has no such restriction, but the splits cause no harm):

- `add_column` → `add_index` (column in migration N, partial index in migration N+1)
- DDL → DML (schema changes in migration N, data backfills in migration N+1)
- DROP constraints → ADD constraints (modifications split across migrations)

---

## Recommended Migration Strategy for PostgreSQL

### Pre-Migration (One-Time Setup)

1. Provision a PostgreSQL 16 database.
2. No extension installation required — `gen_random_uuid()` is built-in since PostgreSQL 13.

### Fixes Applied in This Branch

The following files were updated in this branch:

1. `services/reference-data/migrations/20260127000001_fix_platform_saga_unique_constraint.sql`
   — Replaced `DROP INDEX … CASCADE` with `ALTER TABLE DROP CONSTRAINT`
2. `services/reference-data/migrations/20260128000002_versioned_platform_sagas_constraints.sql`
   — Removed `IF NOT EXISTS` from two `ADD CONSTRAINT` statements
3. `services/reference-data/migrations/20260129000002_bitemporal_platform_sagas_constraints.sql`
   — Removed `IF NOT EXISTS` from one `ADD CONSTRAINT` statement
4. `services/reference-data/migrations/atlas.sum` — Regenerated after migration edits

### Atlas Configuration for PostgreSQL

The Atlas config (`atlas.hcl`) per service needs a PostgreSQL dev-url for the demo environment:

```hcl
env "demo" {
  url = "postgresql://<user>:<password>@<host>:<port>/<db>?sslmode=require"
  dev = "docker://postgres/16/dev"
  migration {
    dir = "file://migrations"
  }
}
```

### Migration Execution Order

Run migrations per service in this order to respect provisioning dependencies:

1. `control-plane`
2. `tenant`
3. `party`
4. `reference-data`
5. `financial-accounting`
6. `position-keeping`
7. `current-account`
8. `internal-bank-account`
9. `market-information`
10. `payment-order`
11. `forecasting`
12. `reconciliation`

### Validation Checklist

- [ ] `ADD CONSTRAINT IF NOT EXISTS` removed from 3 files in `reference-data/migrations`
- [ ] `DROP INDEX … CASCADE` replaced with `ALTER TABLE DROP CONSTRAINT`
- [ ] `atlas.sum` for `reference-data` regenerated
- [ ] All 12 services migrate cleanly against a fresh PostgreSQL 16 instance
- [ ] Smoke test: `gen_random_uuid()` works (default on PG 16)
- [ ] Smoke test: `JSONB` columns accept and return expected payloads
- [ ] Smoke test: partial indexes created and used by query planner

---

## Service-by-Service Summary

| Service | Files | Issues | Status |
|---------|-------|--------|--------|
| control-plane | 4 | None | Compatible |
| current-account | 16 | None | Compatible |
| financial-accounting | 5 | None (DROP+CREATE workarounds) | Compatible |
| forecasting | 2 | None | Compatible |
| internal-bank-account | 9 | None | Compatible |
| market-information | 6 | None | Compatible |
| party | 12 | None | Compatible |
| payment-order | 11 | None (DROP+CREATE workarounds) | Compatible |
| position-keeping | 11 | None (DROP+CREATE workarounds) | Compatible |
| reconciliation | 9 | None | Compatible |
| reference-data | 24 | 3 files fixed (Issues 1 and 2) | Fixed |
| tenant | 4 | SERIAL semantics (informational) | Compatible |
