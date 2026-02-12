-- Create saga_executions table for persisting saga execution records.
-- This provides an audit trail of all saga executions performed by the
-- payment-order service, enabling debugging, monitoring, and compliance.

CREATE TABLE IF NOT EXISTS saga_executions (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    payment_order_id UUID NOT NULL,
    saga_name VARCHAR(128) NOT NULL,
    saga_version INT NOT NULL DEFAULT 0,
    status VARCHAR(32) NOT NULL DEFAULT 'RUNNING',
    correlation_id VARCHAR(128) NOT NULL DEFAULT '',
    input JSONB NOT NULL DEFAULT '{}',
    output JSONB NOT NULL DEFAULT '{}',
    error_message TEXT NOT NULL DEFAULT '',
    step_count INT NOT NULL DEFAULT 0,
    duration_ms BIGINT NOT NULL DEFAULT 0,
    started_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    completed_at TIMESTAMPTZ,

    CONSTRAINT saga_executions_status_check CHECK (
        status IN ('RUNNING', 'COMPLETED', 'FAILED', 'COMPENSATED')
    )
);

-- Index for querying executions by payment order
CREATE INDEX IF NOT EXISTS idx_saga_executions_payment_order_id
    ON saga_executions (payment_order_id);

-- Index for querying executions by saga name and status
CREATE INDEX IF NOT EXISTS idx_saga_executions_saga_name_status
    ON saga_executions (saga_name, status);

-- Index for querying executions by correlation ID
CREATE INDEX IF NOT EXISTS idx_saga_executions_correlation_id
    ON saga_executions (correlation_id);
