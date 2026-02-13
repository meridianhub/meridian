-- Deferred index creation for last_retry_at column added in 20260211000001.
-- CockroachDB requires columns to be "public" (committed in a prior
-- transaction) before a partial index can reference them.

-- Index for finding billing runs that need dunning retry.
-- Queries: "find FAILED runs with dunning_level < 4 ordered by last retry time"
CREATE INDEX "idx_billing_run_dunning" ON "billing_run" ("status", "dunning_level", "last_retry_at")
    WHERE status = 'FAILED' AND dunning_level < 4;
