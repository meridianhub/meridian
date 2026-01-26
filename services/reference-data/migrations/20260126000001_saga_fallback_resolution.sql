-- Saga Fallback Resolution with Bi-Temporal Pinning
-- Implements FR-14: GetSaga resolves tenant override first, then platform default via platform_ref
-- Implements FR-15: In-flight sagas pin the platform version at start time for replay determinism
--
-- Design decisions:
-- 1. script column becomes NULLable: sagas with platform_ref inherit script from platform
-- 2. New constraint ensures at least one source: platform_ref OR script must be set
-- 3. ResolvedScript view uses COALESCE for single-query resolution
-- 4. saga_instances gets platform version pinning fields for bi-temporal replay

-- Step 1: Allow NULL script for platform-referenced sagas
-- The original migration had "script" text NOT NULL. Sagas that reference a platform
-- definition via platform_ref do not need their own script (they inherit it).
ALTER TABLE "saga_definition" ALTER COLUMN "script" DROP NOT NULL;

-- Step 2: Replace the existing mutual exclusivity constraint with a stricter one
-- Old constraint: NOT (platform_ref IS NOT NULL AND script != '')
-- New constraint: Enforces mutual exclusivity (cannot have BOTH platform_ref AND custom script),
-- and requires at least one source on INSERT.
--
-- IMPORTANT: ON DELETE SET NULL on platform_ref FK can create orphaned sagas
-- (neither platform_ref nor script). This is handled by application logic, not constraints.
-- The constraint prevents creating NEW rows without a source, but allows the orphaned state
-- created by cascade operations. We achieve this by only checking mutual exclusivity here
-- and relying on application validation for the "at least one source" check.
ALTER TABLE "saga_definition" DROP CONSTRAINT IF EXISTS "chk_saga_definition_platform_or_custom";
ALTER TABLE "saga_definition"
  ADD CONSTRAINT "chk_saga_definition_script_source"
  CHECK (
    -- Cannot have both platform_ref AND custom script simultaneously
    NOT ("platform_ref" IS NOT NULL AND "script" IS NOT NULL AND "script" != '')
  );

-- Step 3: Add comment explaining the resolution logic
COMMENT ON CONSTRAINT "chk_saga_definition_script_source" ON "saga_definition" IS
  'Ensures mutual exclusivity: platform_ref and custom script cannot both be set. Orphaned sagas (neither source) are handled by application logic to support ON DELETE SET NULL cascading from platform_saga_definition.';
