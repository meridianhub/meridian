-- Populate account_id from account_number and add constraints
-- Separate migration required for CockroachDB (cannot UPDATE column in same transaction as ADD COLUMN)

-- Populate account_id from existing account_number for existing rows
UPDATE "current_account"."accounts"
SET "account_id" = "account_number"
WHERE "account_id" IS NULL;
