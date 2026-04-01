-- Fix schema drift: GORM model defines version as VARCHAR(50) but the
-- original migration (20260209000002) created it as INTEGER. This caused
-- bootstrap failures inserting semver strings like "1.0".
--
-- CockroachDB cannot ALTER COLUMN TYPE inside a transaction, so we use the
-- add-column/copy/drop/rename pattern instead.

-- Step 1: Add a new VARCHAR column.
ALTER TABLE manifest_version ADD COLUMN version_new VARCHAR(50);

-- Step 2: Copy existing integer values as strings.
UPDATE manifest_version SET version_new = version::TEXT;

-- Step 3: Apply NOT NULL after data is copied.
ALTER TABLE manifest_version ALTER COLUMN version_new SET NOT NULL;

-- Step 4: Drop old indexes and the old column.
-- CockroachDB requires DROP INDEX CASCADE for unique constraints.
-- The test adapter (adaptCockroachDDLForPostgres) rewrites these for Postgres.
DROP INDEX IF EXISTS uq_manifest_version_version CASCADE;
DROP INDEX IF EXISTS idx_manifest_version_version;
ALTER TABLE manifest_version DROP COLUMN version;

-- Step 5: Rename the new column.
ALTER TABLE manifest_version RENAME COLUMN version_new TO version;

-- Step 6: Recreate indexes on the new column.
ALTER TABLE manifest_version ADD CONSTRAINT uq_manifest_version_version UNIQUE (version);
CREATE INDEX idx_manifest_version_version ON manifest_version (version DESC);
