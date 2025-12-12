-- Tenant provisioning status table for tracking schema provisioning lifecycle.
-- Stored in 'platform' schema alongside tenants table (shared infrastructure).
--
-- This table tracks the state of org_{tenant_id} schema creation and
-- service migration application for each tenant.
--
-- Design notes:
--  - One row per tenant, created when provisioning starts
--  - service_schemas JSONB stores per-service status for debugging
--  - Idempotent: safe to query and update during retry scenarios
--  - Soft delete via 'deprovisioned' state - records are NEVER hard deleted for audit trail
--  - Schema data retention: deprovisioned schemas are NOT dropped automatically;
--    a separate purge process handles schema deletion after retention period

CREATE TABLE platform.tenant_provisioning (
    -- Foreign key to tenants table (same ID format)
    -- ON DELETE RESTRICT: Cannot delete tenant while provisioning record exists (audit trail)
    tenant_id VARCHAR(50) PRIMARY KEY REFERENCES platform.tenants(id) ON DELETE RESTRICT,

    -- Provisioning lifecycle state
    -- Values: 'pending', 'in_progress', 'active', 'failed', 'deprovisioned'
    -- Note: 'deprovisioned' is a terminal state - schema marked for eventual cleanup
    state VARCHAR(20) NOT NULL DEFAULT 'pending',

    -- Per-service provisioning status
    -- Structure: [{"service_name": "party", "schema_name": "org_acme", "state": "migrated", "migration_version": "20251208...", "error_message": ""}]
    service_schemas JSONB NOT NULL DEFAULT '[]',

    -- Error details if state = 'failed'
    error_message TEXT,

    -- Timestamps
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    deprovisioned_at TIMESTAMPTZ,

    -- Optimistic locking version to prevent concurrent update conflicts
    version INTEGER NOT NULL DEFAULT 1,

    -- Constraints
    CONSTRAINT valid_provisioning_state CHECK (state IN ('pending', 'in_progress', 'active', 'failed', 'deprovisioned')),
    CONSTRAINT deprovisioned_at_required CHECK (
        (state = 'deprovisioned' AND deprovisioned_at IS NOT NULL) OR
        (state != 'deprovisioned' AND deprovisioned_at IS NULL)
    )
);

-- Index for finding tenants by provisioning state (e.g., "find all failed provisioning")
CREATE INDEX idx_tenant_provisioning_state ON platform.tenant_provisioning(state);

-- Index for finding recently updated provisioning (monitoring/debugging)
CREATE INDEX idx_tenant_provisioning_updated_at ON platform.tenant_provisioning(updated_at DESC);

-- Comments for documentation
COMMENT ON TABLE platform.tenant_provisioning IS 'Tracks schema provisioning state for multi-tenant isolation';
COMMENT ON COLUMN platform.tenant_provisioning.tenant_id IS 'Organization ID, references platform.tenants';
COMMENT ON COLUMN platform.tenant_provisioning.state IS 'Provisioning lifecycle: pending → in_progress → active/failed';
COMMENT ON COLUMN platform.tenant_provisioning.service_schemas IS 'JSONB array of per-service provisioning status';
COMMENT ON COLUMN platform.tenant_provisioning.error_message IS 'Error details when state is failed';
