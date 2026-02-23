-- Fix platform_saga_definition UNIQUE constraint for version retention
--
-- CRITICAL BUG FIX: The previous UNIQUE(name) constraint allowed only one
-- version per saga in the platform_saga_definition table. When PlatformSync
-- encountered a newer embedded version, it executed UPDATE SET script, version,
-- overwriting the previous version's script content in-place. Running saga
-- instances pin PlatformSagaVersionID at execution time, and after UPDATE
-- destroyed the old script, replay operations would fail.
--
-- This migration:
-- 1. Drops the old UNIQUE(name) index
-- 2. Adds a compound UNIQUE INDEX on (name, version) allowing multiple
--    versions of the same saga to coexist
--
-- Combined with the INSERT-only sync logic in PlatformSync, this ensures
-- that old versions are never overwritten and pinned replays remain
-- deterministic.
--
-- IMPORTANT: This migration runs per-tenant but modifies objects in the shared
-- public schema. All DDL statements must be idempotent to avoid errors when
-- multiple tenant schemas apply the same migration.

-- Drop the old UNIQUE(name) constraint to allow multiple versions per saga.
-- PostgreSQL: ALTER TABLE DROP CONSTRAINT is required for constraint-backed unique indexes.
-- CockroachDB used DROP INDEX CASCADE, but that syntax does not work on PostgreSQL
-- for constraint-backed indexes (requires ALTER TABLE instead).
ALTER TABLE public.platform_saga_definition
  DROP CONSTRAINT IF EXISTS uq_platform_saga_definition_name;

-- Add new compound unique index allowing multiple versions per saga
-- Using CREATE UNIQUE INDEX IF NOT EXISTS for idempotency
CREATE UNIQUE INDEX IF NOT EXISTS "uq_platform_saga_definition_name_version"
ON "public"."platform_saga_definition" ("name", "version");

-- Update table comment to reflect new multi-version design
COMMENT ON TABLE "public"."platform_saga_definition" IS
  'Platform-level saga definitions synced from embedded .star files. Multiple versions per saga are retained for deterministic replay of running instances.';
