-- Add clearing_purpose column to internal_bank_account table (Part 1)
-- This column distinguishes the specific purpose of CLEARING accounts
-- Matches the ClearingPurpose enum in the protobuf definition
--
-- CockroachDB compatibility: Split into two migrations to avoid backfill errors
-- Part 1: Add column (this file)
-- Part 2: Backfill and add constraints (next migration)

-- Add the clearing_purpose column (nullable initially to allow existing data)
ALTER TABLE "internal_bank_account"
ADD COLUMN "clearing_purpose" character varying(32) NULL;

-- Add comment documenting the column
COMMENT ON COLUMN "internal_bank_account"."clearing_purpose" IS
  'Purpose classification for CLEARING accounts: CLEARING_PURPOSE_DEPOSIT (deposits), CLEARING_PURPOSE_WITHDRAWAL (withdrawals), CLEARING_PURPOSE_SETTLEMENT (settlements), CLEARING_PURPOSE_GENERAL (general). NULL or UNSPECIFIED for non-CLEARING account types.';
