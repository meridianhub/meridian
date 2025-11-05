-- Note: This migration assumes current_account schema already exists (created by current_account migrations)
-- Add new schema named "position_keeping"
CREATE SCHEMA "position_keeping";
-- Create "transactions" table
CREATE TABLE "position_keeping"."transactions" (
  "id" uuid NOT NULL DEFAULT gen_random_uuid(),
  "created_at" timestamptz NOT NULL DEFAULT now(),
  "created_by" character varying(100) NOT NULL,
  "updated_at" timestamptz NOT NULL DEFAULT now(),
  "updated_by" character varying(100) NOT NULL,
  "deleted_at" timestamptz NULL,
  "transaction_id" character varying(100) NOT NULL,
  "transaction_type" character varying(50) NOT NULL,
  "account_id" uuid NOT NULL,
  "amount" bigint NOT NULL,
  "currency" character(3) NOT NULL DEFAULT 'GBP',
  "description" text NULL,
  "reference" character varying(100) NULL,
  "status" character varying(20) NOT NULL DEFAULT 'pending',
  "counterparty_account_id" uuid NULL,
  "counterparty_name" character varying(255) NULL,
  "balance_after" bigint NOT NULL,
  "processed_at" timestamptz NULL,
  "reversed_at" timestamptz NULL,
  PRIMARY KEY ("id"),
  CONSTRAINT "fk_position_keeping_transactions_account" FOREIGN KEY ("account_id") REFERENCES "current_account"."accounts" ("id") ON UPDATE NO ACTION ON DELETE RESTRICT,
  CONSTRAINT "fk_position_keeping_transactions_counterparty_account" FOREIGN KEY ("counterparty_account_id") REFERENCES "current_account"."accounts" ("id") ON UPDATE NO ACTION ON DELETE SET NULL
);
-- Create index "idx_position_keeping_transactions_account_id" to table: "transactions"
CREATE INDEX "idx_position_keeping_transactions_account_id" ON "position_keeping"."transactions" ("account_id");
-- Create index "idx_position_keeping_transactions_counterparty_account_id" to table: "transactions"
CREATE INDEX "idx_position_keeping_transactions_counterparty_account_id" ON "position_keeping"."transactions" ("counterparty_account_id");
-- Create index "idx_position_keeping_transactions_deleted_at" to table: "transactions"
CREATE INDEX "idx_position_keeping_transactions_deleted_at" ON "position_keeping"."transactions" ("deleted_at");
-- Create index "idx_position_keeping_transactions_processed_at" to table: "transactions"
CREATE INDEX "idx_position_keeping_transactions_processed_at" ON "position_keeping"."transactions" ("processed_at");
-- Create index "idx_position_keeping_transactions_reversed_at" to table: "transactions"
CREATE INDEX "idx_position_keeping_transactions_reversed_at" ON "position_keeping"."transactions" ("reversed_at");
-- Create index "idx_position_keeping_transactions_transaction_id" to table: "transactions"
CREATE UNIQUE INDEX "idx_position_keeping_transactions_transaction_id" ON "position_keeping"."transactions" ("transaction_id");
