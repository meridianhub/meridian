-- Step 2: Copy data from integer column and drop the old column.
-- Must be separate from ADD COLUMN (step 1) because CockroachDB requires
-- new columns to be committed before DML can reference them.

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
