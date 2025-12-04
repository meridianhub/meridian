-- Rename account_number to account_identification to match GORM entity
-- This aligns with BIAN terminology (Account Identification = IBAN)

ALTER TABLE "current_account"."accounts"
RENAME COLUMN "account_number" TO "account_identification";

-- Rename the index to match
ALTER INDEX "current_account"."idx_current_account_accounts_account_number"
RENAME TO "idx_current_account_accounts_account_identification";
