-- Align scheduler_execution schema with shared/platform/scheduler.PgExecutionStore.
-- The shared execution store expects: scheduler_name, schedule_id, completed_at, result_ref
-- The current schema has: schedule_name, run_id (UUID)
--
-- DDL only (schema changes). DML backfill and constraints are in
-- 20260213000001b_align_scheduler_execution_backfill.sql because
-- CockroachDB requires new columns to be committed before DML can
-- reference them.

-- 1. Rename schedule_name -> scheduler_name
ALTER TABLE "scheduler_execution" RENAME COLUMN "schedule_name" TO "scheduler_name";

-- 2. Add schedule_id column (the shared store separates scheduler name from schedule ID)
ALTER TABLE "scheduler_execution" ADD COLUMN "schedule_id" VARCHAR(200);

-- 3. Add completed_at column
ALTER TABLE "scheduler_execution" ADD COLUMN "completed_at" TIMESTAMPTZ NULL;

-- 4. Add result_ref column (replaces run_id UUID with VARCHAR)
--    CockroachDB does not support ALTER COLUMN TYPE inside transactions,
--    so we add the new column here, backfill in the next migration, then drop the old one.
ALTER TABLE "scheduler_execution" ADD COLUMN "result_ref" VARCHAR(200) NULL;
