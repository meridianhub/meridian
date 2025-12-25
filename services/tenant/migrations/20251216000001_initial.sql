-- Tenant Service Schema
-- Platform infrastructure for multi-tenant isolation
-- Consolidated migration for clean slate deployment
-- Uses unqualified table names (relies on database-per-service architecture)

-- =============================================================================
-- TENANT REGISTRY
-- =============================================================================

-- Tenant registry table (singular, unqualified)
CREATE TABLE tenant (
    -- Tenant ID (alphanumeric + underscore, 1-50 chars)
    -- Used for schema routing (org_{id} schema) and API subdomain
    id VARCHAR(50) PRIMARY KEY,

    -- Human-readable display name
    display_name VARCHAR(255) NOT NULL,

    -- Primary settlement asset (e.g., GBP, USD, GPU-HOUR, RICE-VOUCHER)
    settlement_asset VARCHAR(20) NOT NULL,

    -- API subdomain (e.g., acme-bank.demo.meridianhub.cloud)
    -- Optional - not all tenants need a subdomain
    subdomain VARCHAR(255),

    -- URL-safe slug for branded API endpoints (e.g., acme → acme.api.meridianhub.cloud)
    -- Separate from subdomain to support both legacy subdomain routing and new slug-based routing
    slug VARCHAR(63),

    -- Party reference (external - Party Service via gRPC)
    -- Links platform infrastructure to BIAN domain entities (Party.Organization)
    party_id VARCHAR(100),

    -- Lifecycle status
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
    CONSTRAINT valid_status CHECK (status IN ('provisioning_pending', 'provisioning', 'active', 'suspended', 'provisioning_failed', 'deprovisioned'))
);

-- Indexes for tenant
CREATE INDEX idx_tenant_status ON tenant(status);
CREATE INDEX idx_tenant_status_created_at ON tenant(status, created_at DESC);
CREATE UNIQUE INDEX idx_tenant_subdomain ON tenant(subdomain) WHERE subdomain IS NOT NULL;
CREATE INDEX idx_tenant_created_at ON tenant(created_at DESC);
CREATE INDEX idx_tenant_party_id ON tenant(party_id);
CREATE UNIQUE INDEX idx_tenant_slug ON tenant(slug) WHERE slug IS NOT NULL;
CREATE INDEX idx_tenant_slug_active ON tenant(slug) WHERE slug IS NOT NULL AND status = 'active';

-- Comments for tenant
COMMENT ON TABLE tenant IS 'Multi-tenant platform registry';
COMMENT ON COLUMN tenant.id IS 'Unique tenant identifier, used for schema routing (org_{id})';
COMMENT ON COLUMN tenant.settlement_asset IS 'Primary asset for this tenant (ISO currency code or custom asset)';
COMMENT ON COLUMN tenant.subdomain IS 'API subdomain for tenant-specific endpoints';
COMMENT ON COLUMN tenant.slug IS 'URL-safe slug for branded API endpoints (e.g., acme → acme.api.meridianhub.cloud)';
COMMENT ON COLUMN tenant.party_id IS 'Reference to corresponding Party in BIAN Party Reference Data Directory (auto-populated on tenant creation)';
COMMENT ON COLUMN tenant.metadata IS 'Flexible JSON storage for features, quotas, and tenant-specific config';
COMMENT ON COLUMN tenant.version IS 'Optimistic locking version for concurrent updates';
COMMENT ON COLUMN tenant.error_message IS 'Error details when status is provisioning_failed';
COMMENT ON COLUMN tenant.status IS 'Lifecycle status: provisioning_pending (awaiting async provisioning), provisioning (in progress), active (ready), suspended, provisioning_failed, deprovisioned';

-- =============================================================================
-- TENANT PROVISIONING
-- =============================================================================

-- Tenant provisioning status table (singular, unqualified)
CREATE TABLE tenant_provisioning (
    -- Foreign key to tenant table
    -- ON DELETE RESTRICT: Cannot delete tenant while provisioning record exists (audit trail)
    tenant_id VARCHAR(50) PRIMARY KEY REFERENCES tenant(id) ON DELETE RESTRICT,

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
CREATE INDEX idx_tenant_provisioning_state ON tenant_provisioning(state);
CREATE INDEX idx_tenant_provisioning_updated_at ON tenant_provisioning(updated_at DESC);

-- Comments for tenant_provisioning
COMMENT ON TABLE tenant_provisioning IS 'Tracks schema provisioning state for multi-tenant isolation';
COMMENT ON COLUMN tenant_provisioning.tenant_id IS 'Tenant ID, references tenant table';
COMMENT ON COLUMN tenant_provisioning.state IS 'Provisioning lifecycle: pending → in_progress → active/failed';
COMMENT ON COLUMN tenant_provisioning.service_schemas IS 'JSONB array of per-service provisioning status';
COMMENT ON COLUMN tenant_provisioning.error_message IS 'Error details when state is failed';

-- =============================================================================
-- TENANT PROVISIONING STATUS (per-service tracking)
-- =============================================================================

-- Create tenant_provisioning_status table (singular, unqualified)
-- Tracks individual service migration progress during async tenant provisioning
CREATE TABLE tenant_provisioning_status (
    -- Auto-incrementing primary key
    id SERIAL PRIMARY KEY,

    -- Foreign key to tenant table
    tenant_id VARCHAR(50) NOT NULL REFERENCES tenant(id) ON DELETE RESTRICT,

    -- Service being provisioned (e.g., 'party', 'account', 'transaction')
    service_name VARCHAR(100) NOT NULL,

    -- Provisioning status for this service
    status VARCHAR(50) NOT NULL CHECK (status IN ('pending', 'in_progress', 'completed', 'failed')),

    -- Migration version applied (e.g., '20251216000001')
    migration_version VARCHAR(255),

    -- Error details if status = 'failed'
    error_message TEXT,

    -- Retry tracking for worker processing patterns
    retry_count INTEGER NOT NULL DEFAULT 0,

    -- Timing metadata
    started_at TIMESTAMPTZ,
    completed_at TIMESTAMPTZ,

    -- Timestamps
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),

    -- Each tenant can only have one status per service
    UNIQUE(tenant_id, service_name),

    -- Data integrity constraint: completed status must have migration_version
    CONSTRAINT migration_version_required_when_completed CHECK (
        (status = 'completed' AND migration_version IS NOT NULL) OR
        status != 'completed'
    )
);

-- Indexes for tenant_provisioning_status
CREATE INDEX idx_tenant_provisioning_status_tenant_id ON tenant_provisioning_status(tenant_id);
CREATE INDEX idx_tenant_provisioning_status_status ON tenant_provisioning_status(status);
CREATE INDEX idx_tenant_provisioning_status_service_name ON tenant_provisioning_status(service_name);
CREATE INDEX idx_tenant_provisioning_status_status_created_at ON tenant_provisioning_status(status, created_at);
CREATE INDEX idx_tenant_provisioning_status_tenant_status ON tenant_provisioning_status(tenant_id, status);

-- Comments for tenant_provisioning_status
COMMENT ON TABLE tenant_provisioning_status IS 'Normalized per-service provisioning status for indexed queries and worker processing';
COMMENT ON COLUMN tenant_provisioning_status.tenant_id IS 'Tenant ID, references tenant table';
COMMENT ON COLUMN tenant_provisioning_status.service_name IS 'Name of the service being provisioned (e.g., party, account)';
COMMENT ON COLUMN tenant_provisioning_status.status IS 'Provisioning status: pending → in_progress → completed/failed';
COMMENT ON COLUMN tenant_provisioning_status.migration_version IS 'Database migration version applied for this service';
COMMENT ON COLUMN tenant_provisioning_status.retry_count IS 'Number of retry attempts for exponential backoff';

-- =============================================================================
-- AUDIT SYSTEM
-- =============================================================================

-- Create audit_log table (unqualified, singular)
CREATE TABLE audit_log (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),

    -- What changed
    table_name VARCHAR(100) NOT NULL,
    operation VARCHAR(10) NOT NULL CHECK (operation IN ('INSERT', 'UPDATE', 'DELETE')),

    -- Record identification (string ID for tenant compatibility)
    record_id VARCHAR(50) NOT NULL,

    -- Change metadata
    changed_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    changed_by VARCHAR(100),

    -- Change details
    old_values JSONB,
    new_values JSONB,

    -- Additional context
    transaction_id VARCHAR(100),
    client_ip INET,
    user_agent TEXT
);

-- Create indexes for efficient audit queries
CREATE INDEX idx_audit_log_table_name ON audit_log(table_name);
CREATE INDEX idx_audit_log_record_id ON audit_log(record_id);
CREATE INDEX idx_audit_log_changed_at ON audit_log(changed_at);
CREATE INDEX idx_audit_log_changed_by ON audit_log(changed_by);
CREATE INDEX idx_audit_log_operation ON audit_log(operation);

-- Create audit_outbox table for async processing (unqualified, singular)
CREATE TABLE audit_outbox (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),

    -- What changed
    table_name VARCHAR(100) NOT NULL,
    operation VARCHAR(10) NOT NULL CHECK (operation IN ('INSERT', 'UPDATE', 'DELETE')),

    -- Record identification (string ID for tenant compatibility)
    record_id VARCHAR(50) NOT NULL,

    -- Change details
    old_values JSONB,
    new_values JSONB,

    -- Processing status
    status VARCHAR(20) NOT NULL DEFAULT 'pending' CHECK (status IN ('pending', 'processing', 'completed', 'failed')),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    retry_count INT NOT NULL DEFAULT 0,
    last_error TEXT,

    -- Additional context
    changed_by VARCHAR(100),
    transaction_id VARCHAR(100),
    client_ip INET,
    user_agent TEXT
);

-- Index for worker to efficiently find pending entries
CREATE INDEX idx_audit_outbox_status_created ON audit_outbox(status, created_at);

-- Create helper view for easy audit queries
CREATE OR REPLACE VIEW change_summary AS
SELECT
    id,
    table_name AS table_full_name,
    operation,
    record_id,
    changed_at,
    changed_by,
    CASE
        WHEN operation = 'UPDATE' THEN
            COALESCE(
                (SELECT json_object_agg(key, value)
                 FROM jsonb_each(new_values)
                 WHERE new_values->key IS DISTINCT FROM old_values->key),
                '{}'::json
            )
        ELSE NULL
    END AS changed_fields,
    transaction_id
FROM audit_log
ORDER BY changed_at DESC;
