-- Step 2: Copy data, drop old column, rename new column.
-- Must be separate from ADD COLUMN because CockroachDB requires new columns
-- to be committed before they can be referenced in DML.

-- Copy existing integer values as strings.
UPDATE manifest_version SET version_new = version::TEXT WHERE version_new IS NULL;

-- Apply NOT NULL constraint after data is copied.
ALTER TABLE manifest_version ALTER COLUMN version_new SET NOT NULL;

-- Drop indexes on the old column.
-- CockroachDB requires DROP INDEX CASCADE for unique constraints.
-- The test adapter (adaptCockroachDDLForPostgres) rewrites this for Postgres.
DROP INDEX IF EXISTS uq_manifest_version_version CASCADE;
DROP INDEX IF EXISTS idx_manifest_version_version;

-- Drop the old integer column.
ALTER TABLE manifest_version DROP COLUMN version;

-- Rename new column to version.
ALTER TABLE manifest_version RENAME COLUMN version_new TO version;

-- Recreate indexes on the renamed column.
ALTER TABLE manifest_version ADD CONSTRAINT uq_manifest_version_version UNIQUE (version);
CREATE INDEX idx_manifest_version_version ON manifest_version (version DESC);
