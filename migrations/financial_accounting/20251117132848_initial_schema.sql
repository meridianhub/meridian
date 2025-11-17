-- Drop schema named "public"
DROP SCHEMA "public" CASCADE;
-- Add new schema named "financial_accounting"
CREATE SCHEMA "financial_accounting";
-- Create "financial_booking_logs" table
CREATE TABLE "financial_accounting"."financial_booking_logs" (
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
-- Create index "idx_financial_accounting_financial_booking_logs_base_currency" to table: "financial_booking_logs"
CREATE INDEX "idx_financial_accounting_financial_booking_logs_base_currency" ON "financial_accounting"."financial_booking_logs" ("base_currency");
-- Create index "idx_financial_accounting_financial_booking_logs_businescafac217" to table: "financial_booking_logs"
CREATE INDEX "idx_financial_accounting_financial_booking_logs_businescafac217" ON "financial_accounting"."financial_booking_logs" ("business_unit_reference");
-- Create index "idx_financial_accounting_financial_booking_logs_deleted_at" to table: "financial_booking_logs"
CREATE INDEX "idx_financial_accounting_financial_booking_logs_deleted_at" ON "financial_accounting"."financial_booking_logs" ("deleted_at");
-- Create index "idx_financial_accounting_financial_booking_logs_financi935e59da" to table: "financial_booking_logs"
CREATE INDEX "idx_financial_accounting_financial_booking_logs_financi935e59da" ON "financial_accounting"."financial_booking_logs" ("financial_account_type");
-- Create index "idx_financial_accounting_financial_booking_logs_idempotency_key" to table: "financial_booking_logs"
CREATE UNIQUE INDEX "idx_financial_accounting_financial_booking_logs_idempotency_key" ON "financial_accounting"."financial_booking_logs" ("idempotency_key");
-- Create index "idx_financial_accounting_financial_booking_logs_product8d9c65fb" to table: "financial_booking_logs"
CREATE INDEX "idx_financial_accounting_financial_booking_logs_product8d9c65fb" ON "financial_accounting"."financial_booking_logs" ("product_service_reference");
-- Create index "idx_financial_accounting_financial_booking_logs_status" to table: "financial_booking_logs"
CREATE INDEX "idx_financial_accounting_financial_booking_logs_status" ON "financial_accounting"."financial_booking_logs" ("status");
-- Create "ledger_postings" table
CREATE TABLE "financial_accounting"."ledger_postings" (
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
-- Create index "idx_booking_log" to table: "ledger_postings"
CREATE INDEX "idx_booking_log" ON "financial_accounting"."ledger_postings" ("financial_booking_log_id");
-- Create index "idx_financial_accounting_ledger_postings_account_id" to table: "ledger_postings"
CREATE INDEX "idx_financial_accounting_ledger_postings_account_id" ON "financial_accounting"."ledger_postings" ("account_id");
-- Create index "idx_financial_accounting_ledger_postings_correlation_id" to table: "ledger_postings"
CREATE INDEX "idx_financial_accounting_ledger_postings_correlation_id" ON "financial_accounting"."ledger_postings" ("correlation_id");
-- Create index "idx_financial_accounting_ledger_postings_currency" to table: "ledger_postings"
CREATE INDEX "idx_financial_accounting_ledger_postings_currency" ON "financial_accounting"."ledger_postings" ("currency");
-- Create index "idx_financial_accounting_ledger_postings_deleted_at" to table: "ledger_postings"
CREATE INDEX "idx_financial_accounting_ledger_postings_deleted_at" ON "financial_accounting"."ledger_postings" ("deleted_at");
-- Create index "idx_financial_accounting_ledger_postings_status" to table: "ledger_postings"
CREATE INDEX "idx_financial_accounting_ledger_postings_status" ON "financial_accounting"."ledger_postings" ("status");
-- Create index "idx_financial_accounting_ledger_postings_value_date" to table: "ledger_postings"
CREATE INDEX "idx_financial_accounting_ledger_postings_value_date" ON "financial_accounting"."ledger_postings" ("value_date");
