-- Drop schema named "public"
DROP SCHEMA "public" CASCADE;
-- Add new schema named "current_account"
CREATE SCHEMA "current_account";
-- Add new schema named "position_keeping"
CREATE SCHEMA "position_keeping";
-- Create "customers" table
CREATE TABLE "current_account"."customers" (
  "id" uuid NOT NULL DEFAULT gen_random_uuid(),
  "created_at" timestamptz NOT NULL DEFAULT now(),
  "created_by" character varying(100) NOT NULL,
  "updated_at" timestamptz NOT NULL DEFAULT now(),
  "updated_by" character varying(100) NOT NULL,
  "deleted_at" timestamptz NULL,
  "customer_number" character varying(50) NOT NULL,
  "first_name" character varying(100) NOT NULL,
  "last_name" character varying(100) NOT NULL,
  "email" character varying(255) NULL,
  "phone" character varying(20) NULL,
  "status" character varying(20) NOT NULL DEFAULT 'active',
  PRIMARY KEY ("id")
);
-- Create index "idx_current_account_customers_customer_number" to table: "customers"
CREATE UNIQUE INDEX "idx_current_account_customers_customer_number" ON "current_account"."customers" ("customer_number");
-- Create index "idx_current_account_customers_deleted_at" to table: "customers"
CREATE INDEX "idx_current_account_customers_deleted_at" ON "current_account"."customers" ("deleted_at");
-- Create index "idx_current_account_customers_email" to table: "customers"
CREATE UNIQUE INDEX "idx_current_account_customers_email" ON "current_account"."customers" ("email");
-- Create "accounts" table
CREATE TABLE "current_account"."accounts" (
  "id" uuid NOT NULL DEFAULT gen_random_uuid(),
  "created_at" timestamptz NOT NULL DEFAULT now(),
  "created_by" character varying(100) NOT NULL,
  "updated_at" timestamptz NOT NULL DEFAULT now(),
  "updated_by" character varying(100) NOT NULL,
  "deleted_at" timestamptz NULL,
  "account_number" character varying(34) NOT NULL,
  "account_type" character varying(50) NOT NULL,
  "currency" character(3) NOT NULL DEFAULT 'GBP',
  "status" character varying(20) NOT NULL DEFAULT 'active',
  "customer_id" uuid NOT NULL,
  "balance" bigint NOT NULL DEFAULT 0,
  "available_balance" bigint NOT NULL DEFAULT 0,
  "overdraft_limit" bigint NOT NULL DEFAULT 0,
  "opened_at" timestamptz NULL,
  "closed_at" timestamptz NULL,
  PRIMARY KEY ("id"),
  CONSTRAINT "fk_current_account_customers_accounts" FOREIGN KEY ("customer_id") REFERENCES "current_account"."customers" ("id") ON UPDATE NO ACTION ON DELETE RESTRICT
);
-- Create index "idx_current_account_accounts_account_number" to table: "accounts"
CREATE UNIQUE INDEX "idx_current_account_accounts_account_number" ON "current_account"."accounts" ("account_number");
-- Create index "idx_current_account_accounts_closed_at" to table: "accounts"
CREATE INDEX "idx_current_account_accounts_closed_at" ON "current_account"."accounts" ("closed_at");
-- Create index "idx_current_account_accounts_customer_id" to table: "accounts"
CREATE INDEX "idx_current_account_accounts_customer_id" ON "current_account"."accounts" ("customer_id");
-- Create index "idx_current_account_accounts_deleted_at" to table: "accounts"
CREATE INDEX "idx_current_account_accounts_deleted_at" ON "current_account"."accounts" ("deleted_at");
-- Create index "idx_current_account_accounts_opened_at" to table: "accounts"
CREATE INDEX "idx_current_account_accounts_opened_at" ON "current_account"."accounts" ("opened_at");
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
