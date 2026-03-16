-- Add PARTIAL to the apply_status CHECK constraint on manifest_versions.
-- Separate migration because CockroachDB cannot alter CHECK constraints
-- in the same transaction as column additions.

ALTER TABLE manifest_versions DROP CONSTRAINT valid_apply_status;
ALTER TABLE manifest_versions ADD CONSTRAINT valid_apply_status
  CHECK (apply_status IN ('APPLIED', 'FAILED', 'ROLLED_BACK', 'PARTIAL'));
