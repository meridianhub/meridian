-- Fix schema drift: GORM model defines version as VARCHAR(50) but the
-- original migration (20260209000002) created it as INTEGER. This caused
-- bootstrap failures inserting semver strings like "1.0".
--
-- CockroachDB requires ALTER COLUMN TYPE to run outside explicit transactions
-- and cannot alter indexed columns. All indexes on the version column must be
-- dropped first, then recreated after the type change.
-- atlas:txn false

-- Drop all indexes on the version column.
-- CockroachDB requires DROP INDEX CASCADE for unique constraints.
-- The test adapter (adaptCockroachDDLForPostgres) rewrites these for Postgres.
DROP INDEX IF EXISTS uq_manifest_version_version CASCADE;
DROP INDEX IF EXISTS idx_manifest_version_version CASCADE;

-- CockroachDB requires enabling experimental support for int -> varchar type changes.
SET enable_experimental_alter_column_type_general = true;

-- Change the column type from INTEGER to VARCHAR(50) to match the GORM model.
ALTER TABLE manifest_version ALTER COLUMN version SET DATA TYPE VARCHAR(50);

-- Recreate the indexes.
ALTER TABLE manifest_version ADD CONSTRAINT uq_manifest_version_version UNIQUE (version);
CREATE INDEX idx_manifest_version_version ON manifest_version (version DESC);
