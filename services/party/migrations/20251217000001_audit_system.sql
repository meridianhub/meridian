-- Party Service Audit System
-- Static audit table creation (CockroachDB compatible)
-- Audit logging handled at application level via GORM hooks
-- See ADR-0009 for rationale
-- Uses unqualified table names (relies on database-per-service architecture)

-- Create audit_log table (unqualified, singular)
CREATE TABLE IF NOT EXISTS audit_log (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),

    -- What changed
    table_name VARCHAR(100) NOT NULL,
    operation VARCHAR(10) NOT NULL CHECK (operation IN ('INSERT', 'UPDATE', 'DELETE')),

    -- Record identification
    record_id UUID NOT NULL,

    -- Change metadata
    changed_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    changed_by VARCHAR(100),

    -- Change details (TEXT to match GORM model - contains JSON strings)
    old_values TEXT,
    new_values TEXT,

    -- Additional context
    transaction_id VARCHAR(100),
    client_ip VARCHAR(45),
    user_agent TEXT
);

-- Create indexes for efficient audit queries
CREATE INDEX IF NOT EXISTS idx_audit_log_table_name ON audit_log(table_name);
CREATE INDEX IF NOT EXISTS idx_audit_log_record_id ON audit_log(record_id);
CREATE INDEX IF NOT EXISTS idx_audit_log_changed_at ON audit_log(changed_at);
CREATE INDEX IF NOT EXISTS idx_audit_log_changed_by ON audit_log(changed_by);
CREATE INDEX IF NOT EXISTS idx_audit_log_operation ON audit_log(operation);

-- Create audit_outbox table for async processing (unqualified, singular)
-- GORM hooks write to outbox, background worker moves to audit_log
CREATE TABLE IF NOT EXISTS audit_outbox (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),

    -- What changed
    table_name VARCHAR(100) NOT NULL,
    operation VARCHAR(10) NOT NULL CHECK (operation IN ('INSERT', 'UPDATE', 'DELETE')),

    -- Record identification
    record_id UUID NOT NULL,

    -- Change details (TEXT to match GORM model - contains JSON strings)
    old_values TEXT,
    new_values TEXT,

    -- Processing status (includes 'completed' for successful processing)
    status VARCHAR(20) NOT NULL DEFAULT 'pending' CHECK (status IN ('pending', 'processing', 'completed', 'failed')),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    retry_count INT NOT NULL DEFAULT 0,
    last_error TEXT,

    -- Additional context
    changed_by VARCHAR(100),
    transaction_id VARCHAR(100),
    client_ip VARCHAR(45),
    user_agent TEXT
);

-- Index for worker to efficiently find pending entries
CREATE INDEX IF NOT EXISTS idx_audit_outbox_status_created ON audit_outbox(status, created_at);

-- Create helper view for easy audit queries
-- Note: old_values and new_values are TEXT containing JSON, cast to JSONB for comparison
CREATE OR REPLACE VIEW change_summary AS
SELECT
    id,
    table_name AS table_full_name,
    operation,
    record_id,
    changed_at,
    changed_by,
    CASE
        WHEN operation = 'UPDATE' AND new_values IS NOT NULL AND old_values IS NOT NULL THEN
            (SELECT json_object_agg(key, value)
             FROM jsonb_each(new_values::jsonb)
             WHERE (new_values::jsonb)->key IS DISTINCT FROM (old_values::jsonb)->key)
        ELSE NULL
    END AS changed_fields,
    transaction_id
FROM audit_log
ORDER BY changed_at DESC;
