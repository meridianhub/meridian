-- Tenant Service Audit System
-- Static audit table creation (CockroachDB compatible)
-- Audit logging handled at application level via GORM hooks
-- Uses unqualified table names (relies on database-per-service architecture)
-- Note: Tenant service uses string IDs (varchar) for record_id, not UUIDs

-- Create audit_log table (unqualified, singular)
CREATE TABLE IF NOT EXISTS audit_log (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),

    -- What changed
    table_name VARCHAR(100) NOT NULL,
    operation VARCHAR(10) NOT NULL CHECK (operation IN ('INSERT', 'UPDATE', 'DELETE')),

    -- Record identification (string ID for tenant compatibility)
    record_id VARCHAR(50) NOT NULL,

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
CREATE INDEX IF NOT EXISTS idx_audit_log_table_name ON audit_log(table_name);
CREATE INDEX IF NOT EXISTS idx_audit_log_record_id ON audit_log(record_id);
CREATE INDEX IF NOT EXISTS idx_audit_log_changed_at ON audit_log(changed_at);
CREATE INDEX IF NOT EXISTS idx_audit_log_changed_by ON audit_log(changed_by);
CREATE INDEX IF NOT EXISTS idx_audit_log_operation ON audit_log(operation);

-- Create audit_outbox table for async processing (unqualified, singular)
-- GORM hooks write to outbox, background worker moves to audit_log
-- Note: Includes 'completed' status from the start (learned from task 7)
CREATE TABLE IF NOT EXISTS audit_outbox (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),

    -- What changed
    table_name VARCHAR(100) NOT NULL,
    operation VARCHAR(10) NOT NULL CHECK (operation IN ('INSERT', 'UPDATE', 'DELETE')),

    -- Record identification (string ID for tenant compatibility)
    record_id VARCHAR(50) NOT NULL,

    -- Change details
    old_values JSONB,
    new_values JSONB,

    -- Processing status (includes 'completed' from the start)
    status VARCHAR(20) NOT NULL DEFAULT 'pending' CHECK (status IN ('pending', 'processing', 'completed', 'failed')),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    retry_count INT NOT NULL DEFAULT 0,
    last_error TEXT,

    -- Additional context
    changed_by VARCHAR(100),
    transaction_id VARCHAR(100),
    client_ip INET,
    user_agent TEXT
);

-- Index for worker to efficiently find pending entries
CREATE INDEX IF NOT EXISTS idx_audit_outbox_status_created ON audit_outbox(status, created_at);

-- Create helper view for easy audit queries
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
