-- Fix audit tables to match shared audit infrastructure
-- CockroachDB does not support ALTER COLUMN TYPE from UUID to VARCHAR
-- For fresh databases, we drop and recreate with correct schema
-- This is safe for development environments

-- Drop dependent views and tables
DROP VIEW IF EXISTS change_summary;
DROP TABLE IF EXISTS audit_outbox;
DROP TABLE IF EXISTS audit_log;

-- Recreate audit_log with VARCHAR(50) for record_id
-- This matches the shared audit infrastructure which uses string IDs
CREATE TABLE audit_log (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),

    -- What changed
    table_name VARCHAR(100) NOT NULL,
    operation VARCHAR(10) NOT NULL CHECK (operation IN ('INSERT', 'UPDATE', 'DELETE')),

    -- Record identification (VARCHAR to support both UUID and string IDs)
    record_id VARCHAR(50) NOT NULL,

    -- Change metadata
    changed_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    changed_by VARCHAR(100),

    -- Change details (TEXT for compatibility with empty strings)
    old_values TEXT,
    new_values TEXT,

    -- Additional context
    transaction_id VARCHAR(100),
    client_ip INET,
    user_agent TEXT
);

-- Create indexes for efficient audit queries
CREATE INDEX idx_audit_log_table_name ON audit_log(table_name);
CREATE INDEX idx_audit_log_record_id ON audit_log(record_id);
CREATE INDEX idx_audit_log_changed_at ON audit_log(changed_at);
CREATE INDEX idx_audit_log_changed_by ON audit_log(changed_by);
CREATE INDEX idx_audit_log_operation ON audit_log(operation);

-- Recreate audit_outbox with VARCHAR(50) for record_id
CREATE TABLE audit_outbox (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),

    -- What changed
    table_name VARCHAR(100) NOT NULL,
    operation VARCHAR(10) NOT NULL CHECK (operation IN ('INSERT', 'UPDATE', 'DELETE')),

    -- Record identification (VARCHAR to support both UUID and string IDs)
    record_id VARCHAR(50) NOT NULL,

    -- Change details (TEXT for compatibility with empty strings)
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
    client_ip INET,
    user_agent TEXT
);

-- Index for worker to efficiently find pending entries
CREATE INDEX idx_audit_outbox_status_created ON audit_outbox(status, created_at);

-- Recreate the view using TEXT columns (parse JSON only when valid)
CREATE OR REPLACE VIEW change_summary AS
SELECT
    id,
    table_name AS table_full_name,
    operation,
    record_id,
    changed_at,
    changed_by,
    CASE
        WHEN operation = 'UPDATE'
             AND new_values IS NOT NULL
             AND new_values != ''
             AND new_values ~ '^{.*}$' THEN
            COALESCE(
                (SELECT json_object_agg(key, value)
                 FROM jsonb_each(new_values::jsonb)
                 WHERE (old_values IS NULL
                        OR old_values = ''
                        OR NOT (old_values ~ '^{.*}$')
                        OR (old_values ~ '^{.*}$'
                            AND new_values::jsonb->key IS DISTINCT FROM old_values::jsonb->key
                        ))),
                '{}'::json
            )
        ELSE NULL
    END AS changed_fields,
    transaction_id
FROM audit_log
ORDER BY changed_at DESC;
