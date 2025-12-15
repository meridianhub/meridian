-- Current Account Service Schema
-- Consolidated migration for clean slate deployment

-- Create schema
CREATE SCHEMA IF NOT EXISTS "current_account";

-- Create "accounts" table
-- Note: No customers table - party data managed by Party Service via gRPC
CREATE TABLE "current_account"."accounts" (
  "id" uuid NOT NULL DEFAULT gen_random_uuid(),
  "created_at" timestamptz NOT NULL DEFAULT now(),
  "created_by" character varying(100) NOT NULL,
  "updated_at" timestamptz NOT NULL DEFAULT now(),
  "updated_by" character varying(100) NOT NULL,
  "deleted_at" timestamptz NULL,

  -- Account identifiers
  "account_id" character varying(100) NOT NULL,
  "account_identification" character varying(34) NOT NULL,

  -- Account details
  "account_type" character varying(50) NOT NULL,
  "currency" character varying(3) NOT NULL DEFAULT 'GBP',
  "status" character varying(20) NOT NULL DEFAULT 'active',

  -- Party reference (external - Party Service via gRPC)
  "party_id" uuid NOT NULL,

  -- Balances
  "balance" bigint NOT NULL DEFAULT 0,
  "available_balance" bigint NOT NULL DEFAULT 0,
  "overdraft_limit" bigint NOT NULL DEFAULT 0,
  "overdraft_rate" numeric(5,4) NOT NULL DEFAULT 0,
  "balance_updated_at" timestamptz NOT NULL DEFAULT now(),

  -- Lifecycle
  "opened_at" timestamptz NULL,
  "closed_at" timestamptz NULL,

  -- Optimistic locking
  "version" bigint NOT NULL DEFAULT 1,

  PRIMARY KEY ("id")
);

-- Indexes for accounts
CREATE UNIQUE INDEX "idx_current_account_accounts_account_id" ON "current_account"."accounts" ("account_id");
CREATE UNIQUE INDEX "idx_current_account_accounts_account_identification" ON "current_account"."accounts" ("account_identification");
CREATE INDEX "idx_current_account_accounts_party_id" ON "current_account"."accounts" ("party_id");
CREATE INDEX "idx_current_account_accounts_deleted_at" ON "current_account"."accounts" ("deleted_at");
CREATE INDEX "idx_current_account_accounts_opened_at" ON "current_account"."accounts" ("opened_at");
CREATE INDEX "idx_current_account_accounts_closed_at" ON "current_account"."accounts" ("closed_at");

-- Comment documenting party_id
COMMENT ON COLUMN "current_account"."accounts"."party_id" IS
  'References a party in the Party Service (accessed via gRPC). Not a foreign key constraint as Party Service is a separate microservice.';

-- Create "liens" table for tracking holds on account balances
CREATE TABLE "current_account"."liens" (
  "id" uuid NOT NULL DEFAULT gen_random_uuid(),
  "account_id" uuid NOT NULL,
  "amount_cents" bigint NOT NULL,
  "currency" character varying(3) NOT NULL,
  "status" character varying(20) NOT NULL,
  "payment_order_reference" character varying(255) NOT NULL,
  "termination_reason" character varying(1000) NULL,
  "expires_at" timestamptz NULL,
  "created_at" timestamptz NOT NULL DEFAULT now(),
  "updated_at" timestamptz NOT NULL DEFAULT now(),
  "version" bigint NOT NULL DEFAULT 1,
  PRIMARY KEY ("id"),
  CONSTRAINT "chk_liens_amount_cents" CHECK (amount_cents > 0),
  CONSTRAINT "chk_liens_status" CHECK (status IN ('ACTIVE', 'EXECUTED', 'TERMINATED')),
  CONSTRAINT "fk_liens_account" FOREIGN KEY ("account_id") REFERENCES "current_account"."accounts" ("id") ON UPDATE NO ACTION ON DELETE RESTRICT
);

-- Indexes for liens
CREATE INDEX "idx_liens_account_status" ON "current_account"."liens" ("account_id", "status");
CREATE UNIQUE INDEX "idx_liens_payment_order" ON "current_account"."liens" ("payment_order_reference");
CREATE INDEX "idx_liens_expires_at" ON "current_account"."liens" ("expires_at");
