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
| Files with incompatibilities | 4 |
| Files fixed in this branch | 2 |
| Files handled by pre-migration script | 1 |
| Files with notable differences (no fix required) | 1 |
| Files fully compatible as-is | 109 |

The migration corpus is in **good shape for PostgreSQL 16**. Most migrations were written with
standard SQL that runs on both CockroachDB and PostgreSQL. Three files have **blocking**
incompatibilities; one additional file has an informational difference that requires no fix:

1. Two files use `ADD CONSTRAINT IF NOT EXISTS` — a CockroachDB v21.2+ extension not supported
   in PostgreSQL 16. Fixed by removing `IF NOT EXISTS`.
2. One file uses `DROP INDEX … CASCADE` for a constraint-backed unique index — valid on
   CockroachDB but not PostgreSQL (which requires `ALTER TABLE DROP CONSTRAINT`). The migration
   file is **not** modified (that would break existing CockroachDB CI); instead a pre-migration
   SQL script handles this in the demo environment.
3. One file uses `SERIAL` (tenant service) — semantics differ between CRDB and PostgreSQL but
   are functionally compatible for this use case. No fix required.

The `gen_random_uuid()` function, used extensively across all services, is natively available
in PostgreSQL 13+ with no extension required.

No CRDB-exclusive features were found: no hash-sharded indexes, no `SPLIT AT`, no zone
configurations, no `crdb_internal` references, no `SHOW CLUSTER SETTING`, no PL/pgSQL `DO $$`
blocks, no `LANGUAGE plpgsql` functions or triggers, no range types, no interleaved tables.

---

## Issue 1 (BLOCKING): `ADD CONSTRAINT IF NOT EXISTS` — Not Supported in PostgreSQL

**Severity:** Blocking — migrations will fail with a syntax error
**Status:** Fixed in this branch

`ALTER TABLE … ADD CONSTRAINT IF NOT EXISTS` is not valid syntax in any PostgreSQL release
including PostgreSQL 16. PostgreSQL raises `ERROR: syntax error at or near "IF"`. This syntax
was added in CockroachDB v21.2 but has never been part of PostgreSQL.

### Affected Files (Fixed)

#### `services/reference-data/migrations/20260128000002_versioned_platform_sagas_constraints.sql`

Lines 16 and 20 — `IF NOT EXISTS` removed from two `ADD CONSTRAINT` statements.

#### `services/reference-data/migrations/20260129000002_bitemporal_platform_sagas_constraints.sql`

Line 15 — `IF NOT EXISTS` removed from one `ADD CONSTRAINT` statement.

### Fix Applied

```sql
-- Before (CRDB-only — fails on PostgreSQL 16):
ALTER TABLE public.platform_saga_definition
  ADD CONSTRAINT IF NOT EXISTS chk_platform_saga_definition_status
    CHECK (status IN ('ACTIVE', 'DEPRECATED'));

-- After (PostgreSQL-compatible — also works on CockroachDB):
ALTER TABLE public.platform_saga_definition
  ADD CONSTRAINT chk_platform_saga_definition_status
    CHECK (status IN ('ACTIVE', 'DEPRECATED'));
```

The `IF NOT EXISTS` guard was unnecessary in both databases. Atlas-managed migrations run
exactly once. The tenant provisioner handles duplicate-object errors via `isAlreadyExistsError`
for the per-tenant CockroachDB path, so explicit `IF NOT EXISTS` provides no additional safety.

---

## Issue 2 (BLOCKING): Schema-Qualified `DROP INDEX` for Constraint-Backed Unique Index

**Severity:** Blocking — PostgreSQL will raise an error at runtime
**Status:** Handled via pre-migration script (`deploy/demo/pg-pre-migration.sql`)

**File:**
`services/reference-data/migrations/20260127000001_fix_platform_saga_unique_constraint.sql`

```sql
DROP INDEX IF EXISTS "public"."uq_platform_saga_definition_name" CASCADE;
```

This statement is correct for CockroachDB, where `DROP INDEX CASCADE` drops a constraint-backed
unique index along with its constraint. On PostgreSQL, this fails with:

```text
ERROR: cannot drop index "uq_platform_saga_definition_name" because constraint
  "uq_platform_saga_definition_name" on table "platform_saga_definition" requires it
HINT: You can drop constraint "uq_platform_saga_definition_name" on table
  "platform_saga_definition" instead.
```

The constraint was explicitly named in migration `20260125000001_platform_saga_definition.sql`
(line 36), so PostgreSQL creates a backing index with the same name and owns it under the
constraint. PostgreSQL refuses to drop the index directly; the constraint must be dropped via
`ALTER TABLE … DROP CONSTRAINT`.

**Why the migration file is NOT modified:**

The E2E test suite runs against CockroachDB and uses this exact `DROP INDEX CASCADE` syntax.
Changing the file to PostgreSQL syntax would break existing CI. The migration's own comment
documents this divergence and notes that the PostgreSQL test helper handles it separately.

**Demo deployment solution:**

Run `deploy/demo/pg-pre-migration.sql` against the `meridian_reference_data` database before
executing Atlas migrations. This script drops the constraint using the PostgreSQL-compatible
`ALTER TABLE DROP CONSTRAINT IF EXISTS` syntax. When Atlas then runs
`20260127000001_fix_platform_saga_unique_constraint.sql`, the `DROP INDEX IF EXISTS` statement
is a no-op (the index no longer exists), and the rest of the migration proceeds normally.

```sql
-- deploy/demo/pg-pre-migration.sql
ALTER TABLE IF EXISTS public.platform_saga_definition
  DROP CONSTRAINT IF EXISTS uq_platform_saga_definition_name;
```

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

### Step 1: Run Pre-Migration Script

Before executing Atlas migrations, run the pre-migration script against the
`meridian_reference_data` database:

```bash
psql "$REFERENCE_DATA_DATABASE_URL" -f deploy/demo/pg-pre-migration.sql
```

This script handles the one CockroachDB-specific `DROP INDEX` statement that cannot be made
dual-compatible without breaking existing CockroachDB CI.

### Step 2: Run Atlas Migrations

Run Atlas migrations per service in the following order to respect provisioning dependencies:

1. `control-plane`
2. `tenant`
3. `party`
4. `reference-data`
5. `financial-accounting`
6. `position-keeping`
7. `current-account`
8. `internal-account`
9. `market-information`
10. `payment-order`
11. `forecasting`
12. `reconciliation`

### Atlas Configuration for PostgreSQL

The Atlas config (`atlas.hcl`) per service needs a PostgreSQL connection URL. Five services
(current-account, financial-accounting, party, payment-order, position-keeping) execute their
full migration history against PostgreSQL 16 in CI via `atlas migrate apply`; reference-data
migrations are not currently tested in CI. The `env "local"` and `env "ci"` configurations
use `dev = "docker://postgres/16/dev"` for schema diffing and lint validation.

### Validation Checklist

- [ ] `deploy/demo/pg-pre-migration.sql` run against `meridian_reference_data` database
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
| internal-account | 9 | None | Compatible |
| market-information | 6 | None | Compatible |
| party | 12 | None | Compatible |
| payment-order | 11 | None (DROP+CREATE workarounds) | Compatible |
| position-keeping | 11 | None (DROP+CREATE workarounds) | Compatible |
| reconciliation | 9 | None | Compatible |
| reference-data | 24 | 3 files (Issues 1 and 2) | 2 fixed, 1 via pre-migration script |
| tenant | 4 | SERIAL semantics (informational) | Compatible |

---

## Changes Made in This Branch

### Fixed Migration Files

- `services/reference-data/migrations/20260128000002_versioned_platform_sagas_constraints.sql`
  — Removed `IF NOT EXISTS` from two `ADD CONSTRAINT` statements
- `services/reference-data/migrations/20260129000002_bitemporal_platform_sagas_constraints.sql`
  — Removed `IF NOT EXISTS` from one `ADD CONSTRAINT` statement
- `services/reference-data/migrations/atlas.sum` — Regenerated after migration edits

### New Files

- `deploy/demo/pg-migration-compatibility-report.md` — This document
- `deploy/demo/pg-pre-migration.sql` — Pre-migration script for PostgreSQL demo deployment
