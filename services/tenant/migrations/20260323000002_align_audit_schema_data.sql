-- Align audit_log and audit_outbox with shared audit infrastructure (DML)
--
-- Backfill and index columns added in the previous DDL migration.
-- CockroachDB requires newly-added columns to be "public" before DML.

-- Backfill created_at from changed_at for any existing rows
UPDATE audit_log SET created_at = changed_at WHERE created_at IS NULL AND changed_at IS NOT NULL;

-- Set default for new rows
ALTER TABLE IF EXISTS audit_log ALTER COLUMN created_at SET DEFAULT now();

-- Create unique index on event_id (supports ON CONFLICT for idempotency)
CREATE UNIQUE INDEX IF NOT EXISTS idx_audit_log_event_id ON audit_log(event_id);

-- Index created_at for query ordering
CREATE INDEX IF NOT EXISTS idx_audit_log_created_at ON audit_log(created_at);
