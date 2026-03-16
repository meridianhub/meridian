-- Add phase_status column to manifest_versions for per-phase execution tracking.
-- Stores a JSONB object keyed by phase name with status, timestamps, and error info.

ALTER TABLE manifest_versions
  ADD COLUMN phase_status JSONB;
