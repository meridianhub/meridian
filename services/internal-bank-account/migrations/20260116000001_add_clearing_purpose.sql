-- Add clearing_purpose column to internal_bank_account table
-- This column specifies the operational purpose for CLEARING account types.
-- Non-CLEARING accounts will have 'UNSPECIFIED' as the default value.

-- Add the clearing_purpose column
ALTER TABLE "internal_bank_account" ADD COLUMN "clearing_purpose" character varying(20) NOT NULL DEFAULT 'UNSPECIFIED';

-- Add CHECK constraint for valid clearing purpose values
ALTER TABLE "internal_bank_account" ADD CONSTRAINT "chk_clearing_purpose" CHECK (clearing_purpose IN (
  'UNSPECIFIED', 'DEPOSIT', 'WITHDRAWAL', 'SETTLEMENT', 'GENERAL'
));

-- Add index for filtered queries by clearing purpose
CREATE INDEX "idx_internal_bank_account_clearing_purpose" ON "internal_bank_account" ("clearing_purpose");

-- Add comment for documentation
COMMENT ON COLUMN "internal_bank_account"."clearing_purpose" IS
  'Operational purpose for CLEARING accounts: DEPOSIT (incoming), WITHDRAWAL (outgoing), SETTLEMENT (inter-party), GENERAL (multi-purpose). UNSPECIFIED for non-CLEARING account types.';
