-- Constraints and indexes for platform_saga_definition schema alignment.
--
-- Adds constraints and partial indexes on the columns added in the previous
-- migration (20260330000001). Separated because CockroachDB cannot create
-- partial indexes on columns added in the same schema change batch.

-- CHECK constraint: previous_version must be semver format or NULL.
ALTER TABLE public.platform_saga_definition
  ADD CONSTRAINT chk_platform_saga_definition_previous_version
    CHECK (previous_version ~ '^[0-9]+\.[0-9]+\.[0-9]+$' OR previous_version IS NULL);

-- CHECK constraint: valid_to must be strictly after valid_from.
ALTER TABLE public.platform_saga_definition
  ADD CONSTRAINT chk_platform_saga_definition_validity_range
    CHECK (valid_to IS NULL OR valid_to > valid_from);

-- Index for finding active versions efficiently.
CREATE INDEX IF NOT EXISTS idx_platform_saga_definition_name_status
  ON public.platform_saga_definition (name, status)
  WHERE status = 'ACTIVE';

-- Index for version chain queries.
CREATE INDEX IF NOT EXISTS idx_platform_saga_definition_previous_version
  ON public.platform_saga_definition (name, previous_version)
  WHERE previous_version IS NOT NULL;

-- Index for point-in-time temporal lookups: "which version was active at time T?"
CREATE INDEX IF NOT EXISTS idx_platform_saga_definition_temporal_lookup
  ON public.platform_saga_definition (name, valid_from, valid_to);
