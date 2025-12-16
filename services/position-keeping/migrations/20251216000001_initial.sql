-- Position Keeping Service Schema
-- Position-keeping is independent per BIAN domain (ADR-002) - no cross-schema dependencies
-- Uses unqualified table names (relies on database-per-service architecture)

-- Create "financial_position_log" table (singular, unqualified)
CREATE TABLE "financial_position_log" (
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
  -- Note: No FK to current_account database - services are independent per BIAN domain (ADR-002)
  -- Account validation is done at the application level via gRPC
);
-- Create indexes for financial_position_log
CREATE INDEX "idx_financial_position_log_account_id" ON "financial_position_log" ("account_id");
CREATE INDEX "idx_financial_position_log_current_status" ON "financial_position_log" ("current_status");
CREATE INDEX "idx_financial_position_log_deleted_at" ON "financial_position_log" ("deleted_at");
CREATE UNIQUE INDEX "idx_financial_position_log_log_id" ON "financial_position_log" ("log_id");

-- Create "audit_trail_entry" table (singular, unqualified)
CREATE TABLE "audit_trail_entry" (
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
  CONSTRAINT "fk_audit_trail_entry_financial_position_log" FOREIGN KEY ("financial_position_log_id") REFERENCES "financial_position_log" ("id") ON UPDATE NO ACTION ON DELETE CASCADE
);
-- Create indexes for audit_trail_entry
CREATE UNIQUE INDEX "idx_audit_trail_entry_audit_id" ON "audit_trail_entry" ("audit_id");
CREATE INDEX "idx_audit_trail_entry_deleted_at" ON "audit_trail_entry" ("deleted_at");
CREATE INDEX "idx_audit_trail_entry_log_id" ON "audit_trail_entry" ("financial_position_log_id");
CREATE INDEX "idx_audit_trail_entry_timestamp" ON "audit_trail_entry" ("timestamp");
CREATE INDEX "idx_audit_trail_entry_user_id" ON "audit_trail_entry" ("user_id");

-- Create "transaction_lineage" table (singular, unqualified)
CREATE TABLE "transaction_lineage" (
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
  CONSTRAINT "fk_transaction_lineage_financial_position_log" FOREIGN KEY ("financial_position_log_id") REFERENCES "financial_position_log" ("id") ON UPDATE NO ACTION ON DELETE CASCADE
);
-- Create indexes for transaction_lineage
CREATE INDEX "idx_transaction_lineage_deleted_at" ON "transaction_lineage" ("deleted_at");
CREATE UNIQUE INDEX "idx_transaction_lineage_log_id" ON "transaction_lineage" ("financial_position_log_id");
CREATE INDEX "idx_transaction_lineage_parent_id" ON "transaction_lineage" ("parent_transaction_id");
CREATE INDEX "idx_transaction_lineage_transaction_id" ON "transaction_lineage" ("transaction_id");

-- Create "transaction_log_entry" table (singular, unqualified)
CREATE TABLE "transaction_log_entry" (
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
  CONSTRAINT "fk_transaction_log_entry_financial_position_log" FOREIGN KEY ("financial_position_log_id") REFERENCES "financial_position_log" ("id") ON UPDATE NO ACTION ON DELETE CASCADE
  -- Note: No FK to current_account database - services are independent per BIAN domain (ADR-002)
);
-- Create indexes for transaction_log_entry
CREATE INDEX "idx_transaction_log_entry_deleted_at" ON "transaction_log_entry" ("deleted_at");
CREATE UNIQUE INDEX "idx_transaction_log_entry_entry_id" ON "transaction_log_entry" ("entry_id");
CREATE INDEX "idx_transaction_log_entry_log_id" ON "transaction_log_entry" ("financial_position_log_id");
CREATE INDEX "idx_transaction_log_entry_timestamp" ON "transaction_log_entry" ("timestamp");
CREATE INDEX "idx_transaction_log_entry_transaction_id" ON "transaction_log_entry" ("transaction_id");

-- Add validation constraints
ALTER TABLE "transaction_log_entry"
  ADD CONSTRAINT "chk_transaction_log_entry_currency"
  CHECK (char_length("currency") = 3);
ALTER TABLE "transaction_log_entry"
  ADD CONSTRAINT "chk_transaction_log_entry_direction"
  CHECK ("direction" IN ('DEBIT', 'CREDIT'));
ALTER TABLE "financial_position_log"
  ADD CONSTRAINT "chk_financial_position_log_current_status"
  CHECK ("current_status" IN ('PENDING', 'RECONCILED', 'POSTED', 'CANCELLED', 'FAILED', 'REJECTED', 'AMENDED', 'REVERSED'));
ALTER TABLE "financial_position_log"
  ADD CONSTRAINT "chk_financial_position_log_previous_status"
  CHECK ("previous_status" IS NULL OR "previous_status" IN ('PENDING', 'RECONCILED', 'POSTED', 'CANCELLED', 'FAILED', 'REJECTED', 'AMENDED', 'REVERSED'));
ALTER TABLE "financial_position_log"
  ADD CONSTRAINT "chk_financial_position_log_reconciliation_status"
  CHECK ("reconciliation_status" IN ('UNRECONCILED', 'MATCHED', 'MISMATCHED', 'RESOLVED'));
