-- Platform Saga Definition Storage
-- This table stores platform-level saga definitions that serve as defaults/templates
-- for tenant schemas. Unlike saga_definition (per-tenant), this is in the PUBLIC schema.
--
-- Design decisions:
-- - PUBLIC schema: Shared across all tenants (platform-level)
-- - Semver versioning: Uses X.Y.Z format (not integer) for proper version comparison
-- - Simpler lifecycle: No DRAFT/ACTIVE/DEPRECATED, just latest version wins
-- - Embedded sync: Populated from embedded .star files at service startup
--
-- IMPORTANT: This migration runs per-tenant but creates objects in the shared public schema.
-- All DDL statements must be idempotent (IF NOT EXISTS) to avoid errors when multiple
-- tenant schemas apply the same migration.

-- Create "platform_saga_definition" table in public schema with all constraints inline
-- Using IF NOT EXISTS because this migration runs per-tenant but creates in shared public schema
CREATE TABLE IF NOT EXISTS "public"."platform_saga_definition" (
  "id" uuid NOT NULL DEFAULT gen_random_uuid(),
  "name" character varying(64) NOT NULL,
  "version" character varying(16) NOT NULL,
  "script" text NOT NULL,
  "display_name" character varying(128) NULL,
  "description" text NULL,
  "created_at" timestamptz NOT NULL DEFAULT now(),
  "updated_at" timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY ("id"),
  -- Semver format: X.Y.Z where X, Y, Z are non-negative integers
  CONSTRAINT "chk_platform_saga_definition_version"
    CHECK ("version" ~ '^[0-9]+\.[0-9]+\.[0-9]+$'),
  -- Limit script to 64KB to prevent abuse
  CONSTRAINT "chk_platform_saga_definition_script_length"
    CHECK (length("script") <= 65536),
  -- Ensure name is unique (only one version per name at a time)
  -- Unlike tenant sagas which can have multiple versions, platform sagas
  -- are always synced to the latest embedded version
  CONSTRAINT "uq_platform_saga_definition_name"
    UNIQUE ("name")
);

-- Create indexes for efficient lookups (idempotent)
-- Primary lookup by name
CREATE INDEX IF NOT EXISTS "idx_platform_saga_definition_name" ON "public"."platform_saga_definition" ("name");

-- Index for listing all platform sagas by updated time
CREATE INDEX IF NOT EXISTS "idx_platform_saga_definition_updated_at" ON "public"."platform_saga_definition" ("updated_at" DESC);

-- Comment on the table (idempotent - replaces if exists)
COMMENT ON TABLE "public"."platform_saga_definition" IS
  'Platform-level saga definitions synced from embedded .star files. Serves as source for tenant saga seeding.';
