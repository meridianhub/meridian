-- Organizations table for multi-tenant platform
-- Part of BIAN Party Lifecycle Management service domain
-- Organizations are stored in 'platform' schema (shared infrastructure, not tenant-specific)

CREATE SCHEMA IF NOT EXISTS platform;

-- Organizations registry table
CREATE TABLE platform.organizations (
    -- Organization ID (alphanumeric + underscore, 1-50 chars)
    -- Used for schema routing (org_{id} schema) and API subdomain
    id VARCHAR(50) PRIMARY KEY,

    -- Human-readable display name
    display_name VARCHAR(255) NOT NULL,

    -- Primary settlement asset (e.g., GBP, USD, GPU-HOUR, RICE-VOUCHER)
    settlement_asset VARCHAR(20) NOT NULL,

    -- API subdomain (e.g., acme-bank.demo.meridian.io)
    -- Optional - not all organizations need a subdomain
    subdomain VARCHAR(255),

    -- Lifecycle status: active, suspended, deprovisioned
    status VARCHAR(20) NOT NULL DEFAULT 'active',

    -- Timestamps
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    deprovisioned_at TIMESTAMPTZ,

    -- Flexible JSON storage for features, quotas, and org-specific config
    metadata JSONB DEFAULT '{}',

    -- Optimistic locking version for concurrent updates
    version INTEGER NOT NULL DEFAULT 1,

    -- Constraints
    CONSTRAINT valid_status CHECK (status IN ('active', 'suspended', 'deprovisioned')),
    CONSTRAINT valid_org_id CHECK (id ~ '^[a-zA-Z0-9_]{1,50}$')
);

-- Index for filtering by status (common query pattern)
CREATE INDEX idx_organizations_status ON platform.organizations(status);

-- Composite index for filtered + sorted queries (List with status filter)
CREATE INDEX idx_organizations_status_created_at ON platform.organizations(status, created_at DESC);

-- Unique index for subdomain (only when not null)
CREATE UNIQUE INDEX idx_organizations_subdomain ON platform.organizations(subdomain)
    WHERE subdomain IS NOT NULL;

-- Index for listing organizations by creation date (default ordering)
CREATE INDEX idx_organizations_created_at ON platform.organizations(created_at DESC);

-- Comments for documentation
COMMENT ON TABLE platform.organizations IS 'BIAN Party Lifecycle Management - Organization registry for multi-tenant platform';
COMMENT ON COLUMN platform.organizations.id IS 'Unique organization identifier, used for schema routing (org_{id})';
COMMENT ON COLUMN platform.organizations.settlement_asset IS 'Primary asset for this organization (ISO currency code or custom asset)';
COMMENT ON COLUMN platform.organizations.subdomain IS 'API subdomain for organization-specific endpoints';
COMMENT ON COLUMN platform.organizations.metadata IS 'Flexible JSON storage for features, quotas, and org-specific config';
COMMENT ON COLUMN platform.organizations.version IS 'Optimistic locking version for concurrent updates';
