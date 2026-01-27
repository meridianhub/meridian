-- Add status and previous_version columns for versioned platform saga lifecycle
--
-- This migration adds lifecycle management columns to platform_saga_definition:
-- - status: ACTIVE (latest, used for new sagas) or DEPRECATED (kept for replay)
-- - previous_version: Links to previous version for audit trail
--
-- Combined with per-saga versioned directories and INSERT-only sync,
-- this enables deterministic replay of running saga instances that pin
-- PlatformSagaVersionID at execution time.
--
-- IMPORTANT: This migration runs per-tenant but modifies objects in the shared
-- public schema. All DDL statements must be idempotent to avoid errors when
-- multiple tenant schemas apply the same migration.
--
-- NOTE: CockroachDB does not support PL/pgSQL DO $$ blocks. Uses
-- ADD COLUMN IF NOT EXISTS for idempotency instead.
--
-- NOTE: Indexes and constraints on the new columns are added in the next
-- migration (20260128000002) because CockroachDB cannot create partial indexes
-- on columns added in the same schema change batch.

-- Add status column for lifecycle management
-- Default to ACTIVE so existing rows (which are the current active versions) are correct
ALTER TABLE public.platform_saga_definition
  ADD COLUMN IF NOT EXISTS status VARCHAR(16) NOT NULL DEFAULT 'ACTIVE';

-- Add previous_version column for explicit version chain linkage
ALTER TABLE public.platform_saga_definition
  ADD COLUMN IF NOT EXISTS previous_version VARCHAR(16) NULL;

-- Update table comment
COMMENT ON TABLE public.platform_saga_definition IS
  'Platform-level saga definitions synced from embedded .star files. Multiple versions per saga are retained with ACTIVE/DEPRECATED lifecycle for deterministic replay.';
