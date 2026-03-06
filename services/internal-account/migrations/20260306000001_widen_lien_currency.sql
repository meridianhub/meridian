-- Widen lien.currency from VARCHAR(3) to VARCHAR(32) for multi-asset support.
-- Supports non-ISO instrument codes like 'KWH', 'CARBON_CREDIT', 'GPU_HOUR'.
-- Note: internal_bank_account already has instrument_code VARCHAR(32) from initial schema.

ALTER TABLE "lien"
    ALTER COLUMN "currency" TYPE VARCHAR(32);
