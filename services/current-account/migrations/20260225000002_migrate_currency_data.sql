-- Migrate currency data to instrument_code.
-- Copies the existing currency value into instrument_code for all rows.
-- dimension remains 'CURRENCY' (the default set in the previous migration).
-- Per CockroachDB rules: this DML is in a separate migration because
-- instrument_code and dimension were added in 20260225000001.

UPDATE "account" SET "instrument_code" = "currency", "dimension" = 'CURRENCY';
