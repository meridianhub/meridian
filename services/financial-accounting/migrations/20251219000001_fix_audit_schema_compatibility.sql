-- Fix audit tables to match shared audit infrastructure
-- The shared AuditOutbox uses:
-- - record_id VARCHAR(50) instead of UUID (to support both UUID and string IDs)
-- - status CHECK includes 'completed' (for successful Kafka processing)
-- - old_values/new_values as TEXT (shared infrastructure may write empty strings)

-- Add 'completed' status to the constraint (matching tenant service pattern)
ALTER TABLE audit_outbox DROP CONSTRAINT IF EXISTS audit_outbox_status_check;
ALTER TABLE audit_outbox ADD CONSTRAINT audit_outbox_status_check
    CHECK (status IN ('pending', 'processing', 'completed', 'failed'));

-- Convert record_id from UUID to VARCHAR(50) to match shared infrastructure
-- This allows AuditOutbox.RecordID (string) to be inserted without type errors
ALTER TABLE audit_outbox ALTER COLUMN record_id TYPE VARCHAR(50) USING record_id::VARCHAR(50);

-- Convert old_values/new_values from JSONB to TEXT for compatibility
-- The shared audit infrastructure may write empty strings when values are nil,
-- which is invalid for JSONB columns. TEXT columns accept any string.
ALTER TABLE audit_outbox ALTER COLUMN old_values TYPE TEXT USING old_values::TEXT;
ALTER TABLE audit_outbox ALTER COLUMN new_values TYPE TEXT USING new_values::TEXT;

-- Drop the view that depends on record_id and old_values/new_values before altering
DROP VIEW IF EXISTS change_summary;

-- Alter the column types for audit_log as well
ALTER TABLE audit_log ALTER COLUMN record_id TYPE VARCHAR(50) USING record_id::VARCHAR(50);
ALTER TABLE audit_log ALTER COLUMN old_values TYPE TEXT USING old_values::TEXT;
ALTER TABLE audit_log ALTER COLUMN new_values TYPE TEXT USING new_values::TEXT;

-- Recreate the view using TEXT columns (parse JSON only when valid)
-- Note: Changed from jsonb_each to parsing the text as JSON only when non-empty
CREATE OR REPLACE VIEW change_summary AS
SELECT
    id,
    table_name AS table_full_name,
    operation,
    record_id,
    changed_at,
    changed_by,
    CASE
        WHEN operation = 'UPDATE' AND new_values IS NOT NULL AND new_values != '' THEN
            COALESCE(
                (SELECT json_object_agg(key, value)
                 FROM jsonb_each(new_values::jsonb)
                 WHERE (old_values IS NULL OR old_values = '' OR new_values::jsonb->key IS DISTINCT FROM old_values::jsonb->key)),
                '{}'::json
            )
        ELSE NULL
    END AS changed_fields,
    transaction_id
FROM audit_log
ORDER BY changed_at DESC;
