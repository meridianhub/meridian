-- Add dunning retry tracking to billing_run table.
-- Supports dunning saga escalation with TTL-based retry scheduling.

-- Add last_retry_at column to track when the last dunning retry was scheduled.
-- NULL means no retry has been attempted yet.
ALTER TABLE "billing_run" ADD COLUMN "last_retry_at" timestamptz NULL;

-- Index for finding billing runs that need dunning retry.
-- Queries: "find FAILED runs with dunning_level < 4 ordered by last retry time"
CREATE INDEX "idx_billing_run_dunning" ON "billing_run" ("status", "dunning_level")
    WHERE status = 'FAILED' AND dunning_level < 4;

COMMENT ON COLUMN "billing_run"."last_retry_at" IS 'Timestamp of the last dunning retry attempt. NULL if no retry has been scheduled.';
