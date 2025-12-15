-- Tenant Service Schema
-- Platform infrastructure for multi-tenant isolation
-- Consolidated migration for clean slate deployment

CREATE SCHEMA IF NOT EXISTS platform;

-- Tenants registry table
CREATE TABLE platform.tenants (
    -- Tenant ID (alphanumeric + underscore, 1-50 chars)
    -- Used for schema routing (org_{id} schema) and API subdomain
    id VARCHAR(50) PRIMARY KEY,

    -- Human-readable display name
    display_name VARCHAR(255) NOT NULL,

    -- Primary settlement asset (e.g., GBP, USD, GPU-HOUR, RICE-VOUCHER)
    settlement_asset VARCHAR(20) NOT NULL,

    -- API subdomain (e.g., acme-bank.demo.meridian.io)
    -- Optional - not all tenants need a subdomain
    subdomain VARCHAR(255),

    -- Party reference (external - Party Service via gRPC)
    -- Links platform infrastructure to BIAN domain entities (Party.Organization)
    party_id VARCHAR(100),

    -- Lifecycle status: provisioning, active, suspended, provisioning_failed, deprovisioned
    status VARCHAR(20) NOT NULL DEFAULT 'provisioning',

    -- Error details if status = 'provisioning_failed'
    error_message TEXT,

    -- Timestamps
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    deprovisioned_at TIMESTAMPTZ,

    -- Flexible JSON storage for features, quotas, and tenant-specific config
    metadata JSONB DEFAULT '{}',

    -- Optimistic locking version for concurrent updates
    version INTEGER NOT NULL DEFAULT 1,

    -- Constraints
    -- Note: tenant_id format validation (alphanumeric + underscore) done at application layer
    CONSTRAINT valid_status CHECK (status IN ('provisioning', 'active', 'suspended', 'provisioning_failed', 'deprovisioned'))
);

-- Indexes for tenants
CREATE INDEX idx_tenants_status ON platform.tenants(status);
CREATE INDEX idx_tenants_status_created_at ON platform.tenants(status, created_at DESC);
CREATE UNIQUE INDEX idx_tenants_subdomain ON platform.tenants(subdomain) WHERE subdomain IS NOT NULL;
CREATE INDEX idx_tenants_created_at ON platform.tenants(created_at DESC);
CREATE INDEX idx_tenants_party_id ON platform.tenants(party_id);

-- Comments for tenants
COMMENT ON TABLE platform.tenants IS 'Multi-tenant platform registry';
COMMENT ON COLUMN platform.tenants.id IS 'Unique tenant identifier, used for schema routing (org_{id})';
COMMENT ON COLUMN platform.tenants.settlement_asset IS 'Primary asset for this tenant (ISO currency code or custom asset)';
COMMENT ON COLUMN platform.tenants.subdomain IS 'API subdomain for tenant-specific endpoints';
COMMENT ON COLUMN platform.tenants.party_id IS 'Reference to corresponding Party in BIAN Party Reference Data Directory (auto-populated on tenant creation)';
COMMENT ON COLUMN platform.tenants.metadata IS 'Flexible JSON storage for features, quotas, and tenant-specific config';
COMMENT ON COLUMN platform.tenants.version IS 'Optimistic locking version for concurrent updates';
COMMENT ON COLUMN platform.tenants.error_message IS 'Error details when status is provisioning_failed';

-- Tenant provisioning status table
CREATE TABLE platform.tenant_provisioning (
    -- Foreign key to tenants table
    -- ON DELETE RESTRICT: Cannot delete tenant while provisioning record exists (audit trail)
    tenant_id VARCHAR(50) PRIMARY KEY REFERENCES platform.tenants(id) ON DELETE RESTRICT,

    -- Provisioning lifecycle state
    -- Values: 'pending', 'in_progress', 'active', 'failed', 'deprovisioned'
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

    -- Optimistic locking version
    version INTEGER NOT NULL DEFAULT 1,

    -- Constraints
    CONSTRAINT valid_provisioning_state CHECK (state IN ('pending', 'in_progress', 'active', 'failed', 'deprovisioned')),
    CONSTRAINT deprovisioned_at_required CHECK (
        (state = 'deprovisioned' AND deprovisioned_at IS NOT NULL) OR
        (state != 'deprovisioned' AND deprovisioned_at IS NULL)
    )
);

-- Indexes for tenant_provisioning
CREATE INDEX idx_tenant_provisioning_state ON platform.tenant_provisioning(state);
CREATE INDEX idx_tenant_provisioning_updated_at ON platform.tenant_provisioning(updated_at DESC);

-- Comments for tenant_provisioning
COMMENT ON TABLE platform.tenant_provisioning IS 'Tracks schema provisioning state for multi-tenant isolation';
COMMENT ON COLUMN platform.tenant_provisioning.tenant_id IS 'Tenant ID, references platform.tenants';
COMMENT ON COLUMN platform.tenant_provisioning.state IS 'Provisioning lifecycle: pending → in_progress → active/failed';
COMMENT ON COLUMN platform.tenant_provisioning.service_schemas IS 'JSONB array of per-service provisioning status';
COMMENT ON COLUMN platform.tenant_provisioning.error_message IS 'Error details when state is failed';
