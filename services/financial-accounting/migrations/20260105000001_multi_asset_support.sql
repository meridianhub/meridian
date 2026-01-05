-- Migration: Add multi-asset support columns to ledger_posting
-- Part of Universal Asset System task 26: Migrate financial-accounting to Qty[D] type
--
-- This migration adds columns needed to support multi-asset quantities beyond fiat currencies.
-- New columns:
--   - dimension_type: distinguishes CURRENCY from ENERGY, COMPUTE, CARBON, etc.
--   - instrument_version: schema version for instrument definition
--   - instrument_precision: decimal places for proper rounding
--   - attributes: JSONB storage for contextual metadata
--
-- The existing 'currency' column is widened to VARCHAR(32) to support non-ISO codes
-- like 'KWH', 'CARBON_CREDIT', 'GPU_HOUR'.

-- Add new columns with defaults for backward compatibility
ALTER TABLE "ledger_posting"
    ADD COLUMN IF NOT EXISTS "dimension_type" character varying(20) DEFAULT 'CURRENCY',
    ADD COLUMN IF NOT EXISTS "instrument_version" integer DEFAULT 1,
    ADD COLUMN IF NOT EXISTS "instrument_precision" integer DEFAULT 2,
    ADD COLUMN IF NOT EXISTS "attributes" jsonb DEFAULT '{}';

-- Widen the currency column to accommodate non-ISO instrument codes
ALTER TABLE "ledger_posting"
    ALTER COLUMN "currency" TYPE character varying(32);

-- Backfill dimension_type for existing rows (all are monetary)
UPDATE "ledger_posting"
SET "dimension_type" = 'CURRENCY'
WHERE "dimension_type" IS NULL;

-- Make dimension_type NOT NULL after backfill
ALTER TABLE "ledger_posting"
    ALTER COLUMN "dimension_type" SET NOT NULL;

-- Add an index on dimension_type for filtering by asset class
CREATE INDEX IF NOT EXISTS "idx_ledger_posting_dimension_type" ON "ledger_posting" ("dimension_type");
