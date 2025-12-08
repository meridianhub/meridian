-- Position-keeping schema is independent per BIAN domain (ADR-002) - no cross-schema dependencies
-- Add new schema named "position_keeping"
CREATE SCHEMA IF NOT EXISTS "position_keeping";
-- Create "financial_position_logs" table
CREATE TABLE "position_keeping"."financial_position_logs" (
  "id" uuid NOT NULL DEFAULT gen_random_uuid(),
  "created_at" timestamptz NOT NULL DEFAULT now(),
  "created_by" character varying(100) NOT NULL,
  "updated_at" timestamptz NOT NULL DEFAULT now(),
  "updated_by" character varying(100) NOT NULL,
  "deleted_at" timestamptz NULL,
  "log_id" uuid NOT NULL,
  "account_id" character varying(34) NOT NULL,
  "version" bigint NOT NULL DEFAULT 1,
  "current_status" character varying(20) NOT NULL,
  "previous_status" character varying(20) NULL,
  "status_updated_at" timestamptz NOT NULL,
  "status_reason" text NOT NULL,
  "failure_reason" text NULL,
  "reconciliation_status" character varying(20) NOT NULL,
  PRIMARY KEY ("id")
  -- Note: No FK to current_account schema - services are independent per BIAN domain (ADR-002)
  -- Account validation is done at the application level via gRPC
);
-- Create index "idx_position_keeping_financial_position_logs_account_id" to table: "financial_position_logs"
CREATE INDEX "idx_position_keeping_financial_position_logs_account_id" ON "position_keeping"."financial_position_logs" ("account_id");
-- Create index "idx_position_keeping_financial_position_logs_current_status" to table: "financial_position_logs"
CREATE INDEX "idx_position_keeping_financial_position_logs_current_status" ON "position_keeping"."financial_position_logs" ("current_status");
-- Create index "idx_position_keeping_financial_position_logs_deleted_at" to table: "financial_position_logs"
CREATE INDEX "idx_position_keeping_financial_position_logs_deleted_at" ON "position_keeping"."financial_position_logs" ("deleted_at");
-- Create index "idx_position_keeping_financial_position_logs_log_id" to table: "financial_position_logs"
CREATE UNIQUE INDEX "idx_position_keeping_financial_position_logs_log_id" ON "position_keeping"."financial_position_logs" ("log_id");
-- Create "audit_trail_entries" table
CREATE TABLE "position_keeping"."audit_trail_entries" (
  "id" uuid NOT NULL DEFAULT gen_random_uuid(),
  "created_at" timestamptz NOT NULL DEFAULT now(),
  "created_by" character varying(100) NOT NULL,
  "updated_at" timestamptz NOT NULL DEFAULT now(),
  "updated_by" character varying(100) NOT NULL,
  "deleted_at" timestamptz NULL,
  "audit_id" uuid NOT NULL,
  "financial_position_log_id" uuid NOT NULL,
  "timestamp" timestamptz NOT NULL,
  "user_id" character varying(100) NOT NULL,
  "action" character varying(100) NOT NULL,
  "details" text NULL,
  "ip_address" character varying(45) NULL,
  "system_context" jsonb NULL,
  PRIMARY KEY ("id"),
  CONSTRAINT "fk_position_keeping_financial_position_logs_audit_trail_entries" FOREIGN KEY ("financial_position_log_id") REFERENCES "position_keeping"."financial_position_logs" ("id") ON UPDATE NO ACTION ON DELETE CASCADE
);
-- Create index "idx_position_keeping_audit_trail_entries_audit_id" to table: "audit_trail_entries"
CREATE UNIQUE INDEX "idx_position_keeping_audit_trail_entries_audit_id" ON "position_keeping"."audit_trail_entries" ("audit_id");
-- Create index "idx_position_keeping_audit_trail_entries_deleted_at" to table: "audit_trail_entries"
CREATE INDEX "idx_position_keeping_audit_trail_entries_deleted_at" ON "position_keeping"."audit_trail_entries" ("deleted_at");
-- Create index "idx_position_keeping_audit_trail_entries_log_id" to table: "audit_trail_entries"
CREATE INDEX "idx_position_keeping_audit_trail_entries_log_id" ON "position_keeping"."audit_trail_entries" ("financial_position_log_id");
-- Create index "idx_position_keeping_audit_trail_entries_timestamp" to table: "audit_trail_entries"
CREATE INDEX "idx_position_keeping_audit_trail_entries_timestamp" ON "position_keeping"."audit_trail_entries" ("timestamp");
-- Create index "idx_position_keeping_audit_trail_entries_user_id" to table: "audit_trail_entries"
CREATE INDEX "idx_position_keeping_audit_trail_entries_user_id" ON "position_keeping"."audit_trail_entries" ("user_id");
-- Create "transaction_lineages" table
CREATE TABLE "position_keeping"."transaction_lineages" (
  "id" uuid NOT NULL DEFAULT gen_random_uuid(),
  "created_at" timestamptz NOT NULL DEFAULT now(),
  "created_by" character varying(100) NOT NULL,
  "updated_at" timestamptz NOT NULL DEFAULT now(),
  "updated_by" character varying(100) NOT NULL,
  "deleted_at" timestamptz NULL,
  "financial_position_log_id" uuid NOT NULL,
  "transaction_id" uuid NOT NULL,
  "parent_transaction_id" uuid NULL,
  "child_transaction_ids" jsonb NOT NULL DEFAULT '[]',
  "related_transaction_ids" jsonb NOT NULL DEFAULT '[]',
  "transaction_type" character varying(50) NOT NULL,
  PRIMARY KEY ("id"),
  CONSTRAINT "fk_position_keeping_financial_position_logs_transaction_lineage" FOREIGN KEY ("financial_position_log_id") REFERENCES "position_keeping"."financial_position_logs" ("id") ON UPDATE NO ACTION ON DELETE CASCADE
);
-- Create index "idx_position_keeping_transaction_lineages_deleted_at" to table: "transaction_lineages"
CREATE INDEX "idx_position_keeping_transaction_lineages_deleted_at" ON "position_keeping"."transaction_lineages" ("deleted_at");
-- Create index "idx_position_keeping_transaction_lineages_log_id" to table: "transaction_lineages"
CREATE UNIQUE INDEX "idx_position_keeping_transaction_lineages_log_id" ON "position_keeping"."transaction_lineages" ("financial_position_log_id");
-- Create index "idx_position_keeping_transaction_lineages_parent_id" to table: "transaction_lineages"
CREATE INDEX "idx_position_keeping_transaction_lineages_parent_id" ON "position_keeping"."transaction_lineages" ("parent_transaction_id");
-- Create index "idx_position_keeping_transaction_lineages_transaction_id" to table: "transaction_lineages"
CREATE INDEX "idx_position_keeping_transaction_lineages_transaction_id" ON "position_keeping"."transaction_lineages" ("transaction_id");
-- Create "transaction_log_entries" table
CREATE TABLE "position_keeping"."transaction_log_entries" (
  "id" uuid NOT NULL DEFAULT gen_random_uuid(),
  "created_at" timestamptz NOT NULL DEFAULT now(),
  "created_by" character varying(100) NOT NULL,
  "updated_at" timestamptz NOT NULL DEFAULT now(),
  "updated_by" character varying(100) NOT NULL,
  "deleted_at" timestamptz NULL,
  "entry_id" uuid NOT NULL,
  "financial_position_log_id" uuid NOT NULL,
  "transaction_id" uuid NOT NULL,
  "account_id" character varying(34) NOT NULL,
  "amount_cents" bigint NOT NULL,
  "currency" character(3) NOT NULL DEFAULT 'GBP',
  "direction" character varying(10) NOT NULL,
  "timestamp" timestamptz NOT NULL,
  "description" text NULL,
  "reference" character varying(100) NULL,
  "source" character varying(50) NOT NULL,
  PRIMARY KEY ("id"),
  CONSTRAINT "fk_position_keeping_financial_position_logs_transaction_adbf542" FOREIGN KEY ("financial_position_log_id") REFERENCES "position_keeping"."financial_position_logs" ("id") ON UPDATE NO ACTION ON DELETE CASCADE
  -- Note: No FK to current_account schema - services are independent per BIAN domain (ADR-002)
);
-- Create index "idx_position_keeping_transaction_log_entries_deleted_at" to table: "transaction_log_entries"
CREATE INDEX "idx_position_keeping_transaction_log_entries_deleted_at" ON "position_keeping"."transaction_log_entries" ("deleted_at");
-- Create index "idx_position_keeping_transaction_log_entries_entry_id" to table: "transaction_log_entries"
CREATE UNIQUE INDEX "idx_position_keeping_transaction_log_entries_entry_id" ON "position_keeping"."transaction_log_entries" ("entry_id");
-- Create index "idx_position_keeping_transaction_log_entries_log_id" to table: "transaction_log_entries"
CREATE INDEX "idx_position_keeping_transaction_log_entries_log_id" ON "position_keeping"."transaction_log_entries" ("financial_position_log_id");
-- Create index "idx_position_keeping_transaction_log_entries_timestamp" to table: "transaction_log_entries"
CREATE INDEX "idx_position_keeping_transaction_log_entries_timestamp" ON "position_keeping"."transaction_log_entries" ("timestamp");
-- Create index "idx_position_keeping_transaction_log_entries_transaction_id" to table: "transaction_log_entries"
CREATE INDEX "idx_position_keeping_transaction_log_entries_transaction_id" ON "position_keeping"."transaction_log_entries" ("transaction_id");
-- Add validation constraints
ALTER TABLE "position_keeping"."transaction_log_entries"
  ADD CONSTRAINT "chk_transaction_log_entries_currency"
  CHECK (char_length("currency") = 3);
ALTER TABLE "position_keeping"."transaction_log_entries"
  ADD CONSTRAINT "chk_transaction_log_entries_direction"
  CHECK ("direction" IN ('debit', 'credit'));
ALTER TABLE "position_keeping"."financial_position_logs"
  ADD CONSTRAINT "chk_financial_position_logs_current_status"
  CHECK ("current_status" IN ('pending', 'completed', 'failed', 'reconciled'));
ALTER TABLE "position_keeping"."financial_position_logs"
  ADD CONSTRAINT "chk_financial_position_logs_previous_status"
  CHECK ("previous_status" IS NULL OR "previous_status" IN ('pending', 'completed', 'failed', 'reconciled'));
ALTER TABLE "position_keeping"."financial_position_logs"
  ADD CONSTRAINT "chk_financial_position_logs_reconciliation_status"
  CHECK ("reconciliation_status" IN ('pending', 'matched', 'unmatched', 'resolved'));
