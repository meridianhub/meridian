-- Add account_id column to match GORM entity definition
-- The GORM model expects account_id but migration only had account_number
-- Note: CockroachDB requires separate migrations for ADD COLUMN and UPDATE/INDEX

-- Add account_id column (nullable initially)
ALTER TABLE "current_account"."accounts"
ADD COLUMN "account_id" character varying(100) NULL;
