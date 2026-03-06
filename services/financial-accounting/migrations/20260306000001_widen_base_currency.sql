-- Widen financial_booking_log.base_currency from VARCHAR(3) to VARCHAR(32)
-- for multi-asset support. Supports non-ISO instrument codes like 'KWH', 'GPU_HOUR'.
-- Note: ledger_posting.currency was already widened to VARCHAR(32) in 20260105000001.

ALTER TABLE "financial_booking_log"
    ALTER COLUMN "base_currency" TYPE VARCHAR(32);
