-- Event Outbox Table for Financial Accounting
-- Implements transactional outbox pattern for reliable event delivery
-- See shared/platform/events/schema.sql for reference schema
-- Ensures at-least-once delivery of audit-critical control operation events

CREATE TABLE IF NOT EXISTS event_outbox (
    -- Primary key using UUID for global uniqueness
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),

    -- Event identification
    event_type VARCHAR(200) NOT NULL,
    aggregate_id VARCHAR(100) NOT NULL,
    aggregate_type VARCHAR(100) NOT NULL,

    -- Event payload (serialized protobuf)
    event_payload BYTEA NOT NULL,

    -- Tracing
    correlation_id VARCHAR(100),
    causation_id VARCHAR(100),

    -- Processing status: pending, processing, completed, failed
    status VARCHAR(20) NOT NULL DEFAULT 'pending',

    -- Kafka destination
    topic VARCHAR(200) NOT NULL,
    partition_key VARCHAR(200),

    -- Timestamps and retry tracking
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    processed_at TIMESTAMPTZ,
    retry_count INT NOT NULL DEFAULT 0,
    last_error TEXT,

    -- Service identifier for multi-tenant outbox tables
    service_name VARCHAR(100) NOT NULL
);

-- Indexes for efficient polling and monitoring
CREATE INDEX IF NOT EXISTS idx_event_outbox_status_service ON event_outbox(status, service_name);
CREATE INDEX IF NOT EXISTS idx_event_outbox_created_at ON event_outbox(created_at);
CREATE INDEX IF NOT EXISTS idx_event_outbox_aggregate ON event_outbox(aggregate_type, aggregate_id);
CREATE INDEX IF NOT EXISTS idx_event_outbox_event_type ON event_outbox(event_type);
CREATE INDEX IF NOT EXISTS idx_event_outbox_correlation ON event_outbox(correlation_id) WHERE correlation_id IS NOT NULL;
CREATE INDEX IF NOT EXISTS idx_event_outbox_processed_at ON event_outbox(processed_at) WHERE processed_at IS NOT NULL;

COMMENT ON TABLE event_outbox IS 'Transactional outbox for reliable domain event delivery. Events are written atomically with business operations and processed by background worker.';
COMMENT ON COLUMN event_outbox.event_type IS 'Fully qualified event type (e.g., financial_accounting.booking_log_updated.v1)';
COMMENT ON COLUMN event_outbox.status IS 'Processing status: pending (ready), processing (in-flight), completed (published), failed (DLQ)';
COMMENT ON COLUMN event_outbox.service_name IS 'Service that owns this entry, used for multi-service deployments sharing the same database';
