-- Fix audit tables to match shared audit infrastructure
-- The shared AuditOutbox uses:
-- - record_id VARCHAR(50) instead of UUID (to support both UUID and string IDs)
-- - status CHECK includes 'completed' (for successful Kafka processing)
-- - old_values/new_values need to accept null or valid JSON (not empty strings)

-- Add 'completed' status to the constraint (matching tenant service pattern)
ALTER TABLE audit_outbox DROP CONSTRAINT IF EXISTS audit_outbox_status_check;
ALTER TABLE audit_outbox ADD CONSTRAINT audit_outbox_status_check
    CHECK (status IN ('pending', 'processing', 'completed', 'failed'));

-- Convert record_id from UUID to VARCHAR(50) to match shared infrastructure
-- This allows AuditOutbox.RecordID (string) to be inserted without type errors
ALTER TABLE audit_outbox ALTER COLUMN record_id TYPE VARCHAR(50) USING record_id::VARCHAR(50);
ALTER TABLE audit_log ALTER COLUMN record_id TYPE VARCHAR(50) USING record_id::VARCHAR(50);
