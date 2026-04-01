-- Fix schema drift: GORM model defines version as VARCHAR(50) but the
-- original migration (20260209000002) created it as INTEGER.
--
-- Step 1: Add a new VARCHAR column alongside the existing INTEGER column.
-- CockroachDB requires the column to be "public" before it can be referenced
-- in DML, so the UPDATE must be in a separate migration file.

ALTER TABLE manifest_version ADD COLUMN version_new VARCHAR(50);
