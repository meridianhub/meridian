-- Tenant Provisioning Status Table
-- Normalized per-service tracking for async tenant provisioning
-- Uses unqualified table names (relies on database-per-service architecture)
--
-- MIGRATION ORDERING NOTE:
-- This migration (20251218) was intentionally inserted between existing migrations
-- (20251217 audit_system and 20251220 add_slug). Atlas handles non-sequential
-- timestamp insertion correctly as long as the sum file is regenerated.
--
-- PURPOSE:
-- This table denormalizes the per-service provisioning data that was previously stored
-- only in tenant_provisioning.service_schemas (JSONB). While service_schemas contains
-- a full snapshot of all service states as a JSON array, this table provides:
--   1. Indexable columns for efficient queries (status, service_name, tenant_id)
--   2. Direct SQL updates without JSON manipulation
--   3. Row-level locking for concurrent worker access
--
-- RELATIONSHIP TO tenant_provisioning:
-- The tenant_provisioning table (see 20251216000001_initial.sql) tracks overall
-- provisioning state with service_schemas as a JSONB blob:
--   {"service_name": "party", "schema_name": "org_acme", "state": "migrated", ...}
-- This table mirrors that data in normalized form for operational queries.
-- Both are updated together - service_schemas remains the source of truth for
-- the PostgresProvisioner (see postgres_provisioner.go:960 provisioningEntity struct),
-- while this table enables efficient worker processing and status reporting.
--
-- KEY USE CASES:
--   - Partial failure recovery: Query failed services without parsing JSONB
--   - Status reporting: Fast aggregation of per-service status across tenants
--   - Worker job processing: Workers can claim pending services with row-level locks
--   - Retry logic: Track started_at/completed_at for timeout detection

-- Create tenant_provisioning_status table (singular, unqualified)
-- Tracks individual service migration progress during async tenant provisioning
CREATE TABLE IF NOT EXISTS tenant_provisioning_status (
    -- Auto-incrementing primary key
    id SERIAL PRIMARY KEY,

    -- Foreign key to tenant table
    -- ON DELETE RESTRICT: Cannot delete tenant while provisioning records exist (audit trail)
    --
    -- FK DESIGN CHOICE: References tenant(id) directly, not tenant_provisioning(tenant_id).
    -- This is intentional because:
    --   1. tenant_provisioning_status records may exist before tenant_provisioning is created
    --      (e.g., during initial provisioning setup before the overall status record exists)
    --   2. Simpler FK chain: tenant_provisioning_status → tenant (vs → tenant_provisioning → tenant)
    --   3. The application layer (ProvisioningService) ensures consistency between both tables
    tenant_id VARCHAR(50) NOT NULL REFERENCES tenant(id) ON DELETE RESTRICT,

    -- Service being provisioned (e.g., 'party', 'account', 'transaction')
    service_name VARCHAR(100) NOT NULL,

    -- Provisioning status for this service
    -- Values: 'pending', 'in_progress', 'completed', 'failed'
    --
    -- STATUS VALUES NOTE: These differ from tenant_provisioning.state which uses:
    --   'pending', 'in_progress', 'active', 'failed', 'deprovisioned'
    -- The difference is intentional:
    --   - This table tracks per-service migration status: 'completed' = service migration done
    --   - tenant_provisioning tracks overall tenant lifecycle: 'active' = tenant fully provisioned
    --   - 'deprovisioned' doesn't apply here (individual services aren't deprovisioned separately)
    status VARCHAR(50) NOT NULL CHECK (status IN ('pending', 'in_progress', 'completed', 'failed')),

    -- Migration version applied (e.g., '20251216000001')
    migration_version VARCHAR(255),

    -- Error details if status = 'failed'
    error_message TEXT,

    -- Retry tracking for worker processing patterns (exponential backoff, circuit breaker)
    retry_count INTEGER NOT NULL DEFAULT 0,

    -- Timing metadata
    started_at TIMESTAMPTZ,
    completed_at TIMESTAMPTZ,

    -- Timestamps
    -- Note: updated_at uses DEFAULT NOW() without a trigger, consistent with tenant_provisioning
    -- table (see 20251216000001_initial.sql). Application layer is responsible for setting
    -- updated_at on UPDATE operations. This avoids trigger complexity and aligns with Go's
    -- sqlc pattern where we explicitly set all modified fields.
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

-- Composite index for worker claiming pattern:
-- SELECT ... WHERE status = 'pending' ORDER BY created_at LIMIT 1 FOR UPDATE SKIP LOCKED
CREATE INDEX IF NOT EXISTS idx_tenant_provisioning_status_status_created_at
    ON tenant_provisioning_status(status, created_at);

-- Comments for documentation
COMMENT ON TABLE tenant_provisioning_status IS 'Normalized per-service provisioning status. Denormalizes tenant_provisioning.service_schemas for indexed queries, worker processing, and partial failure recovery.';
COMMENT ON COLUMN tenant_provisioning_status.id IS 'Auto-incrementing primary key';
COMMENT ON COLUMN tenant_provisioning_status.tenant_id IS 'Tenant ID, references tenant table';
COMMENT ON COLUMN tenant_provisioning_status.service_name IS 'Name of the service being provisioned (e.g., party, account)';
COMMENT ON COLUMN tenant_provisioning_status.status IS 'Provisioning status: pending → in_progress → completed/failed';
COMMENT ON COLUMN tenant_provisioning_status.migration_version IS 'Database migration version applied for this service';
COMMENT ON COLUMN tenant_provisioning_status.error_message IS 'Error details when status is failed';
COMMENT ON COLUMN tenant_provisioning_status.retry_count IS 'Number of retry attempts for exponential backoff and circuit breaker patterns';
COMMENT ON COLUMN tenant_provisioning_status.started_at IS 'When provisioning started for this service';
COMMENT ON COLUMN tenant_provisioning_status.completed_at IS 'When provisioning completed (success or failure)';
