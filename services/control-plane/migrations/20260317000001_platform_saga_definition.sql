-- Platform Saga Definition table for control-plane executor.
-- The executor resolves the apply_manifest saga script from this table.
-- This table is shared across all tenants (platform-level, public schema).
--
-- Note: This table was previously created by reference-data migrations
-- in the reference-data database. The control-plane executor needs it
-- in the platform database (meridian_platform) where its pool connects.

CREATE TABLE IF NOT EXISTS "public"."platform_saga_definition" (
  "id" uuid NOT NULL DEFAULT gen_random_uuid(),
  "name" character varying(64) NOT NULL,
  "version" character varying(16) NOT NULL,
  "script" text NOT NULL,
  "status" character varying(16) NOT NULL DEFAULT 'ACTIVE',
  "display_name" character varying(128) NULL,
  "description" text NULL,
  "created_at" timestamptz NOT NULL DEFAULT now(),
  "updated_at" timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY ("id"),
  CONSTRAINT "chk_platform_saga_definition_version"
    CHECK ("version" ~ '^[0-9]+\.[0-9]+\.[0-9]+$'),
  CONSTRAINT "chk_platform_saga_definition_script_length"
    CHECK (length("script") <= 65536)
);

-- Unique constraint on (name, version) to allow multiple versions.
CREATE UNIQUE INDEX IF NOT EXISTS "uq_platform_saga_definition_name_version"
  ON "public"."platform_saga_definition" ("name", "version");

-- Lookup indexes.
CREATE INDEX IF NOT EXISTS "idx_platform_saga_definition_name"
  ON "public"."platform_saga_definition" ("name");
CREATE INDEX IF NOT EXISTS "idx_platform_saga_definition_updated_at"
  ON "public"."platform_saga_definition" ("updated_at" DESC);
