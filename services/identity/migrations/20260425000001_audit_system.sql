-- Identity Service Audit System
-- Static audit table creation (CockroachDB compatible)
-- Audit logging handled at application level via GORM hooks
-- See ADR-0009 for rationale
-- Uses unqualified table names (relies on per-tenant schema routing via search_path)

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
