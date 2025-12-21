-- Tenant Provisioning Status Table
-- Normalized per-service tracking for async tenant provisioning
-- Provides queryable/indexable alternative to JSONB service_schemas in tenant_provisioning
-- Uses unqualified table names (relies on database-per-service architecture)

-- Create tenant_provisioning_status table (singular, unqualified)
-- Tracks individual service migration progress during async tenant provisioning
CREATE TABLE IF NOT EXISTS tenant_provisioning_status (
    -- Auto-incrementing primary key
    id SERIAL PRIMARY KEY,

    -- Foreign key to tenant table
    -- ON DELETE RESTRICT: Cannot delete tenant while provisioning records exist (audit trail)
    tenant_id VARCHAR(50) NOT NULL REFERENCES tenant(id) ON DELETE RESTRICT,

    -- Service being provisioned (e.g., 'party', 'account', 'transaction')
    service_name VARCHAR(100) NOT NULL,

    -- Provisioning status for this service
    -- Values: 'pending', 'in_progress', 'completed', 'failed'
    status VARCHAR(50) NOT NULL CHECK (status IN ('pending', 'in_progress', 'completed', 'failed')),

    -- Migration version applied (e.g., '20251216000001')
    migration_version VARCHAR(255),

    -- Error details if status = 'failed'
    error_message TEXT,

    -- Timing metadata
    started_at TIMESTAMPTZ,
    completed_at TIMESTAMPTZ,

    -- Timestamps
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),

    -- Each tenant can only have one status per service
    UNIQUE(tenant_id, service_name)
);

-- Indexes for efficient queries
CREATE INDEX IF NOT EXISTS idx_tenant_provisioning_status_tenant_id
    ON tenant_provisioning_status(tenant_id);
CREATE INDEX IF NOT EXISTS idx_tenant_provisioning_status_status
    ON tenant_provisioning_status(status);
CREATE INDEX IF NOT EXISTS idx_tenant_provisioning_status_service_name
    ON tenant_provisioning_status(service_name);

-- Comments for documentation
COMMENT ON TABLE tenant_provisioning_status IS 'Normalized per-service provisioning status for async tenant provisioning';
COMMENT ON COLUMN tenant_provisioning_status.id IS 'Auto-incrementing primary key';
COMMENT ON COLUMN tenant_provisioning_status.tenant_id IS 'Tenant ID, references tenant table';
COMMENT ON COLUMN tenant_provisioning_status.service_name IS 'Name of the service being provisioned (e.g., party, account)';
COMMENT ON COLUMN tenant_provisioning_status.status IS 'Provisioning status: pending → in_progress → completed/failed';
COMMENT ON COLUMN tenant_provisioning_status.migration_version IS 'Database migration version applied for this service';
COMMENT ON COLUMN tenant_provisioning_status.error_message IS 'Error details when status is failed';
COMMENT ON COLUMN tenant_provisioning_status.started_at IS 'When provisioning started for this service';
COMMENT ON COLUMN tenant_provisioning_status.completed_at IS 'When provisioning completed (success or failure)';
