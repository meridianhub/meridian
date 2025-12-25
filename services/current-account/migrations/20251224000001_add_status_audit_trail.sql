-- Add status audit trail for BIAN Control Record operations
-- Tracks all status transitions (ACTIVE -> FROZEN -> ACTIVE -> CLOSED) for regulatory compliance

-- Add freeze_reason column to capture the reason when account is frozen
ALTER TABLE "account" ADD COLUMN "freeze_reason" varchar(1000) NULL;

-- Add status_history JSONB column for audit trail
-- Structure: [{"from_status": "ACTIVE", "to_status": "FROZEN", "reason": "...", "timestamp": "2025-01-15T10:30:00Z", "changed_by": "user_id"}]
ALTER TABLE "account" ADD COLUMN "status_history" jsonb NOT NULL DEFAULT '[]'::jsonb;

-- Add index on status column for operational queries (e.g., find all frozen accounts)
CREATE INDEX idx_account_status ON "account" ("status");

-- Add GIN index on status_history for audit trail queries
-- Enables efficient queries like: SELECT * FROM account WHERE status_history @> '[{"to_status": "FROZEN"}]'
CREATE INDEX idx_account_status_history ON "account" USING GIN ("status_history");

-- Add comment documenting the status_history structure
COMMENT ON COLUMN "account"."status_history" IS
    'JSONB array of status changes for audit trail. Each entry contains: from_status, to_status, reason, timestamp, changed_by. Appended on each Freeze/Unfreeze/Close operation.';

COMMENT ON COLUMN "account"."freeze_reason" IS
    'Current freeze reason when account status is FROZEN. Cleared when account is unfrozen.';
