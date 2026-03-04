-- Create event_outbox table for transactional outbox pattern.
-- Webhook events received from payment providers are written atomically
-- to this table. The outbox worker publishes them to Kafka for downstream consumers.

CREATE TABLE IF NOT EXISTS event_outbox (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    event_type VARCHAR(200) NOT NULL,
    aggregate_id VARCHAR(100) NOT NULL,
    aggregate_type VARCHAR(100) NOT NULL,
    event_payload BYTEA NOT NULL,
    correlation_id VARCHAR(100),
    causation_id VARCHAR(100),
    status VARCHAR(20) NOT NULL,
    topic VARCHAR(200) NOT NULL,
    partition_key VARCHAR(200),
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    processed_at TIMESTAMPTZ,
    retry_count INTEGER NOT NULL DEFAULT 0,
    last_error TEXT,
    service_name VARCHAR(100) NOT NULL
);

-- Index for efficient polling of pending events by worker
CREATE INDEX IF NOT EXISTS idx_event_outbox_status ON event_outbox(status) WHERE status = 'pending';

-- Index for cleanup of old processed events
CREATE INDEX IF NOT EXISTS idx_event_outbox_created ON event_outbox(created_at);

-- Index for correlation tracking and debugging
CREATE INDEX IF NOT EXISTS idx_event_outbox_aggregate ON event_outbox(aggregate_type, aggregate_id);
