-- Align scheduler_execution schema with shared/platform/scheduler.PgExecutionStore.
-- The shared execution store expects: scheduler_name, schedule_id, completed_at, result_ref
-- The current schema has: schedule_name, run_id (UUID)

-- 1. Rename schedule_name -> scheduler_name
ALTER TABLE "scheduler_execution" RENAME COLUMN "schedule_name" TO "scheduler_name";

-- 2. Add schedule_id column (the shared store separates scheduler name from schedule ID)
ALTER TABLE "scheduler_execution" ADD COLUMN "schedule_id" VARCHAR(200);

-- 3. Add completed_at column
ALTER TABLE "scheduler_execution" ADD COLUMN "completed_at" TIMESTAMPTZ NULL;

-- 4. Rename run_id -> result_ref and change type from UUID to VARCHAR(200)
--    CockroachDB does not support ALTER COLUMN TYPE inside transactions,
--    so we add the new column, backfill, then drop the old one.
ALTER TABLE "scheduler_execution" ADD COLUMN "result_ref" VARCHAR(200) NULL;

UPDATE "scheduler_execution"
SET "result_ref" = "run_id"::TEXT
WHERE "run_id" IS NOT NULL;

ALTER TABLE "scheduler_execution" DROP COLUMN "run_id";

-- 5. Backfill: move existing scheduler_name values into schedule_id
--    (the old schedule_name was actually the schedule ID, not the scheduler name)
UPDATE "scheduler_execution"
SET "schedule_id" = "scheduler_name",
    "scheduler_name" = 'reconciliation'
WHERE "scheduler_name" IS NOT NULL AND "scheduler_name" != '';

-- Enforce NOT NULL after backfill (fails fast if any rows have empty schedule_id)
ALTER TABLE "scheduler_execution" ALTER COLUMN "schedule_id" SET NOT NULL;

-- 6. Drop old indexes that reference renamed columns and recreate
DROP INDEX IF EXISTS "idx_scheduler_execution_schedule_name";
DROP INDEX IF EXISTS "idx_scheduler_execution_name_at";

CREATE INDEX "idx_scheduler_execution_scheduler_name" ON "scheduler_execution" ("scheduler_name");
CREATE INDEX "idx_scheduler_execution_schedule_id" ON "scheduler_execution" ("schedule_id");
CREATE INDEX "idx_scheduler_execution_name_schedule_at"
  ON "scheduler_execution" ("scheduler_name", "schedule_id", "scheduled_at" DESC);
