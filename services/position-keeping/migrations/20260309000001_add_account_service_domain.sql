-- Add account_service_domain column to financial_position_log.
-- This column identifies which BIAN Service Domain (Current Account or Internal Account)
-- manages the account referenced by account_id. Populated during account validation.
-- Empty string means unresolved (legacy records or graceful degradation).

ALTER TABLE financial_position_log
    ADD COLUMN IF NOT EXISTS account_service_domain VARCHAR(20) NOT NULL DEFAULT '';
