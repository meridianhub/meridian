-- Rename lien.currency to lien.instrument_code for multi-asset consistency.
-- All other services use instrument_code; this aligns the internal-account lien table.

ALTER TABLE "lien" RENAME COLUMN "currency" TO "instrument_code";
