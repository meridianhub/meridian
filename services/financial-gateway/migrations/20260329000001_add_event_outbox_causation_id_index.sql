CREATE INDEX IF NOT EXISTS idx_event_outbox_causation_id ON event_outbox (causation_id) WHERE causation_id IS NOT NULL;
