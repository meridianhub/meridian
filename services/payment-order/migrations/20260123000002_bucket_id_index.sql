-- Deferred index creation for bucket_id column added in 20260123000001.
-- CockroachDB requires the column to be "public" (committed in a prior
-- transaction) before a partial index can reference it.

-- Index for querying by bucket_id (useful for analytics and debugging)
CREATE INDEX "idx_payment_order_bucket_id" ON "payment_order" ("bucket_id")
WHERE bucket_id IS NOT NULL;
