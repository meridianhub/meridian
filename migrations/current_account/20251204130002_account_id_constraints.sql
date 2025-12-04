-- Add NOT NULL constraint and unique index on account_id
-- Separate migration required for CockroachDB

-- Make account_id NOT NULL after populating
ALTER TABLE "current_account"."accounts"
ALTER COLUMN "account_id" SET NOT NULL;

-- Create unique index on account_id
CREATE UNIQUE INDEX "idx_current_account_accounts_account_id" ON "current_account"."accounts" ("account_id");
