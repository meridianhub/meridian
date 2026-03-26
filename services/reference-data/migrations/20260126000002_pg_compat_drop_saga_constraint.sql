-- PostgreSQL compatibility: drop UNIQUE constraint before DROP INDEX in next migration.
--
-- Migration 20260127000001 uses DROP INDEX ... CASCADE to remove the old
-- uq_platform_saga_definition_name index. On CockroachDB this works because
-- DROP INDEX CASCADE also removes the backing constraint. On PostgreSQL,
-- DROP INDEX fails when the index backs a constraint - the constraint must
-- be dropped first with ALTER TABLE DROP CONSTRAINT.
--
-- CockroachDB does not support ALTER TABLE DROP CONSTRAINT for UNIQUE constraints
-- (requires DROP INDEX CASCADE instead). We use a PL/pgSQL block with exception
-- handling so the statement is a no-op on CockroachDB - the error is caught and
-- ignored, and the next migration's DROP INDEX CASCADE handles it.
--
-- On existing environments where 20260127000001 already ran successfully,
-- the constraint no longer exists, so this is a no-op on both databases.

DO $$ BEGIN
  ALTER TABLE IF EXISTS "public"."platform_saga_definition"
    DROP CONSTRAINT IF EXISTS "uq_platform_saga_definition_name";
EXCEPTION
  WHEN SQLSTATE '0A000' THEN
    -- CockroachDB v24.1: "unimplemented: cannot drop UNIQUE constraint using ALTER TABLE"
    -- Safe to ignore - the next migration (20260127000001) handles it via DROP INDEX CASCADE.
    NULL;
  WHEN OTHERS THEN
    RAISE;
END $$;
