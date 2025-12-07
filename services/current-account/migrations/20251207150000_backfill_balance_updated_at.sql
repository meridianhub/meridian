-- Backfill balance_updated_at with updated_at for existing rows
-- Separated from column addition due to CockroachDB backfill behavior
-- See: https://www.cockroachlabs.com/docs/stable/online-schema-changes

UPDATE "current_account"."accounts"
SET "balance_updated_at" = "updated_at"
WHERE "balance_updated_at" IS NULL;
