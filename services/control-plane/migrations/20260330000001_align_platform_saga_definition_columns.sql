-- Align platform_saga_definition with reference-data evolved schema.
--
-- The reference-data service evolved platform_saga_definition through multiple
-- migrations (Jan 2026) adding:
--   - status CHECK constraint (missing from initial migration)
--   - previous_version: version chain linkage for audit trail
--   - valid_from / valid_to: bitemporal tracking for historical replay queries
--
-- The control-plane copy was created in March 2026 without these columns.
-- This migration brings the control-plane copy into schema parity so that
-- both copies of the table have the same structure.
--
-- Constraints and indexes on the new columns are added in the next migration
-- (20260330000002) because CockroachDB cannot create partial indexes on columns
-- added in the same schema change batch.

-- Add CHECK constraint on existing status column (was missing from initial migration).
ALTER TABLE public.platform_saga_definition
  ADD CONSTRAINT chk_platform_saga_definition_status
    CHECK (status IN ('ACTIVE', 'DEPRECATED'));

-- Add previous_version column for explicit version chain linkage.
ALTER TABLE public.platform_saga_definition
  ADD COLUMN IF NOT EXISTS previous_version VARCHAR(16) NULL;

-- Add valid_from: system time when this version became effective.
-- Default to NOW() so existing rows get a sensible baseline timestamp.
ALTER TABLE public.platform_saga_definition
  ADD COLUMN IF NOT EXISTS valid_from TIMESTAMPTZ NOT NULL DEFAULT now();

-- Add valid_to: system time when this version was superseded.
-- NULL means this is the currently active version.
ALTER TABLE public.platform_saga_definition
  ADD COLUMN IF NOT EXISTS valid_to TIMESTAMPTZ NULL;
