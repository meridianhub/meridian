-- Add phase_status column to manifest_version for per-phase execution tracking.
-- Stores a JSONB object keyed by phase name with status, timestamps, and error info.

ALTER TABLE manifest_version
  ADD COLUMN phase_status JSONB;
