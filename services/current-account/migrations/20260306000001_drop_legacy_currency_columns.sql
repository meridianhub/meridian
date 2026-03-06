-- Drop legacy currency columns from lien and withdrawal tables.
-- These tables already have instrument_code VARCHAR(32), dimension, and precision
-- columns added in 20260226000003 and backfilled in 20260226000004.
-- The currency column is now redundant.

ALTER TABLE "lien" DROP COLUMN "currency";
ALTER TABLE "withdrawal" DROP COLUMN "currency";
