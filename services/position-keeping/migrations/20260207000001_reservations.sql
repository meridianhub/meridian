-- Reservations Table Migration
-- Tracks lien-based reservations for projected balance calculations.
-- Each reservation is linked to a lien_id from Current Account and holds a reserved amount
-- against an account's position until the reservation is executed or terminated.

CREATE TABLE "reservation" (
  "lien_id" uuid NOT NULL,
  "account_id" character varying(255) NOT NULL,
  "instrument_code" character varying(32) NOT NULL,
  "bucket_id" character varying(256) NOT NULL DEFAULT '',
  "reserved_amount" decimal(38, 18) NOT NULL,
  "status" character varying(16) NOT NULL DEFAULT 'ACTIVE',
  "created_at" timestamptz NOT NULL DEFAULT now(),
  "executed_at" timestamptz NULL,
  "terminated_at" timestamptz NULL,
  PRIMARY KEY ("lien_id")
);

-- Check constraint for valid status values
ALTER TABLE "reservation"
  ADD CONSTRAINT "chk_reservation_status"
  CHECK ("status" IN ('ACTIVE', 'EXECUTED', 'TERMINATED'));

-- Index for projected balance queries: filter by account, instrument, status, and bucket
CREATE INDEX "idx_reservation_projected_balance"
  ON "reservation" ("account_id", "instrument_code", "status", "bucket_id");

-- Index for active reservations lookup (partial index for efficiency)
CREATE INDEX "idx_reservation_active"
  ON "reservation" ("account_id", "instrument_code", "bucket_id")
  WHERE "status" = 'ACTIVE';

COMMENT ON TABLE "reservation" IS 'Lien-based reservations for projected balance calculations. Primary key is lien_id for natural idempotency.';
