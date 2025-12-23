-- Add provisioning_pending status to tenant check constraint
-- Required for async provisioning workflow where tenants are created with
-- PROVISIONING_PENDING status before background worker handles provisioning
--
-- This migration is idempotent:
-- 1. Drops the constraint if it exists (ignores if already dropped)
-- 2. Creates the new constraint only if it doesn't exist
--
-- CockroachDB note: We use a new constraint name to avoid issues with
-- DROP/ADD in the same transaction

-- Drop the old constraint (safe - IF EXISTS handles missing constraint)
ALTER TABLE tenant DROP CONSTRAINT IF EXISTS valid_status;

-- Drop the new constraint name too in case of re-run
ALTER TABLE tenant DROP CONSTRAINT IF EXISTS valid_status_v2;

-- Add updated constraint with new name including provisioning_pending
ALTER TABLE tenant ADD CONSTRAINT valid_status_v2
    CHECK (status IN ('provisioning_pending', 'provisioning', 'active', 'suspended', 'provisioning_failed', 'deprovisioned'));

-- Comment documenting status values
COMMENT ON COLUMN tenant.status IS 'Lifecycle status: provisioning_pending (awaiting async provisioning), provisioning (in progress), active (ready), suspended, provisioning_failed, deprovisioned';
