-- Add bitemporal tracking columns to platform_saga_definition
--
-- This migration adds system-time temporal columns (valid_from, valid_to) to
-- platform_saga_definition for historical replay and audit queries.
--
-- The temporal columns complement the existing status/version columns:
-- - status (ACTIVE/DEPRECATED) controls which version is used for NEW executions
-- - valid_from/valid_to records WHEN each version was the active version
--
-- This enables point-in-time queries: "Which version was active at time T?"
-- which is essential for audit trails, regulatory reporting, and replay analysis.
--
-- IMPORTANT: This migration runs per-tenant but modifies objects in the shared
-- public schema. All DDL statements must be idempotent to avoid errors when
-- multiple tenant schemas apply the same migration.
--
-- NOTE: Constraints and indexes on the new columns are added in the next
-- migration (20260129000002) because CockroachDB cannot create partial indexes
-- on columns added in the same schema change batch.

-- Add valid_from column: when this version became effective
-- Default to NOW() so existing rows get a sensible baseline
ALTER TABLE public.platform_saga_definition
  ADD COLUMN IF NOT EXISTS valid_from TIMESTAMPTZ NOT NULL DEFAULT now();

-- Add valid_to column: when this version ceased being the active version
-- NULL means this is the currently active version
ALTER TABLE public.platform_saga_definition
  ADD COLUMN IF NOT EXISTS valid_to TIMESTAMPTZ NULL;
