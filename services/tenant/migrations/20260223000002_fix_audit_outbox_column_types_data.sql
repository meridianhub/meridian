-- Fix audit_outbox and audit_log column types: JSONB → TEXT (part 2: data + cleanup)
-- Copies existing JSONB data to TEXT columns, drops old JSONB columns,
-- and recreates the change_summary VIEW using explicit JSONB casts.

-- audit_outbox: copy data and drop old JSONB columns
UPDATE audit_outbox SET old_values = old_values_jsonb::TEXT WHERE old_values_jsonb IS NOT NULL;
ALTER TABLE audit_outbox DROP COLUMN old_values_jsonb;
UPDATE audit_outbox SET new_values = new_values_jsonb::TEXT WHERE new_values_jsonb IS NOT NULL;
ALTER TABLE audit_outbox DROP COLUMN new_values_jsonb;

-- audit_log: copy data and drop old JSONB columns
UPDATE audit_log SET old_values = old_values_jsonb::TEXT WHERE old_values_jsonb IS NOT NULL;
ALTER TABLE audit_log DROP COLUMN old_values_jsonb;
UPDATE audit_log SET new_values = new_values_jsonb::TEXT WHERE new_values_jsonb IS NOT NULL;
ALTER TABLE audit_log DROP COLUMN new_values_jsonb;

-- Recreate change_summary VIEW with explicit JSONB casts for the TEXT columns
CREATE OR REPLACE VIEW change_summary AS
SELECT
    id,
    table_name AS table_full_name,
    operation,
    record_id,
    changed_at,
    changed_by,
    CASE
        WHEN operation = 'UPDATE' AND new_values IS NOT NULL THEN
            COALESCE(
                (SELECT json_object_agg(key, value)
                 FROM jsonb_each(new_values::JSONB)
                 WHERE new_values::JSONB->key IS DISTINCT FROM old_values::JSONB->key),
                '{}'::json
            )
        ELSE NULL
    END AS changed_fields,
    transaction_id
FROM audit_log
ORDER BY changed_at DESC;
