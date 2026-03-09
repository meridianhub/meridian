-- Add account_service_domain column to ledger_posting.
-- Identifies which BIAN Service Domain (Current Account or Internal Account)
-- manages the account referenced by account_id.
-- Empty string means unresolved (legacy records or when caller doesn't provide it).

ALTER TABLE ledger_posting
    ADD COLUMN IF NOT EXISTS account_service_domain VARCHAR(20) NOT NULL DEFAULT '';
