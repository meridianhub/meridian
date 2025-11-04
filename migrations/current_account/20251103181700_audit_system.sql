-- Current Account Audit System
-- Static audit table creation (CockroachDB compatible)
-- Audit logging will be handled at application level via GORM hooks
-- See ADR-0009 for rationale

-- Create audit schema
CREATE SCHEMA IF NOT EXISTS current_account_audit;

-- Create audit log table
CREATE TABLE IF NOT EXISTS current_account_audit.audit_log (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),

    -- What changed
    table_name VARCHAR(100) NOT NULL,
    operation VARCHAR(10) NOT NULL CHECK (operation IN ('INSERT', 'UPDATE', 'DELETE')),

    -- Record identification
    record_id UUID NOT NULL,

    -- Change metadata
    changed_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    changed_by VARCHAR(100),

    -- Change details
    old_values JSONB,
    new_values JSONB,

    -- Additional context
    transaction_id VARCHAR(100),
    client_ip INET,
    user_agent TEXT
);

-- Create indexes for efficient audit queries
CREATE INDEX IF NOT EXISTS idx_audit_log_table_name ON current_account_audit.audit_log(table_name);
CREATE INDEX IF NOT EXISTS idx_audit_log_record_id ON current_account_audit.audit_log(record_id);
CREATE INDEX IF NOT EXISTS idx_audit_log_changed_at ON current_account_audit.audit_log(changed_at);
CREATE INDEX IF NOT EXISTS idx_audit_log_changed_by ON current_account_audit.audit_log(changed_by);
CREATE INDEX IF NOT EXISTS idx_audit_log_operation ON current_account_audit.audit_log(operation);

-- Create helper view for easy audit queries
CREATE OR REPLACE VIEW current_account_audit.change_summary AS
SELECT
    id,
    'current_account.' || table_name AS table_full_name,
    operation,
    record_id,
    changed_at,
    changed_by,
    CASE
        WHEN operation = 'UPDATE' THEN
            (SELECT json_object_agg(key, value)
             FROM jsonb_each(new_values)
             WHERE new_values->key IS DISTINCT FROM old_values->key)
        ELSE NULL
    END AS changed_fields,
    transaction_id
FROM current_account_audit.audit_log
ORDER BY changed_at DESC;
