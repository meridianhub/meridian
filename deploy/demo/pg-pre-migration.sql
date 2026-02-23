-- PostgreSQL pre-migration script for demo deployment
--
-- Run this script ONCE against the reference-data service database BEFORE
-- running Atlas migrations. It resolves the one CockroachDB-specific statement
-- in the migration corpus that cannot be made compatible without breaking the
-- existing CockroachDB CI path.
--
-- Applies to: services/reference-data database (meridian_reference_data) ONLY.
-- Do NOT run against other service databases: the platform_saga_definition
-- table does not exist in other services, and this script would raise an error.
--
-- Reason: 20260127000001_fix_platform_saga_unique_constraint.sql uses
--   DROP INDEX CASCADE to drop a constraint-backed unique index.
--   CockroachDB requires this syntax; PostgreSQL requires ALTER TABLE
--   DROP CONSTRAINT instead.
--
-- Safety: ALTER TABLE IF EXISTS makes this safe to run at any migration state:
--   - Table not yet created (fresh DB): no-op
--   - Table exists, constraint absent (already dropped): DROP CONSTRAINT IF EXISTS is a no-op
--   - Table exists, constraint present: drops the constraint

-- reference-data: drop constraint-backed unique index before Atlas migration run
ALTER TABLE IF EXISTS public.platform_saga_definition
  DROP CONSTRAINT IF EXISTS uq_platform_saga_definition_name;
