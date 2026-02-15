-- Backfill and finalize scheduler_execution schema alignment.
-- Split from 20260213000001 for CockroachDB compatibility: new columns
-- must be committed (public) before DML can reference them.

-- 1. Backfill result_ref from run_id, then drop run_id
UPDATE "scheduler_execution"
SET "result_ref" = "run_id"::TEXT
WHERE "run_id" IS NOT NULL;

ALTER TABLE "scheduler_execution" DROP COLUMN "run_id";

-- 2. Backfill: move existing scheduler_name values into schedule_id
--    (the old schedule_name was actually the schedule ID, not the scheduler name)
UPDATE "scheduler_execution"
SET "schedule_id" = "scheduler_name",
    "scheduler_name" = 'reconciliation'
WHERE "scheduler_name" IS NOT NULL AND "scheduler_name" != '';

-- Enforce NOT NULL after backfill (fails fast if any rows have empty schedule_id)
ALTER TABLE "scheduler_execution" ALTER COLUMN "schedule_id" SET NOT NULL;

-- 3. Drop old indexes that reference renamed columns and recreate
DROP INDEX IF EXISTS "idx_scheduler_execution_schedule_name";
DROP INDEX IF EXISTS "idx_scheduler_execution_name_at";

CREATE INDEX "idx_scheduler_execution_scheduler_name" ON "scheduler_execution" ("scheduler_name");
CREATE INDEX "idx_scheduler_execution_schedule_id" ON "scheduler_execution" ("schedule_id");
CREATE INDEX "idx_scheduler_execution_name_schedule_at"
  ON "scheduler_execution" ("scheduler_name", "schedule_id", "scheduled_at" DESC);
