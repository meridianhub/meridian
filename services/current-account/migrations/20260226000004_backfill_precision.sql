-- Backfill precision and instrument_code for existing rows.
-- Per CockroachDB rules: this DML is in a separate migration because
-- precision, instrument_code, and dimension were added in 20260226000003.
--
-- account: use instrument_code to derive precision for known currency exceptions.
-- Most CURRENCY instruments use precision=2; JPY is an exception (precision=0).
-- Precision for non-CURRENCY dimensions is populated in separate service migrations.
UPDATE "account"
SET "precision" = CASE
    WHEN "instrument_code" = 'JPY' THEN 0
    ELSE 2
END
WHERE "dimension" = 'CURRENCY';

-- lien: copy currency -> instrument_code, derive dimension and precision from the
-- linked account row (which has already been backfilled above).
-- Foreign key: lien.account_id -> account.id
UPDATE "lien" l
SET "instrument_code" = l."currency",
    "dimension" = a."dimension",
    "precision" = a."precision"
FROM "account" a
WHERE a."id" = l."account_id";

-- withdrawal: copy currency -> instrument_code, derive dimension and precision from
-- the linked account row.
-- Note: the FK in the original schema is on account_id -> account.id.
-- Withdrawals without a matching account (orphaned rows) retain the default values.
UPDATE "withdrawal" w
SET "instrument_code" = w."currency",
    "dimension" = a."dimension",
    "precision" = a."precision"
FROM "account" a
WHERE a."id" = w."account_id";
