-- Add slug column to tenant table
-- URL-safe slug for branded API endpoints (e.g., acme → acme.api.meridian.io)
-- Separate from subdomain to support both legacy subdomain routing and new slug-based routing

-- Add slug column
ALTER TABLE tenant ADD COLUMN slug VARCHAR(63);

-- Comment documenting the column's purpose
COMMENT ON COLUMN tenant.slug IS 'URL-safe slug for branded API endpoints (e.g., acme → acme.api.meridian.io)';

-- Unique index on slug with NULL handling
-- Partial index enforces uniqueness only for non-NULL slugs (allows multiple NULLs)
CREATE UNIQUE INDEX idx_tenant_slug ON tenant(slug) WHERE slug IS NOT NULL;

-- Performance index for active tenant slug lookups
-- Optimizes gateway routing queries that filter by slug and active status
CREATE INDEX idx_tenant_slug_active ON tenant(slug) WHERE slug IS NOT NULL AND status = 'active';
