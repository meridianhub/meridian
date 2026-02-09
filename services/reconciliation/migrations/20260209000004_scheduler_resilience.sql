-- Scheduler resilience: execution audit trail and duplicate run prevention.

-- 1. Scheduler execution audit trail
-- Records every cron tick so operators can verify scheduler health,
-- detect missed windows, and support catch-up on startup.
CREATE TABLE "scheduler_execution" (
  "id" uuid NOT NULL DEFAULT gen_random_uuid(),
  "schedule_name" character varying(200) NOT NULL,
  "scheduled_at" timestamptz NOT NULL,
  "executed_at" timestamptz NULL,
  "status" character varying(20) NOT NULL DEFAULT 'TRIGGERED',
  "run_id" uuid NULL,
  "error_message" text NULL,
  "created_at" timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY ("id")
);

ALTER TABLE "scheduler_execution"
  ADD CONSTRAINT "chk_scheduler_execution_status"
  CHECK ("status" IN ('TRIGGERED', 'COMPLETED', 'FAILED', 'MISSED', 'SKIPPED'));

CREATE INDEX "idx_scheduler_execution_schedule_name" ON "scheduler_execution" ("schedule_name");
CREATE INDEX "idx_scheduler_execution_scheduled_at" ON "scheduler_execution" ("scheduled_at" DESC);
CREATE INDEX "idx_scheduler_execution_status" ON "scheduler_execution" ("status");

-- Composite index for catch-up queries: find last execution per schedule
CREATE INDEX "idx_scheduler_execution_name_at"
  ON "scheduler_execution" ("schedule_name", "scheduled_at" DESC);

-- 2. Unique constraint on settlement_run to prevent duplicate runs
-- Defense in depth beyond application-level checks.
--
-- Clean up any pre-existing duplicate (account_id, period_start, period_end) rows
-- before creating the unique index. Keep the earliest row per combination,
-- determined by created_at with id as deterministic tie-break (UUIDs have no
-- chronological ordering so MIN(id) would be incorrect).
-- This is a forward-only migration: if rollback is needed, drop the index:
--   DROP INDEX IF EXISTS "idx_settlement_run_account_period";
-- Deleted rows can be recovered from database backups if necessary.
WITH ranked AS (
  SELECT
    "id",
    ROW_NUMBER() OVER (
      PARTITION BY "account_id", "period_start", "period_end"
      ORDER BY "created_at" ASC, "id" ASC
    ) AS rn
  FROM "settlement_run"
)
DELETE FROM "settlement_run"
WHERE "id" IN (SELECT "id" FROM ranked WHERE rn > 1);

CREATE UNIQUE INDEX "idx_settlement_run_account_period"
  ON "settlement_run" ("account_id", "period_start", "period_end");
