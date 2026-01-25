-- Extend Saga Definition with Platform Reference Support
-- Implements FR-13: Allow tenant sagas to reference platform saga definitions
-- Tenant sagas can either reference platform templates OR provide custom scripts, not both
--
-- Design decisions:
-- - platform_ref: Optional FK to public.platform_saga_definition for template-based sagas
-- - override_reason: Audit trail for why tenant deviated from platform default
-- - platform_version_at_override: Tracks which platform version was active when override was created
-- - Mutual exclusivity: Either platform_ref OR script must be set, never both

-- Add new columns for platform reference support
ALTER TABLE "saga_definition"
  ADD COLUMN "platform_ref" uuid NULL,
  ADD COLUMN "override_reason" text NULL,
  ADD COLUMN "platform_version_at_override" character varying(16) NULL;

-- Add foreign key to platform_saga_definition table in public schema
ALTER TABLE "saga_definition"
  ADD CONSTRAINT "fk_saga_definition_platform_ref"
  FOREIGN KEY ("platform_ref") REFERENCES "public"."platform_saga_definition" ("id")
  ON DELETE SET NULL;

-- Add CHECK constraint: Either platform_ref is set (referencing platform) OR script is set (custom)
-- We must allow the edge case where both are empty (platform_ref=NULL, script='') because:
-- 1. ON DELETE SET NULL can create this state when a platform saga is deleted
-- 2. These orphaned sagas should be handled by application logic (either provide a script or delete)
-- The constraint still enforces that you cannot have BOTH platform_ref AND script set simultaneously
ALTER TABLE "saga_definition"
  ADD CONSTRAINT "chk_saga_definition_platform_or_custom"
  CHECK (NOT ("platform_ref" IS NOT NULL AND "script" != ''));

-- Add index for efficient lookups of sagas by platform reference
-- Used for: "Which tenant sagas reference this platform saga?"
CREATE INDEX "idx_saga_definition_platform_ref" ON "saga_definition" ("platform_ref")
  WHERE "platform_ref" IS NOT NULL;

-- Comment on new columns for clarity
COMMENT ON COLUMN "saga_definition"."platform_ref" IS
  'Optional FK to public.platform_saga_definition. When set, this saga inherits its script from the platform template.';

COMMENT ON COLUMN "saga_definition"."override_reason" IS
  'Audit trail: Why did the tenant create a custom saga instead of using the platform default?';

COMMENT ON COLUMN "saga_definition"."platform_version_at_override" IS
  'Tracks which platform saga version was active when this override was created. Useful for migration impact analysis.';
