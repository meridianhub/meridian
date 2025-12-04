-- Add missing domain columns to current_account.accounts
-- Resolves issue #209: OverdraftRate and BalanceUpdatedAt fields

ALTER TABLE "current_account"."accounts"
ADD COLUMN "overdraft_rate" numeric(5,4) NOT NULL DEFAULT 0,
ADD COLUMN "balance_updated_at" timestamptz NULL;

-- Backfill balance_updated_at with updated_at for existing rows
UPDATE "current_account"."accounts"
SET "balance_updated_at" = "updated_at"
WHERE "balance_updated_at" IS NULL;
