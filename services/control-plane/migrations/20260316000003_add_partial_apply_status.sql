-- Drop the existing apply_status CHECK constraint to allow adding PARTIAL.
-- CockroachDB cannot drop and recreate a constraint with the same name
-- in a single transaction, so the new constraint is added in a separate file.

ALTER TABLE manifest_version DROP CONSTRAINT valid_apply_status;
