-- Create withdrawal table for tracking account withdrawals
-- Part of Current Account Service - database-per-service architecture
-- Uses singular, unqualified table name

CREATE TABLE "withdrawal" (
  "id" uuid NOT NULL DEFAULT gen_random_uuid(),
  "account_id" uuid NOT NULL,
  "amount_cents" bigint NOT NULL,
  "currency" character varying(3) NOT NULL,
  "status" character varying(20) NOT NULL,
  "reference" character varying(255) NOT NULL,
  "created_at" timestamptz NOT NULL DEFAULT now(),
  "updated_at" timestamptz NOT NULL DEFAULT now(),
  "version" bigint NOT NULL DEFAULT 1,
  PRIMARY KEY ("id"),
  CONSTRAINT "chk_withdrawal_amount_cents" CHECK (amount_cents > 0),
  CONSTRAINT "chk_withdrawal_status" CHECK (status IN ('PENDING', 'COMPLETED', 'FAILED', 'CANCELLED')),
  CONSTRAINT "fk_withdrawal_account" FOREIGN KEY ("account_id") REFERENCES "account" ("id") ON UPDATE NO ACTION ON DELETE RESTRICT
);

-- Indexes for withdrawal
-- Composite index for queries filtering by account and status (most common query pattern)
CREATE INDEX "idx_withdrawal_account_status" ON "withdrawal" ("account_id", "status");

-- Unique index on reference for idempotency checks
CREATE UNIQUE INDEX "idx_withdrawal_reference" ON "withdrawal" ("reference");

-- Index for time-based queries (most recent first)
CREATE INDEX "idx_withdrawal_created_at" ON "withdrawal" ("created_at" DESC);

-- Comment documenting table purpose
COMMENT ON TABLE "withdrawal" IS
  'Tracks account withdrawals through their lifecycle (PENDING -> COMPLETED/FAILED/CANCELLED). Uses reference for idempotency.';

COMMENT ON COLUMN "withdrawal"."account_id" IS
  'Foreign key to account table. Enforced with RESTRICT delete to prevent orphaned withdrawals.';

COMMENT ON COLUMN "withdrawal"."reference" IS
  'Unique reference for idempotency. Clients should generate this to prevent duplicate withdrawals.';
