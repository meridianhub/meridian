-- Identity Service Audit System
-- Static audit table creation (CockroachDB compatible)
-- Audit logging handled at application level via GORM hooks
-- See ADR-0009 for rationale
-- Uses unqualified table names (relies on per-tenant schema routing via search_path)
--
-- Schema is aligned with shared/platform/audit (created_at, not changed_at) and the
-- audit-worker TenantAuditWriter (event_id, schema_name, correlation_id, causation_id,
-- idempotency_key) from the start. Other services accreted these columns through
-- follow-up alignment migrations (services/party/migrations/20260323*); identity
-- starts aligned to avoid the same retrofit.

-- Create audit_log table (unqualified, singular)
CREATE TABLE IF NOT EXISTS audit_log (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),

    -- What changed
    table_name VARCHAR(100) NOT NULL,
    operation VARCHAR(10) NOT NULL CHECK (operation IN ('INSERT', 'UPDATE', 'DELETE', 'INITIAL_IMPORT')),

    -- Record identification
    record_id UUID NOT NULL,

    -- Change metadata
    -- created_at matches the GORM AuditLog model in shared/platform/audit/hooks.go
    -- and the consumer/worker INSERT/SELECT paths.
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    changed_by VARCHAR(100),

    -- Change details (TEXT to match GORM model - contains JSON strings)
    old_values TEXT,
    new_values TEXT,

    -- Additional context
    transaction_id VARCHAR(100),
    client_ip VARCHAR(45),
    user_agent TEXT,

    -- Idempotency and tenant-aware writer fields used by the audit-worker
    -- TenantAuditWriter (services/audit-worker/adapters/persistence/tenant_audit_writer.go).
    -- event_id supports ON CONFLICT idempotent inserts.
    event_id VARCHAR(100),
    schema_name VARCHAR(100),
    correlation_id VARCHAR(255),
    causation_id VARCHAR(255),
    idempotency_key VARCHAR(255)
);

-- Indexes for efficient audit queries
CREATE INDEX IF NOT EXISTS idx_audit_log_table_name ON audit_log(table_name);
CREATE INDEX IF NOT EXISTS idx_audit_log_record_id ON audit_log(record_id);
CREATE INDEX IF NOT EXISTS idx_audit_log_created_at ON audit_log(created_at);
CREATE INDEX IF NOT EXISTS idx_audit_log_changed_by ON audit_log(changed_by);
CREATE INDEX IF NOT EXISTS idx_audit_log_operation ON audit_log(operation);
-- Unique index on event_id supports ON CONFLICT for idempotent writes from
-- the Kafka consumer path. CockroachDB treats NULLs as distinct in UNIQUE
-- indexes, so outbox-path rows that leave event_id NULL coexist with
-- Kafka-path rows that populate it (matches services/party/migrations/20260323*).
CREATE UNIQUE INDEX IF NOT EXISTS idx_audit_log_event_id ON audit_log(event_id);

-- Create audit_outbox table for async processing (unqualified, singular)
-- GORM hooks write to outbox, background worker moves to audit_log
CREATE TABLE IF NOT EXISTS audit_outbox (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),

    -- What changed
    table_name VARCHAR(100) NOT NULL,
    operation VARCHAR(10) NOT NULL CHECK (operation IN ('INSERT', 'UPDATE', 'DELETE', 'INITIAL_IMPORT')),

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
