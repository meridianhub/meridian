-- Widen currency columns from CHAR(3) to VARCHAR(32) for multi-asset support.
-- Supports non-ISO instrument codes like 'KWH', 'CARBON_CREDIT', 'GPU_HOUR'.

-- Drop the char_length=3 constraint before widening.
ALTER TABLE "transaction_log_entry"
    DROP CONSTRAINT "chk_transaction_log_entry_currency";

ALTER TABLE "transaction_log_entry"
    ALTER COLUMN "currency" TYPE VARCHAR(32);

-- Widen opening_balance_currency on financial_position_log.
ALTER TABLE "financial_position_log"
    DROP CONSTRAINT "chk_financial_position_log_opening_balance_currency";

ALTER TABLE "financial_position_log"
    ALTER COLUMN "opening_balance_currency" TYPE VARCHAR(32);
