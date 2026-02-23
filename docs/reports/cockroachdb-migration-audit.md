# CockroachDB Migration Audit: PostgreSQL Compatibility Analysis

**Date**: 2026-02-23\
**Scope**: All Atlas migration files across `services/*/migrations/`\
**Total Files Audited**: 111 SQL files across 11 services\
**Purpose**: Identify CockroachDB-specific SQL syntax incompatible with standard PostgreSQL, for dual-database support planning.

---

## Executive Summary

The migration files are well-structured and already exhibit awareness of CockroachDB constraints.
Many files include explicit comments explaining why patterns are split across migrations.
The codebase has been thoughtfully developed for CockroachDB compatibility.

For PostgreSQL compatibility (dual-database support), the following categories of changes are required:

| Severity     | Category                                    | Files    | Services Affected              |
|--------------|---------------------------------------------|----------|--------------------------------|
| **Critical** | `ADD CONSTRAINT IF NOT EXISTS` syntax       | 2 files  | reference-data                 |
| **Critical** | `DROP INDEX ... CASCADE` syntax             | 1 file   | reference-data                 |
| **High**     | `SERIAL` type (CockroachDB maps differently)| 1 file   | tenant                         |
| **High**     | `verification_status` ENUM type             | 1 file   | party                          |
| **Medium**   | `TEXT[]` array columns                      | 2 files  | forecasting, reference-data    |
| **Medium**   | `INET` type inconsistency across services   | Many     | audit tables (informational)   |
| **Low**      | `public.platform_saga_definition` FK        | Multiple | reference-data                 |
| **Info**     | Intentional CockroachDB workarounds         | Many     | All                            |

---

## Severity Definitions

- **Critical**: Will cause migration to fail on PostgreSQL as written
- **High**: Will run on PostgreSQL but may behave differently or require manual review
- **Medium**: Works on both but has compatibility notes warranting attention
- **Low**: Architectural pattern that requires documentation for PostgreSQL deployments
- **Info**: Good practice documented in the codebase

---

## Critical Issues

### CRIT-1: `ADD CONSTRAINT IF NOT EXISTS` Syntax

**Files Affected**:

- `services/reference-data/migrations/20260128000002_versioned_platform_sagas_constraints.sql`
- `services/reference-data/migrations/20260129000002_bitemporal_platform_sagas_constraints.sql`

**CockroachDB Syntax** (not supported in PostgreSQL < 15):

```sql
ALTER TABLE public.platform_saga_definition
  ADD CONSTRAINT IF NOT EXISTS chk_platform_saga_definition_status
    CHECK (status IN ('ACTIVE', 'DEPRECATED'));
```

**Issue**: `ALTER TABLE ... ADD CONSTRAINT IF NOT EXISTS` is CockroachDB-specific syntax.
Standard PostgreSQL does not support `IF NOT EXISTS` for `ADD CONSTRAINT`.
This fails on PostgreSQL 14 and below; PostgreSQL 15+ added partial support for certain constraint types only.

**Required Change for PostgreSQL**:

```sql
-- Option A: Remove IF NOT EXISTS (requires idempotency handled by migration runner)
ALTER TABLE public.platform_saga_definition
  ADD CONSTRAINT chk_platform_saga_definition_status
    CHECK (status IN ('ACTIVE', 'DEPRECATED'));

-- Option B: Use DO block to check existence first (PostgreSQL-specific workaround)
DO $$ BEGIN
  IF NOT EXISTS (
    SELECT 1 FROM pg_constraint WHERE conname = 'chk_platform_saga_definition_status'
  ) THEN
    ALTER TABLE public.platform_saga_definition
      ADD CONSTRAINT chk_platform_saga_definition_status
        CHECK (status IN ('ACTIVE', 'DEPRECATED'));
  END IF;
END $$;
```

**Occurrence counts**:
`20260128000002_versioned_platform_sagas_constraints.sql` — 4 occurrences;
`20260129000002_bitemporal_platform_sagas_constraints.sql` — 1 occurrence.

---

### CRIT-2: `DROP INDEX ... CASCADE` Syntax

**File Affected**:

- `services/reference-data/migrations/20260127000001_fix_platform_saga_unique_constraint.sql`

**CockroachDB Syntax**:

```sql
DROP INDEX IF EXISTS "public"."uq_platform_saga_definition_name" CASCADE;
```

**Issue**: In CockroachDB, dropping a unique index that backs a constraint requires `CASCADE`.
In PostgreSQL, unique indexes backing constraints must be dropped via `ALTER TABLE DROP CONSTRAINT`, not
`DROP INDEX`. Using `DROP INDEX ... CASCADE` on a PostgreSQL unique constraint index raises an error.

The migration file itself documents this divergence:

> "CockroachDB requires DROP INDEX CASCADE for unique constraints.
> PostgreSQL requires ALTER TABLE DROP CONSTRAINT (handled by migration runner)."

**Required Change for PostgreSQL**:

```sql
ALTER TABLE public.platform_saga_definition
  DROP CONSTRAINT IF EXISTS uq_platform_saga_definition_name;
```

The PostgreSQL test helper already applies this equivalent. Any automated dual-database support
must account for this difference.

---

## High Severity Issues

### HIGH-1: `SERIAL` Primary Key Type

**File Affected**:

- `services/tenant/migrations/20251216000001_initial.sql`

**SQL Used**:

```sql
CREATE TABLE tenant_provisioning_status (
    id SERIAL PRIMARY KEY,
    ...
);
```

**Issue**: CockroachDB maps `SERIAL` to its own internal sequence that does not guarantee sequential
ordering (values are distributed across nodes). PostgreSQL's `SERIAL` is a true auto-incrementing
sequence. Both produce unique integer IDs, but applications relying on sequential ordering will
behave differently between the two databases.

**Recommendation**: `SERIAL` works on PostgreSQL, but for consistency with the rest of the codebase
(which uses `uuid DEFAULT gen_random_uuid()`), consider migrating to UUID:

```sql
id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
```

This is a compatibility note rather than a blocking issue — `SERIAL` works on both databases.

---

### HIGH-2: `CREATE TYPE ... AS ENUM` for `verification_status`

**File Affected**:

- `services/party/migrations/20251231000001_party_verification.sql`

**SQL Used**:

```sql
CREATE TYPE verification_status AS ENUM (
    'PENDING',
    'APPROVED',
    'REJECTED',
    'MANUAL_REVIEW'
);

CREATE TABLE "party_verification" (
    ...
    "status" verification_status NOT NULL DEFAULT 'PENDING',
    ...
);
```

**Issue**: CockroachDB supports `CREATE TYPE ... AS ENUM` but has stricter limitations on ENUM mutation
(e.g., `ALTER TYPE ... ADD VALUE` requires a schema change that may not be transactional).
PostgreSQL supports ENUMs fully but they are generally considered an anti-pattern due to migration
complexity.

The rest of the codebase uses `VARCHAR` with `CHECK` constraints instead of ENUMs — every other
status column across all 111 files follows this pattern. This is the **only file** using a custom
ENUM type.

**Risk**: Adding new enum values to `verification_status` requires `ALTER TYPE` on both databases,
which behaves differently (transactional on PostgreSQL, schema change on CockroachDB).

**Recommended Change for Consistency**:

```sql
-- Replace ENUM with VARCHAR + CHECK constraint (matches codebase pattern)
"status" VARCHAR(20) NOT NULL DEFAULT 'PENDING'
  CHECK (status IN ('PENDING', 'APPROVED', 'REJECTED', 'MANUAL_REVIEW')),
```

**Migration Path**: Requires a separate migration to:

1. Add `VARCHAR(20)` column
2. Backfill from ENUM column
3. Drop ENUM column and type
4. Rename new column

---

## Medium Severity Issues

### MED-1: `TEXT[]` Array Columns

**Files Affected**:

- `services/forecasting/migrations/20260210000001_initial.sql`
- `services/reference-data/migrations/20260206000001_valuation_methods_policies.sql`

**SQL Used**:

```sql
-- forecasting_strategy
input_dataset_codes TEXT[] NOT NULL,

-- valuation_method
required_policies text[] NOT NULL DEFAULT '{}',
```

**Issue**: PostgreSQL native arrays (`TEXT[]`) are supported in both CockroachDB and PostgreSQL.
However, CockroachDB's array support has limitations compared to PostgreSQL:

- Limited array operator support
- `ANY`/`ALL` array operators have restricted usage in CockroachDB
- `unnest()` works but with performance differences

Both databases support `TEXT[]` for storage. If the application only reads/writes arrays as whole
values (no `ANY`, `@>`, `&&` operators), this is fully compatible. If array operators are used in
queries, verify each operator is supported in both databases.

**Recommendation**: Review application query code to confirm no array-specific operators are used
beyond basic equality on the `input_dataset_codes` and `required_policies` columns.

---

### MED-2: `INET` Column Type — Inconsistency Across Services

**Summary**: `INET` is compatible with both databases, but its usage is inconsistent across services.

| Service          | `client_ip` Type in Audit Tables                        |
|------------------|---------------------------------------------------------|
| current-account  | `INET` (original; compatible)                           |
| financial-accounting | `INET`                                              |
| position-keeping | `INET`                                                  |
| tenant           | `INET`                                                  |
| party            | `VARCHAR(45)`                                           |
| payment-order    | `VARCHAR(45)` (initial), then `INET` after fix migration|

This is an internal inconsistency that would benefit from standardization, but is not a
compatibility blocker. Both `INET` and `VARCHAR(45)` work on both databases.

---

### MED-3: `public.` Schema References and Cross-Schema Foreign Keys

**Files Affected**:

- `services/reference-data/migrations/20260125000001_platform_saga_definition.sql`
- `services/reference-data/migrations/20260125000002_extend_saga_definition_platform_ref.sql`
- `services/reference-data/migrations/20260126000001_saga_fallback_resolution.sql`

**SQL Used**:

```sql
CREATE TABLE IF NOT EXISTS "public"."platform_saga_definition" (...);

ALTER TABLE "saga_definition"
  ADD CONSTRAINT "fk_saga_definition_platform_ref"
  FOREIGN KEY ("platform_ref") REFERENCES "public"."platform_saga_definition" ("id")
  ON DELETE SET NULL;
```

**Issue**: The `public` schema qualifier creates shared platform tables accessible across all tenant
schemas. This pattern works in both CockroachDB and PostgreSQL, but requires:

1. The `public` schema to exist (default in PostgreSQL, true in CockroachDB)
2. `search_path` configured so tenant schema queries can resolve `public.` references
3. Cross-schema foreign keys — supported in PostgreSQL, require careful `search_path` management

For PostgreSQL deployments, ensure:

- The database user has permissions on the `public` schema
- `search_path` includes `public` for all tenant connections
- Row-Level Security (if ever added) accounts for cross-schema FK resolution

---

## Low Severity Issues

### LOW-1: `CREATE OR REPLACE VIEW` with `ORDER BY`

**Files Affected**: Multiple audit system files across all services

**Issue**: `CREATE OR REPLACE VIEW` is supported identically in both databases. However, `ORDER BY`
in a view definition does not guarantee ordering when selecting from the view unless `ORDER BY` is
repeated in the outer query. This behavior is consistent between CockroachDB and PostgreSQL — it is
not a compatibility issue, but a documentation note.

---

### LOW-2: `DROP INDEX IF EXISTS` Scope Differences

**Files with unqualified DROP INDEX**:

- `services/party/migrations/20251223000002_optimize_association_index.sql`
- `services/reconciliation/migrations/20260213000001b_align_scheduler_execution_backfill.sql`

In CockroachDB, index names must be unique within a database. In PostgreSQL, index names are
schema-scoped. `DROP INDEX IF EXISTS "idx_name"` without schema qualification works identically in
both databases when within the same schema. No action needed unless schemas overlap.

---

## Informational: Well-Handled CockroachDB Patterns

The following patterns are correctly implemented for CockroachDB compatibility and are already
documented in migration comments. They would need different handling on PostgreSQL deployments.

### INFO-1: Partial Index Split Pattern (Intentional and Correct)

Many migrations split column addition and partial index creation into separate files because
CockroachDB requires columns to be "public" (committed in a prior transaction) before partial
indexes can reference them.

**Files demonstrating this pattern** (representative sample):

| Column Addition                                  | Index Creation                                 |
|--------------------------------------------------|------------------------------------------------|
| `20260123000001_bucket_aware_solvency.sql`       | `20260123000002_bucket_id_index.sql`           |
| `20260119000002_add_shared_dataset_support.sql`  | `20260119000003_add_shared_dataset_index.sql`  |
| `20260214000001_add_org_party_id.sql`            | `20260214000002_add_org_scoping_indexes.sql`   |
| `20260125000002_extend_saga_def_platform_ref.sql`| `20260125000003_platform_ref_index.sql`        |
| `20260106000001_add_successor_id.sql`            | `20260106000002_successor_id_index.sql`        |
| `20260211000001_account_type_definitions.sql`    | `20260211000002_account_type_active_index.sql` |
| `20260220000001_add_product_type_columns.sql`    | `20260220000002_add_product_type_index.sql`    |
| `20260221000001_add_party_attributes.sql`        | `20260221000002_add_party_attributes_index.sql`|
| `20260221000003_party_type_definition.sql`       | `20260221000004_party_type_def_index.sql`      |

**PostgreSQL Impact**: On PostgreSQL, these splits are unnecessary — a partial index on a
newly-added column can be created in the same transaction. Keeping them split is harmless:
the migrations will still run correctly on PostgreSQL.

---

### INFO-2: No `COMMENT ON INDEX` (Intentional)

`services/market-information/migrations/20260123000003_add_cursor_pagination_indexes.sql`
explicitly omits `COMMENT ON INDEX`:

> "COMMENT ON INDEX omitted because CockroachDB requires table-qualified syntax (table@index)
> which is not PostgreSQL-compatible."

This is the correct approach — omitting `COMMENT ON INDEX` entirely avoids the syntax divergence.

---

### INFO-3: No PL/pgSQL Triggers (Intentional)

Multiple files note that application-layer enforcement replaces database triggers because
CockroachDB does not support PL/pgSQL user-defined functions:

- `services/position-keeping/migrations/20260105000002_positions_append_only.sql`
- `services/reference-data/migrations/20260104000001_initial.sql`
- `services/market-information/migrations/20260116000001_initial.sql`
- `services/position-keeping/migrations/20260105000004_import_manifest.sql`
- `services/reference-data/migrations/20260211000001_account_type_definitions.sql`

**PostgreSQL Impact**: PostgreSQL supports PL/pgSQL triggers. For a PostgreSQL deployment, triggers
could optionally enforce constraints currently at the application layer (append-only, status
transitions, immutable fields). The existing application-layer enforcement works correctly on
PostgreSQL without modification.

---

### INFO-4: No `CREATE INDEX CONCURRENTLY` (Intentional)

No migration files use `CREATE INDEX CONCURRENTLY`. CockroachDB creates all indexes online by
default, making `CONCURRENTLY` unnecessary and potentially problematic with `atlas:txn false`.

**PostgreSQL Impact**: On PostgreSQL, large-table index creation blocks writes without `CONCURRENTLY`.
For production PostgreSQL deployments, consider adding `CONCURRENTLY` to index creation on
high-traffic tables if table sizes warrant it.

---

### INFO-5: DROP/RECREATE Pattern for Constraint Modification

Several services use a drop-then-recreate pattern to modify CHECK constraints, because CockroachDB
does not support modifying existing constraints in-place:

```sql
-- Example from reconciliation service (split across two migration files)
ALTER TABLE "settlement_run" DROP CONSTRAINT "chk_settlement_run_status";
ALTER TABLE "settlement_run" ADD CONSTRAINT "chk_settlement_run_status" CHECK (...);
```

**Files**:

- `services/reconciliation/migrations/20260209000003_settlement_finality.sql` / `...003b...`
- `services/reconciliation/migrations/20260209000005_pause_resume.sql` / `...005b...`
- `services/financial-accounting/migrations/20251219000001_fix_audit_schema_compatibility.sql`
- `services/payment-order/migrations/20251223000001_fix_audit_schema_compatibility.sql`
- `services/position-keeping/migrations/20251223000002_fix_audit_schema_compatibility.sql`

**PostgreSQL Impact**: This pattern works identically on PostgreSQL, which also requires
drop-then-add for constraint modification.

---

### INFO-6: `ALTER COLUMN TYPE` Avoidance

CockroachDB does not support `ALTER COLUMN TYPE` inside transactions for many type changes.
The add-new-column / backfill / drop-old-column pattern is used instead:

- `services/reconciliation/migrations/20260213000001_align_scheduler_execution.sql`:
  "CockroachDB does not support ALTER COLUMN TYPE inside transactions, so we add the new column
  here, backfill in the next migration, then drop the old one."

**PostgreSQL Impact**: PostgreSQL supports `ALTER COLUMN TYPE` with `USING` casts in most cases.
The three-step pattern used here is more verbose but fully compatible with PostgreSQL.

---

## Schema Inconsistencies (Cross-Service)

These are not compatibility issues but represent internal inconsistencies worth tracking.

### INCONS-1: `audit_log.old_values` / `new_values` Type

Some services use `JSONB`, others use `TEXT`:

| Service              | Type Used                                  |
|----------------------|--------------------------------------------|
| current-account      | `JSONB` (original, unchanged)              |
| tenant               | `JSONB`                                    |
| financial-accounting | `TEXT` (changed in fix migration)          |
| payment-order        | `TEXT` (changed in fix migration)          |
| position-keeping     | `TEXT` (changed in fix migration)          |
| party                | `TEXT`                                     |

The fix migrations document the reason: "the shared AuditOutbox may write empty strings for nil
values, which is invalid JSONB." This is an application-level constraint, not a database issue.

---

## Summary: Files Requiring Changes for PostgreSQL

| File | Service | Issue | Severity | Required Action |
|------|---------|-------|----------|-----------------|
| `20260128000002_versioned_platform_sagas_constraints.sql` | reference-data | `ADD CONSTRAINT IF NOT EXISTS` | Critical | Replace with PostgreSQL syntax |
| `20260129000002_bitemporal_platform_sagas_constraints.sql` | reference-data | `ADD CONSTRAINT IF NOT EXISTS` | Critical | Replace with PostgreSQL syntax |
| `20260127000001_fix_platform_saga_unique_constraint.sql` | reference-data | `DROP INDEX ... CASCADE` | Critical | Use `ALTER TABLE DROP CONSTRAINT` |
| `20251216000001_initial.sql` | tenant | `SERIAL` type | High | Functional on PostgreSQL; consider UUID |
| `20251231000001_party_verification.sql` | party | `CREATE TYPE ... AS ENUM` | High | Replace with VARCHAR + CHECK |
| `20260210000001_initial.sql` | forecasting | `TEXT[]` array column | Medium | Verify array operator compatibility |
| `20260206000001_valuation_methods_policies.sql` | reference-data | `text[]` array column | Medium | Verify array operator compatibility |
| `20260125000001_platform_saga_definition.sql` | reference-data | Cross-schema FK | Low | Document `search_path` requirements |
| `20260125000002_extend_saga_definition_platform_ref.sql` | reference-data | Cross-schema FK | Low | Document `search_path` requirements |

---

## Files With No Compatibility Issues

All remaining 102 migration files use SQL fully compatible with both CockroachDB and PostgreSQL:

- Standard `CREATE TABLE`, `ALTER TABLE`, `CREATE INDEX` statements
- `UUID` primary keys with `gen_random_uuid()` (PostgreSQL 13+ natively, no extension needed)
- `TIMESTAMPTZ`, `JSONB`, `BYTEA`, `TEXT`, `VARCHAR`, `BIGINT`, `INTEGER`, `DECIMAL`, `BOOLEAN`
- `CHECK` constraints using `IN (...)` and comparison operators
- Partial indexes using `WHERE` clauses
- `CREATE OR REPLACE VIEW` definitions
- Foreign keys with `ON DELETE RESTRICT`, `ON DELETE CASCADE`, `ON DELETE SET NULL`
- `GIN` indexes on JSONB columns
- `UNIQUE INDEX` creation

---

## Recommendations for Dual-Database Support

### Immediate Actions (Critical Issues)

1. **Create PostgreSQL-specific migration overrides** for the 3 critical files, or implement
   pre-processing in the migration runner that detects the database type and applies appropriate
   syntax transformations.

2. **`ADD CONSTRAINT IF NOT EXISTS`**: Add a migration runner hook that transforms this syntax
   to a `DO $$ ... END $$` block when running against PostgreSQL.

3. **`DROP INDEX ... CASCADE`**: Add a migration runner hook that transforms to
   `ALTER TABLE DROP CONSTRAINT` when running against PostgreSQL.
   (The file already notes this is done in the PostgreSQL test helper.)

### Short-Term Actions (High Severity)

1. **Standardize `verification_status`**: Replace the ENUM with `VARCHAR(20) + CHECK` to match
   the codebase convention. This eliminates a class of future migration complexity.

2. **`SERIAL` in tenant service**: Document that `SERIAL` is used intentionally for
   `tenant_provisioning_status.id`. Consider migrating to UUID in a future schema cleanup.

### Documentation Actions

1. **Add a `database-compatibility.md` guide** in `docs/development/` noting:
   - The 3 CockroachDB-specific syntax patterns and their PostgreSQL equivalents
   - The `public.` schema architecture for `platform_saga_definition` and `search_path` requirements
   - The split-migration pattern (harmless on PostgreSQL)
   - The no-trigger pattern and optional PostgreSQL trigger enhancements

2. **Atlas configuration**: When adding a PostgreSQL target in `atlas.hcl`, consider the `dev-url`
   for each environment and whether dialect-specific migration files should be maintained.

---

*Report generated by: 026-operations-console task 65 — Atlas Migration Compatibility Audit*\
*Methodology: Manual analysis of 111 SQL migration files across 11 services*
