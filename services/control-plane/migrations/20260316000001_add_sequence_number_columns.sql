-- Add sequence_number for optimistic locking, plus checksum, source, and resource_path
-- columns to manifest_version table.

ALTER TABLE manifest_version
  ADD COLUMN sequence_number BIGINT NOT NULL DEFAULT 0,
  ADD COLUMN checksum VARCHAR(64),
  ADD COLUMN source VARCHAR(20),
  ADD COLUMN resource_path VARCHAR(255);
