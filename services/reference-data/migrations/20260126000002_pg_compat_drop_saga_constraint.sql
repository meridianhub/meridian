-- PostgreSQL compatibility: drop UNIQUE constraint before DROP INDEX in next migration.
--
-- Migration 20260127000001 uses DROP INDEX ... CASCADE to remove the old
-- uq_platform_saga_definition_name index. On CockroachDB this works because
-- DROP INDEX CASCADE also removes the backing constraint. On PostgreSQL,
-- DROP INDEX fails when the index backs a constraint - the constraint must
-- be dropped first with ALTER TABLE DROP CONSTRAINT.
--
-- This migration runs ALTER TABLE DROP CONSTRAINT IF EXISTS, which:
--   - PostgreSQL: drops the constraint, freeing the index for the next migration
--   - CockroachDB: drops the constraint (and its backing index) - the next
--     migration's DROP INDEX IF EXISTS becomes a safe no-op
--
-- On existing environments where 20260127000001 already ran successfully,
-- the constraint no longer exists, so DROP CONSTRAINT IF EXISTS is a no-op.

ALTER TABLE IF EXISTS "public"."platform_saga_definition"
  DROP CONSTRAINT IF EXISTS "uq_platform_saga_definition_name";
