-- Create "deposit_idempotency" table
-- Per-deposit idempotency marker written in the same transaction as a deposit's
-- ledger postings, making exactly-once deposit processing a database invariant.
-- The primary key on dedupe_key rejects a redelivered deposit before any
-- duplicate postings are written. See DepositIdempotencyEntity and the deposit
-- consumer's dedupe-key derivation.
CREATE TABLE "deposit_idempotency" (
  "dedupe_key" character varying(64) NOT NULL,
  "account_id" character varying(255) NOT NULL,
  "correlation_id" character varying(255) NULL,
  "created_at" timestamptz NOT NULL,
  PRIMARY KEY ("dedupe_key")
);
