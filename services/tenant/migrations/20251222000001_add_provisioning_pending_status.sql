-- Add provisioning_pending status to tenant check constraint
-- Required for async provisioning workflow where tenants are created with
-- PROVISIONING_PENDING status before background worker handles provisioning

-- Drop existing constraint
ALTER TABLE tenant DROP CONSTRAINT IF EXISTS valid_status;

-- Add updated constraint including provisioning_pending
ALTER TABLE tenant ADD CONSTRAINT valid_status
    CHECK (status IN ('provisioning_pending', 'provisioning', 'active', 'suspended', 'provisioning_failed', 'deprovisioned'));

-- Comment documenting status values
COMMENT ON COLUMN tenant.status IS 'Lifecycle status: provisioning_pending (awaiting async provisioning), provisioning (in progress), active (ready), suspended, provisioning_failed, deprovisioned';
