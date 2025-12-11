-- Rename platform.organizations table to platform.tenants
-- This distinguishes platform tenants (infrastructure concept) from BIAN Party.Organization entities

-- Rename the table
ALTER TABLE platform.organizations RENAME TO tenants;

-- Rename indexes to match new table name
ALTER INDEX platform.idx_organizations_status RENAME TO idx_tenants_status;
ALTER INDEX platform.idx_organizations_status_created_at RENAME TO idx_tenants_status_created_at;
ALTER INDEX platform.idx_organizations_subdomain RENAME TO idx_tenants_subdomain;
ALTER INDEX platform.idx_organizations_created_at RENAME TO idx_tenants_created_at;

-- Update table comment
COMMENT ON TABLE platform.tenants IS 'Tenant registry for multi-tenant platform (infrastructure concept, distinct from BIAN Party.Organization)';
COMMENT ON COLUMN platform.tenants.id IS 'Unique tenant identifier, used for schema routing (org_{id})';
COMMENT ON COLUMN platform.tenants.settlement_asset IS 'Primary asset for this tenant (ISO currency code or custom asset)';
COMMENT ON COLUMN platform.tenants.subdomain IS 'API subdomain for tenant-specific endpoints';
COMMENT ON COLUMN platform.tenants.metadata IS 'Flexible JSON storage for features, quotas, and tenant-specific config';
COMMENT ON COLUMN platform.tenants.version IS 'Optimistic locking version for concurrent updates';
