-- Remove platform_ref inheritance infrastructure

-- Backfill any NULL scripts before adding NOT NULL constraint.
-- platform_ref sagas that were never backfilled get a placeholder so the
-- migration does not fail. In practice all rows already have scripts
-- (seeder copies content directly), but this guards against edge cases.
UPDATE saga_definition SET script = '' WHERE script IS NULL;

ALTER TABLE saga_definition DROP CONSTRAINT IF EXISTS chk_saga_definition_script_source;
ALTER TABLE saga_definition DROP CONSTRAINT IF EXISTS fk_saga_definition_platform_ref;
DROP INDEX IF EXISTS idx_saga_definition_platform_ref;
ALTER TABLE saga_definition DROP COLUMN IF EXISTS platform_ref;
ALTER TABLE saga_definition DROP COLUMN IF EXISTS override_reason;
ALTER TABLE saga_definition DROP COLUMN IF EXISTS platform_version_at_override;
ALTER TABLE saga_definition ALTER COLUMN script SET NOT NULL;
