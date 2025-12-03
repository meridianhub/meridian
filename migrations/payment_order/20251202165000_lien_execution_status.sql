-- Add lien execution tracking fields for async retry mechanism
-- These fields track the status of ExecuteLien calls after payment completion

-- Add lien_execution_status column with CHECK constraint
ALTER TABLE "payment_order"."payment_orders"
ADD COLUMN "lien_execution_status" character varying(20) NULL
CHECK (lien_execution_status IS NULL OR lien_execution_status IN ('PENDING', 'SUCCEEDED', 'FAILED'));

-- Add lien_execution_attempts to track retry count
ALTER TABLE "payment_order"."payment_orders"
ADD COLUMN "lien_execution_attempts" integer NOT NULL DEFAULT 0;

-- Add lien_execution_error to store last error message
ALTER TABLE "payment_order"."payment_orders"
ADD COLUMN "lien_execution_error" character varying(1000) NULL;

-- Create index for finding payment orders that need lien execution retry
-- This supports the reconciliation query: COMPLETED orders with pending/failed lien execution
CREATE INDEX "idx_payment_orders_lien_execution" ON "payment_order"."payment_orders" ("status", "lien_execution_status")
WHERE status = 'COMPLETED' AND lien_id IS NOT NULL AND (lien_execution_status = 'PENDING' OR lien_execution_status = 'FAILED');

-- Comment on new columns for documentation
COMMENT ON COLUMN "payment_order"."payment_orders"."lien_execution_status" IS 'Status of lien execution: PENDING (in-progress), SUCCEEDED, FAILED. NULL for non-completed orders.';
COMMENT ON COLUMN "payment_order"."payment_orders"."lien_execution_attempts" IS 'Number of times ExecuteLien has been attempted. Used for retry exhaustion monitoring.';
COMMENT ON COLUMN "payment_order"."payment_orders"."lien_execution_error" IS 'Last error message from failed ExecuteLien attempt. Only set when status is FAILED.';
