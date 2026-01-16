-- Add clearing_purpose column to internal_bank_account table
-- This column distinguishes the specific purpose of CLEARING accounts
-- Matches the ClearingPurpose enum in the protobuf definition

-- Add the clearing_purpose column (nullable initially to allow existing data)
ALTER TABLE "internal_bank_account"
ADD COLUMN "clearing_purpose" character varying(32) NULL;

-- Backfill existing CLEARING accounts based on account_code patterns
-- This is a best-effort mapping based on common naming conventions:
--   - Codes ending in '-DEPOSIT' -> CLEARING_PURPOSE_DEPOSIT
--   - Codes ending in '-WITHDRAW' or '-WITHDRAWAL' -> CLEARING_PURPOSE_WITHDRAWAL
--   - Codes ending in '-SETTLEMENT' -> CLEARING_PURPOSE_SETTLEMENT
--   - All other CLEARING accounts -> CLEARING_PURPOSE_GENERAL
-- NOTE: Backfill MUST run before adding the constraint to avoid migration failure
UPDATE "internal_bank_account"
SET "clearing_purpose" = CASE
  WHEN account_code LIKE '%-DEPOSIT' OR account_code LIKE '%-deposit' THEN 'CLEARING_PURPOSE_DEPOSIT'
  WHEN account_code LIKE '%-WITHDRAW' OR account_code LIKE '%-WITHDRAWAL'
       OR account_code LIKE '%-withdraw' OR account_code LIKE '%-withdrawal' THEN 'CLEARING_PURPOSE_WITHDRAWAL'
  WHEN account_code LIKE '%-SETTLEMENT' OR account_code LIKE '%-settlement' THEN 'CLEARING_PURPOSE_SETTLEMENT'
  ELSE 'CLEARING_PURPOSE_GENERAL'
END
WHERE account_type = 'CLEARING' AND clearing_purpose IS NULL;

-- Add check constraint to enforce that CLEARING accounts must have a non-null clearing_purpose
-- Non-CLEARING accounts should have NULL or CLEARING_PURPOSE_UNSPECIFIED
-- NOTE: Constraint added AFTER backfill to ensure existing data satisfies it
ALTER TABLE "internal_bank_account"
ADD CONSTRAINT "chk_clearing_purpose_for_clearing_type"
CHECK (
  account_type != 'CLEARING' OR clearing_purpose IS NOT NULL
);

-- Create partial index for efficient filtering of clearing accounts by purpose
-- Only indexes rows where account_type = 'CLEARING', reducing index size
CREATE INDEX "idx_internal_bank_account_clearing_purpose"
ON "internal_bank_account" ("clearing_purpose")
WHERE account_type = 'CLEARING';

-- Add comment documenting the column
COMMENT ON COLUMN "internal_bank_account"."clearing_purpose" IS
  'Purpose classification for CLEARING accounts: CLEARING_PURPOSE_DEPOSIT (deposits), CLEARING_PURPOSE_WITHDRAWAL (withdrawals), CLEARING_PURPOSE_SETTLEMENT (settlements), CLEARING_PURPOSE_GENERAL (general). NULL or UNSPECIFIED for non-CLEARING account types.';
