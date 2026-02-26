-- Add precision column to account, lien, and withdrawal tables.
-- Precision stores the number of decimal places for the instrument.
-- Default of 2 handles existing CURRENCY accounts (GBP, USD, EUR all use 2 decimal places).
--
-- Also add instrument_code and dimension columns to lien and withdrawal tables,
-- replacing the legacy currency column for multi-asset support.
-- Per CockroachDB rules: DML referencing new columns must be in a separate migration.

ALTER TABLE "account" ADD COLUMN "precision" INT NOT NULL DEFAULT 2;

ALTER TABLE "lien"
    ADD COLUMN "instrument_code" VARCHAR(32) NOT NULL DEFAULT '',
    ADD COLUMN "dimension" VARCHAR(20) NOT NULL DEFAULT 'CURRENCY',
    ADD COLUMN "precision" INT NOT NULL DEFAULT 2;

ALTER TABLE "withdrawal"
    ADD COLUMN "instrument_code" VARCHAR(32) NOT NULL DEFAULT '',
    ADD COLUMN "dimension" VARCHAR(20) NOT NULL DEFAULT 'CURRENCY',
    ADD COLUMN "precision" INT NOT NULL DEFAULT 2;
