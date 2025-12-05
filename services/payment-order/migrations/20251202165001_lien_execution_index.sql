-- Create index for finding payment orders that need lien execution retry
-- This supports the reconciliation query: COMPLETED orders with pending/failed lien execution
-- Note: Separate migration required for CockroachDB - index cannot be created on column in same transaction as ADD COLUMN
CREATE INDEX "idx_payment_orders_lien_execution" ON "payment_order"."payment_orders" ("status", "lien_execution_status")
WHERE status = 'COMPLETED' AND lien_id IS NOT NULL AND (lien_execution_status = 'PENDING' OR lien_execution_status = 'FAILED');
