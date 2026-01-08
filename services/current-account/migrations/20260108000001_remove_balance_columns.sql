-- Remove balance columns from account table
-- Balance computation is now delegated to Position Keeping service per BIAN architecture
-- See ADR-0002 for BIAN service boundary decisions

-- Drop balance-related columns (now computed by Position Keeping service)
ALTER TABLE "account" DROP COLUMN IF EXISTS "balance";
ALTER TABLE "account" DROP COLUMN IF EXISTS "available_balance";
ALTER TABLE "account" DROP COLUMN IF EXISTS "balance_updated_at";

-- Add comment documenting balance delegation
COMMENT ON TABLE "account" IS
  'Current Account facility metadata. Balance computation is delegated to Position Keeping service per BIAN architecture.';
