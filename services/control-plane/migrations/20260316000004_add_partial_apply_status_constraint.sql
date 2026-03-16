-- Recreate the apply_status CHECK constraint with PARTIAL included.
-- This must be in a separate migration from the DROP because CockroachDB
-- cannot drop and recreate a constraint with the same name in one transaction.

ALTER TABLE manifest_versions ADD CONSTRAINT valid_apply_status
  CHECK (apply_status IN ('APPLIED', 'FAILED', 'ROLLED_BACK', 'PARTIAL'));
