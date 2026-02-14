-- Scheduler execution audit trail for the shared scheduler package.
-- Records each cron job execution with status and timing for observability.

CREATE TABLE "scheduler_execution" (
    "id" uuid NOT NULL DEFAULT gen_random_uuid(),
    "scheduler_name" character varying(100) NOT NULL,
    "schedule_id" character varying(200) NOT NULL,
    "scheduled_at" timestamptz NOT NULL,
    "executed_at" timestamptz NULL,
    "completed_at" timestamptz NULL,
    "status" character varying(20) NOT NULL DEFAULT 'TRIGGERED',
    "result_ref" character varying(200) NULL,
    "error_message" text NULL,
    "created_at" timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY ("id")
);

ALTER TABLE "scheduler_execution"
    ADD CONSTRAINT "chk_scheduler_execution_status"
    CHECK ("status" IN ('TRIGGERED', 'COMPLETED', 'FAILED', 'MISSED', 'SKIPPED'));

CREATE INDEX "idx_scheduler_execution_name_schedule" ON "scheduler_execution" ("scheduler_name", "schedule_id");
CREATE INDEX "idx_scheduler_execution_scheduled_at" ON "scheduler_execution" ("scheduled_at" DESC);
CREATE INDEX "idx_scheduler_execution_status" ON "scheduler_execution" ("status");
CREATE INDEX "idx_scheduler_execution_name_at" ON "scheduler_execution" ("scheduler_name", "scheduled_at" DESC);
