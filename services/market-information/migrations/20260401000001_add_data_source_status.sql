-- Add lifecycle status to data_source for deprecation support.
-- Config-only resources go ACTIVE on creation, DEPRECATED on deprecation (no DRAFT state).

ALTER TABLE data_source ADD COLUMN status VARCHAR(20) NOT NULL DEFAULT 'ACTIVE';
ALTER TABLE data_source ADD COLUMN deprecated_at TIMESTAMPTZ NULL;

ALTER TABLE data_source ADD CONSTRAINT chk_data_source_status
  CHECK (status IN ('ACTIVE', 'DEPRECATED'));

CREATE INDEX idx_data_source_status ON data_source (status);
