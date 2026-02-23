-- PostgreSQL pre-migration script for demo deployment
--
-- Run this script ONCE against each service database BEFORE running Atlas migrations.
-- It resolves the one CockroachDB-specific statement in the migration corpus that
-- cannot be made compatible without breaking the existing CockroachDB CI path.
--
-- Applies to: services/reference-data database only
-- Reason:     20260127000001_fix_platform_saga_unique_constraint.sql uses
--             DROP INDEX CASCADE to drop a constraint-backed unique index.
--             CockroachDB requires this syntax; PostgreSQL requires ALTER TABLE
--             DROP CONSTRAINT instead.
--
-- If the constraint has already been dropped (e.g., fresh database), this is a no-op.

-- reference-data: drop constraint-backed unique index before Atlas migration run
ALTER TABLE public.platform_saga_definition
  DROP CONSTRAINT IF EXISTS uq_platform_saga_definition_name;
