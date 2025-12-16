-- Financial Accounting Service Schema
-- Uses unqualified table names (relies on database-per-service architecture)

-- Create "financial_booking_log" table (singular, unqualified)
CREATE TABLE "financial_booking_log" (
  "id" uuid NOT NULL,
  "financial_account_type" character varying(50) NOT NULL,
  "product_service_reference" character varying(255) NOT NULL,
  "business_unit_reference" character varying(255) NOT NULL,
  "chart_of_accounts_rules" text NOT NULL,
  "base_currency" character varying(3) NOT NULL,
  "status" character varying(50) NOT NULL,
  "idempotency_key" character varying(255) NOT NULL,
  "created_at" timestamptz NOT NULL,
  "updated_at" timestamptz NOT NULL,
  "created_by" character varying(255) NULL,
  "updated_by" character varying(255) NULL,
  "deleted_at" timestamptz NULL,
  "version" bigint NOT NULL,
  PRIMARY KEY ("id")
);
-- Create indexes for financial_booking_log
CREATE INDEX "idx_financial_booking_log_base_currency" ON "financial_booking_log" ("base_currency");
CREATE INDEX "idx_financial_booking_log_business_unit_reference" ON "financial_booking_log" ("business_unit_reference");
CREATE INDEX "idx_financial_booking_log_deleted_at" ON "financial_booking_log" ("deleted_at");
CREATE INDEX "idx_financial_booking_log_financial_account_type" ON "financial_booking_log" ("financial_account_type");
CREATE UNIQUE INDEX "idx_financial_booking_log_idempotency_key" ON "financial_booking_log" ("idempotency_key");
CREATE INDEX "idx_financial_booking_log_product_service_reference" ON "financial_booking_log" ("product_service_reference");
CREATE INDEX "idx_financial_booking_log_status" ON "financial_booking_log" ("status");

-- Create "ledger_posting" table (singular, unqualified)
CREATE TABLE "ledger_posting" (
  "id" uuid NOT NULL,
  "financial_booking_log_id" uuid NOT NULL,
  "posting_direction" character varying(10) NOT NULL,
  "amount_cents" bigint NOT NULL,
  "currency" character varying(3) NOT NULL,
  "account_id" character varying(255) NOT NULL,
  "value_date" timestamptz NOT NULL,
  "posting_result" character varying(1000) NULL,
  "status" character varying(50) NOT NULL,
  "correlation_id" character varying(255) NULL,
  "created_at" timestamptz NOT NULL,
  "updated_at" timestamptz NOT NULL,
  "created_by" character varying(255) NULL,
  "updated_by" character varying(255) NULL,
  "deleted_at" timestamptz NULL,
  PRIMARY KEY ("id")
);
-- Create indexes for ledger_posting
CREATE INDEX "idx_ledger_posting_booking_log_id" ON "ledger_posting" ("financial_booking_log_id");
CREATE INDEX "idx_ledger_posting_account_id" ON "ledger_posting" ("account_id");
CREATE INDEX "idx_ledger_posting_correlation_id" ON "ledger_posting" ("correlation_id");
CREATE INDEX "idx_ledger_posting_currency" ON "ledger_posting" ("currency");
CREATE INDEX "idx_ledger_posting_deleted_at" ON "ledger_posting" ("deleted_at");
CREATE INDEX "idx_ledger_posting_status" ON "ledger_posting" ("status");
CREATE INDEX "idx_ledger_posting_value_date" ON "ledger_posting" ("value_date");

-- Add foreign key constraint from ledger_posting to financial_booking_log
ALTER TABLE "ledger_posting"
  ADD CONSTRAINT "fk_ledger_posting_booking_log"
  FOREIGN KEY ("financial_booking_log_id")
  REFERENCES "financial_booking_log" ("id")
  ON DELETE RESTRICT;
