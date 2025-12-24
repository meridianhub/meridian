-- Add updated_at column to tenant table for tracking last modification time
-- GORM's autoUpdateTime will automatically update this field on any UPDATE operation
-- This makes it more accurate than created_at for identifying how long a tenant
-- has been in a particular status (e.g., provisioning_failed)

ALTER TABLE tenant ADD COLUMN updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW();

COMMENT ON COLUMN tenant.updated_at IS 'Timestamp of last update, automatically managed by GORM';

-- Add composite index for status + updated_at queries
-- Optimizes ListByStatusOlderThan queries used by the alert manager
-- to identify tenants stuck in provisioning_failed state for extended periods
--
-- Query pattern: WHERE status = ? AND updated_at < ?
-- This index allows efficient filtering and sorting without full table scans

CREATE INDEX idx_tenant_status_updated_at ON tenant(status, updated_at);
