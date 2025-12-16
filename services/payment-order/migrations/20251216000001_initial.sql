-- Payment Order Service Schema
-- Manages payment orders and saga orchestration for payment processing
-- Uses unqualified table names (relies on database-per-service architecture)

-- Create "payment_order" table (singular, unqualified)
CREATE TABLE "payment_order" (
  "id" uuid NOT NULL DEFAULT gen_random_uuid(),
  "debtor_account_id" character varying(255) NOT NULL,
  "creditor_reference" character varying(255) NOT NULL,
  "amount_cents" bigint NOT NULL CHECK (amount_cents > 0),
  "currency" character(3) NOT NULL,
  "status" character varying(20) NOT NULL CHECK (status IN ('INITIATED', 'RESERVED', 'EXECUTING', 'COMPLETED', 'FAILED', 'CANCELLED', 'REVERSED')),
  "lien_id" character varying(255) NULL,
  "gateway_reference_id" character varying(255) NULL,
  "ledger_booking_id" character varying(255) NULL,
  "correlation_id" character varying(255) NOT NULL,
  "causation_id" character varying(255) NULL,
  "idempotency_key" character varying(255) NOT NULL,
  "failure_reason" character varying(1000) NULL,
  "error_code" character varying(50) NULL,
  "version" integer NOT NULL DEFAULT 1,
  "created_at" timestamptz NOT NULL DEFAULT now(),
  "updated_at" timestamptz NOT NULL DEFAULT now(),
  "reserved_at" timestamptz NULL,
  "executing_at" timestamptz NULL,
  "completed_at" timestamptz NULL,
  "failed_at" timestamptz NULL,
  "cancelled_at" timestamptz NULL,
  "reversed_at" timestamptz NULL,
  -- Lien execution tracking fields for async retry mechanism
  "lien_execution_status" character varying(20) NULL CHECK (lien_execution_status IS NULL OR lien_execution_status IN ('PENDING', 'SUCCEEDED', 'FAILED')),
  "lien_execution_attempts" integer NOT NULL DEFAULT 0,
  "lien_execution_error" character varying(1000) NULL,
  PRIMARY KEY ("id")
);

-- Create indexes for payment_order
CREATE INDEX "idx_payment_order_debtor_account" ON "payment_order" ("debtor_account_id");
CREATE INDEX "idx_payment_order_status" ON "payment_order" ("status");
CREATE UNIQUE INDEX "idx_payment_order_idempotency_key" ON "payment_order" ("idempotency_key");
CREATE INDEX "idx_payment_order_gateway_ref" ON "payment_order" ("gateway_reference_id");

-- Index for finding payment orders that need lien execution retry
-- This supports the reconciliation query: COMPLETED orders with pending/failed lien execution
CREATE INDEX "idx_payment_order_lien_execution" ON "payment_order" ("status", "lien_execution_status")
WHERE status = 'COMPLETED' AND lien_id IS NOT NULL AND (lien_execution_status = 'PENDING' OR lien_execution_status = 'FAILED');

-- Comments for lien execution columns
COMMENT ON COLUMN "payment_order"."lien_execution_status" IS 'Status of lien execution: PENDING (in-progress), SUCCEEDED, FAILED. NULL for non-completed orders.';
COMMENT ON COLUMN "payment_order"."lien_execution_attempts" IS 'Number of times ExecuteLien has been attempted. Used for retry exhaustion monitoring.';
COMMENT ON COLUMN "payment_order"."lien_execution_error" IS 'Last error message from failed ExecuteLien attempt. Only set when status is FAILED.';
