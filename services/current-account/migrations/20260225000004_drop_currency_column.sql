-- Drop the currency column now that data has been migrated to instrument_code
-- and the unique index has been recreated in 20260225000003.

ALTER TABLE "account" DROP COLUMN "currency";
