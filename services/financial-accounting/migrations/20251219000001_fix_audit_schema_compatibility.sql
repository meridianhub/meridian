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

-- Drop the view that depends on record_id before altering the column
DROP VIEW IF EXISTS change_summary;

-- Alter the column type
ALTER TABLE audit_log ALTER COLUMN record_id TYPE VARCHAR(50) USING record_id::VARCHAR(50);

-- Recreate the view with COALESCE to handle NULL in json_object_agg
CREATE OR REPLACE VIEW change_summary AS
SELECT
    id,
    table_name AS table_full_name,
    operation,
    record_id,
    changed_at,
    changed_by,
    CASE
        WHEN operation = 'UPDATE' THEN
            COALESCE(
                (SELECT json_object_agg(key, value)
                 FROM jsonb_each(new_values)
                 WHERE new_values->key IS DISTINCT FROM old_values->key),
                '{}'::json
            )
        ELSE NULL
    END AS changed_fields,
    transaction_id
FROM audit_log
ORDER BY changed_at DESC;
