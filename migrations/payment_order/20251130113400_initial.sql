-- Add new schema named "payment_order"
CREATE SCHEMA IF NOT EXISTS "payment_order";

-- Create "payment_orders" table
CREATE TABLE "payment_order"."payment_orders" (
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
  PRIMARY KEY ("id")
);

-- Create index on debtor_account_id for queries by account
CREATE INDEX "idx_payment_orders_debtor_account" ON "payment_order"."payment_orders" ("debtor_account_id");

-- Create index on status for filtering by lifecycle state
CREATE INDEX "idx_payment_orders_status" ON "payment_order"."payment_orders" ("status");

-- Create unique index on idempotency_key to prevent duplicate payment orders
CREATE UNIQUE INDEX "idx_payment_orders_idempotency_key" ON "payment_order"."payment_orders" ("idempotency_key");

-- Create index on gateway_reference_id for webhook lookups
CREATE INDEX "idx_payment_orders_gateway_ref" ON "payment_order"."payment_orders" ("gateway_reference_id");
