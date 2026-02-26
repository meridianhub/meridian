-- Backfill precision and instrument_code for existing rows.
-- Per CockroachDB rules: this DML is in a separate migration because
-- precision, instrument_code, and dimension were added in 20260226000003.
--
-- account: precision=2 for all CURRENCY accounts (GBP, USD, EUR all use 2 decimal places).
-- JPY is an exception (0 decimal places) but handled at application layer via currency registry.
UPDATE "account" SET "precision" = 2 WHERE "dimension" = 'CURRENCY';

-- lien: copy currency -> instrument_code, set dimension and precision from account's values.
-- Using a subquery join to get dimension/precision from the linked account.
UPDATE "lien" l
SET "instrument_code" = l."currency",
    "dimension" = 'CURRENCY',
    "precision" = 2;

-- withdrawal: copy currency -> instrument_code, set dimension and precision.
UPDATE "withdrawal" w
SET "instrument_code" = w."currency",
    "dimension" = 'CURRENCY',
    "precision" = 2;
