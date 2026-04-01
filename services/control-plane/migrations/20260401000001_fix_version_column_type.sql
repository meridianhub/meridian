-- Fix schema drift: GORM model defines version as VARCHAR(50) but the
-- original migration (20260209000002) created it as INTEGER. This caused
-- bootstrap failures inserting semver strings like "1.0".
--
-- CockroachDB requires ALTER COLUMN TYPE to run outside explicit transactions.
-- atlas:txn false

-- Drop the unique constraint before altering the column type.
-- CockroachDB requires DROP INDEX CASCADE for unique constraints.
-- The test adapter (adaptCockroachDDLForPostgres) rewrites this for Postgres.
DROP INDEX IF EXISTS uq_manifest_version_version CASCADE;

-- CockroachDB requires enabling experimental support for int -> varchar type changes.
-- This is a no-op on PostgreSQL (unrecognized variable is silently ignored by the adapter).
SET enable_experimental_alter_column_type_general = true;

-- Change the column type from INTEGER to VARCHAR(50) to match the GORM model.
ALTER TABLE manifest_version ALTER COLUMN version SET DATA TYPE VARCHAR(50);

-- Recreate the unique constraint.
ALTER TABLE manifest_version ADD CONSTRAINT uq_manifest_version_version UNIQUE (version);
