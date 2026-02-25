-- Add instrument_code and dimension columns to account table.
-- These columns replace the currency column to support multi-asset accounts.
-- Defaults preserve existing GBP/CURRENCY data for all current rows.
-- Per CockroachDB rules: DML referencing new columns must be in a separate migration.
--
-- NOTE: The unique partial index idx_account_syndicate_scope_integrity (on currency)
-- is dropped here before adding new columns to avoid CockroachDB concurrent mutation
-- conflicts. It is recreated on instrument_code in 20260225000003.

DROP INDEX IF EXISTS "idx_account_syndicate_scope_integrity";

ALTER TABLE "account" ADD COLUMN "instrument_code" VARCHAR(32) NOT NULL DEFAULT 'GBP';
ALTER TABLE "account" ADD COLUMN "dimension" VARCHAR(20) NOT NULL DEFAULT 'CURRENCY';
