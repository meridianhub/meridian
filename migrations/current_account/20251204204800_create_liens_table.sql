-- Create "liens" table for tracking holds on account balances
CREATE TABLE "current_account"."liens" (
  "id" uuid NOT NULL,
  "account_id" uuid NOT NULL,
  "amount_cents" bigint NOT NULL,
  "currency" character varying(3) NOT NULL,
  "status" character varying(20) NOT NULL,
  "payment_order_reference" character varying(255) NOT NULL,
  "termination_reason" character varying(1000) NULL,
  "expires_at" timestamptz NULL,
  "created_at" timestamptz NOT NULL,
  "updated_at" timestamptz NOT NULL,
  "version" integer NOT NULL DEFAULT 1,
  PRIMARY KEY ("id"),
  CONSTRAINT "chk_liens_amount_cents" CHECK (amount_cents > 0),
  CONSTRAINT "chk_liens_status" CHECK (status IN ('ACTIVE', 'EXECUTED', 'TERMINATED')),
  CONSTRAINT "fk_liens_account" FOREIGN KEY ("account_id") REFERENCES "current_account"."accounts" ("id") ON UPDATE NO ACTION ON DELETE RESTRICT
);
-- Create index "idx_liens_account_status" for account + status lookups
CREATE INDEX "idx_liens_account_status" ON "current_account"."liens" ("account_id", "status");
-- Create unique index "idx_liens_payment_order" for payment order reference uniqueness
CREATE UNIQUE INDEX "idx_liens_payment_order" ON "current_account"."liens" ("payment_order_reference");
-- Create index "idx_liens_expires_at" for expiration queries
CREATE INDEX "idx_liens_expires_at" ON "current_account"."liens" ("expires_at");
