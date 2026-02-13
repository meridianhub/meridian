-- Add dunning retry tracking to billing_run table.
-- Supports dunning saga escalation with Redis sorted-set retry scheduling.

-- Add last_retry_at column to track when the last dunning retry was scheduled.
-- NULL means no retry has been attempted yet.
ALTER TABLE "billing_run" ADD COLUMN "last_retry_at" timestamptz NULL;

-- NOTE: Partial index for dunning retry deferred to 20260211000002 because CockroachDB
-- cannot create a partial index referencing a column added in the same transaction.

COMMENT ON COLUMN "billing_run"."last_retry_at" IS 'Timestamp of the last dunning retry attempt. NULL if no retry has been scheduled.';
