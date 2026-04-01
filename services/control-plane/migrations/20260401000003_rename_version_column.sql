-- Step 3: Rename the new column and recreate indexes.
-- Must be separate from DROP COLUMN (step 2) because CockroachDB drops
-- columns asynchronously and the old name is not released in the same transaction.

ALTER TABLE manifest_version RENAME COLUMN version_new TO version;

ALTER TABLE manifest_version ADD CONSTRAINT uq_manifest_version_version UNIQUE (version);
CREATE INDEX idx_manifest_version_version ON manifest_version (version DESC);
