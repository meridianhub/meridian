-- Add constraints and indexes for bitemporal tracking columns
--
-- This migration adds a CHECK constraint and index on the valid_from/valid_to
-- columns added in the previous migration (20260129000001).
--
-- Separated into its own migration because CockroachDB cannot create indexes
-- on columns added in the same schema change batch.
--
-- IMPORTANT: This migration runs per-tenant but modifies objects in the shared
-- public schema. All DDL statements must be idempotent to avoid errors when
-- multiple tenant schemas apply the same migration.

-- Check constraint: valid_to must be strictly after valid_from
-- Note: ADD CONSTRAINT IF NOT EXISTS is CockroachDB-only syntax; omitted for PostgreSQL compatibility.
ALTER TABLE public.platform_saga_definition
  ADD CONSTRAINT chk_platform_saga_definition_validity_range
    CHECK (valid_to IS NULL OR valid_to > valid_from);

-- Index for temporal lookups: "which version of saga X was active at time T?"
CREATE INDEX IF NOT EXISTS idx_platform_saga_definition_temporal_lookup
  ON public.platform_saga_definition (name, valid_from, valid_to);

-- Add column comments
COMMENT ON COLUMN public.platform_saga_definition.valid_from IS
  'System time when this version became effective. Set to NOW() on insert.';

COMMENT ON COLUMN public.platform_saga_definition.valid_to IS
  'System time when this version was superseded. NULL means currently active. Set when a newer version is activated.';
