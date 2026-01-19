-- Migration: Add Shared Dataset Support with Multi-Tenant Hierarchical Lookup
-- This migration enables datasets to be shared across tenants with hierarchical lookup:
-- 1. Tenant-specific schema is queried first
-- 2. If dataset is shared and not found, falls through to master tenant schema
-- 3. Access control via tenant_data_entitlements for RESTRICTED datasets

--------------------------------------------------------------------------------
-- Section 1: Add IsShared and AccessLevel to dataset_definition
--------------------------------------------------------------------------------

-- Add is_shared column: enables hierarchical lookup for this dataset
ALTER TABLE dataset_definition
  ADD COLUMN is_shared BOOLEAN NOT NULL DEFAULT FALSE;

COMMENT ON COLUMN dataset_definition.is_shared IS 'Enables hierarchical lookup: query tenant schema first, then fall through to master tenant if not found';

-- Add access_level column: controls visibility and entitlement requirements
ALTER TABLE dataset_definition
  ADD COLUMN access_level VARCHAR(50) NOT NULL DEFAULT 'PRIVATE';

COMMENT ON COLUMN dataset_definition.access_level IS 'Access control level: PUBLIC (all tenants), PRIVATE (tenant-only), RESTRICTED (requires entitlements)';

-- Ensure access_level has valid values
ALTER TABLE dataset_definition
  ADD CONSTRAINT chk_dataset_definition_access_level
  CHECK (access_level IN ('PUBLIC', 'PRIVATE', 'RESTRICTED'));

-- Index for finding shared datasets
CREATE INDEX idx_dataset_definition_is_shared ON dataset_definition (is_shared) WHERE is_shared = TRUE;

--------------------------------------------------------------------------------
-- Section 2: Create tenant_data_entitlements table
--------------------------------------------------------------------------------

CREATE TABLE tenant_data_entitlements (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  tenant_id VARCHAR(255) NOT NULL,
  dataset_code VARCHAR(255) NOT NULL,
  is_active BOOLEAN NOT NULL DEFAULT TRUE,
  granted_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  expires_at TIMESTAMPTZ NULL,
  created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  created_by VARCHAR(100) NOT NULL DEFAULT 'SYSTEM',
  updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  updated_by VARCHAR(100) NOT NULL DEFAULT 'SYSTEM',
  CONSTRAINT uq_tenant_dataset UNIQUE (tenant_id, dataset_code)
);

COMMENT ON TABLE tenant_data_entitlements IS 'Controls which tenants can access RESTRICTED shared datasets';
COMMENT ON COLUMN tenant_data_entitlements.tenant_id IS 'Tenant ID (matches TenantID type in Go code)';
COMMENT ON COLUMN tenant_data_entitlements.dataset_code IS 'Dataset code (e.g., "FX_RATE")';
COMMENT ON COLUMN tenant_data_entitlements.is_active IS 'Whether entitlement is currently active';
COMMENT ON COLUMN tenant_data_entitlements.expires_at IS 'Optional expiration timestamp for time-limited access';

-- Index for fast entitlement lookups
CREATE INDEX idx_entitlements_tenant_dataset
  ON tenant_data_entitlements(tenant_id, dataset_code, is_active)
  WHERE is_active = TRUE;

-- Index for expiration checks
CREATE INDEX idx_entitlements_expires_at
  ON tenant_data_entitlements(expires_at)
  WHERE expires_at IS NOT NULL AND is_active = TRUE;

--------------------------------------------------------------------------------
-- Section 3: Mark ECB FX_RATE dataset as shared/public
--------------------------------------------------------------------------------

-- Update ECB FX_RATE dataset to be shared and public
-- This allows all tenants to access ECB rates without per-tenant ingestion
UPDATE dataset_definition
SET
  is_shared = TRUE,
  access_level = 'PUBLIC',
  updated_at = NOW(),
  updated_by = 'MIGRATION'
WHERE code LIKE 'ECB_%_FX'
  AND status = 'ACTIVE'
  AND deleted_at IS NULL;

-- Note: No need to populate tenant_data_entitlements for PUBLIC datasets
-- PUBLIC datasets skip entitlement checks and allow access to all tenants
