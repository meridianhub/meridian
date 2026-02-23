-- Add constraints and indexes for versioned platform saga lifecycle columns
--
-- This migration adds CHECK constraints and partial indexes on the status and
-- previous_version columns added in the previous migration (20260128000001).
--
-- Separated into its own migration because CockroachDB cannot create partial
-- indexes on columns added in the same schema change batch.
--
-- IMPORTANT: This migration runs per-tenant but modifies objects in the shared
-- public schema. All DDL statements must be idempotent to avoid errors when
-- multiple tenant schemas apply the same migration.

-- Add CHECK constraints for data integrity
-- Note: ADD CONSTRAINT IF NOT EXISTS is CockroachDB-only syntax; omitted for PostgreSQL compatibility.
ALTER TABLE public.platform_saga_definition
  ADD CONSTRAINT chk_platform_saga_definition_status
    CHECK (status IN ('ACTIVE', 'DEPRECATED'));

ALTER TABLE public.platform_saga_definition
  ADD CONSTRAINT chk_platform_saga_definition_previous_version
    CHECK (previous_version ~ '^[0-9]+\.[0-9]+\.[0-9]+$' OR previous_version IS NULL);

-- Create index for finding active versions efficiently
CREATE INDEX IF NOT EXISTS idx_platform_saga_definition_name_status
  ON public.platform_saga_definition(name, status)
  WHERE status = 'ACTIVE';

-- Create index for version chain queries
CREATE INDEX IF NOT EXISTS idx_platform_saga_definition_previous_version
  ON public.platform_saga_definition(name, previous_version)
  WHERE previous_version IS NOT NULL;

-- Add column comments
COMMENT ON COLUMN public.platform_saga_definition.status IS
  'Lifecycle status: ACTIVE (latest, used for new sagas) or DEPRECATED (kept for replay of pinned instances)';

COMMENT ON COLUMN public.platform_saga_definition.previous_version IS
  'Links to previous version in the version chain for audit trail and migration analysis';
