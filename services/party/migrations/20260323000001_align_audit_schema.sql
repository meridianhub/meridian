-- Align audit_log and audit_outbox with shared audit infrastructure
--
-- Fixes three schema mismatches between migration-created tables and application code:
--   1. GORM AuditLog model and AuditService query use "created_at", migrations have "changed_at"
--   2. tenant_audit_writer writes "event_id" for idempotency, but column doesn't exist
--   3. record_id is UUID in some services, but code writes string IDs (e.g. "IBA-xxx")
--
-- CockroachDB constraints:
--   - ALTER COLUMN TYPE cannot run inside a transaction
--   - Column must be "public" before it can be indexed or used in DML
--
-- This migration uses atlas:txn false to handle ALTER COLUMN TYPE.
-- atlas:txn false

-- ============================================================
-- audit_log: add created_at alongside changed_at
-- ============================================================
-- Add created_at (the column the code writes to)
ALTER TABLE IF EXISTS audit_log ADD COLUMN IF NOT EXISTS created_at TIMESTAMPTZ;

-- Backfill from changed_at for any existing rows (guarded for services where audit_log may not exist)
UPDATE audit_log SET created_at = changed_at WHERE created_at IS NULL AND changed_at IS NOT NULL;

-- Set default for new rows
ALTER TABLE IF EXISTS audit_log ALTER COLUMN created_at SET DEFAULT now();

-- Add event_id for idempotent writes from tenant_audit_writer (Kafka consumer path)
ALTER TABLE IF EXISTS audit_log ADD COLUMN IF NOT EXISTS event_id VARCHAR(100);

-- Add columns the tenant_audit_writer writes but migrations never created
ALTER TABLE IF EXISTS audit_log ADD COLUMN IF NOT EXISTS schema_name VARCHAR(100);
ALTER TABLE IF EXISTS audit_log ADD COLUMN IF NOT EXISTS correlation_id VARCHAR(255);
ALTER TABLE IF EXISTS audit_log ADD COLUMN IF NOT EXISTS causation_id VARCHAR(255);
ALTER TABLE IF EXISTS audit_log ADD COLUMN IF NOT EXISTS idempotency_key VARCHAR(255);

-- Create unique index on event_id (supports ON CONFLICT for idempotency)
CREATE UNIQUE INDEX IF NOT EXISTS idx_audit_log_event_id ON audit_log(event_id);

-- Index created_at for query ordering
CREATE INDEX IF NOT EXISTS idx_audit_log_created_at ON audit_log(created_at);

-- ============================================================
-- Drop change_summary view before ALTER COLUMN TYPE
-- (view depends on record_id and changed_at columns)
-- ============================================================
DROP VIEW IF EXISTS change_summary;

-- ============================================================
-- audit_log: fix record_id type (UUID -> VARCHAR)
-- ============================================================
-- Change record_id from UUID to VARCHAR(100) to support string IDs like "IBA-xxx"
-- This is a no-op if already VARCHAR from a prior compatibility migration.
ALTER TABLE IF EXISTS audit_log ALTER COLUMN record_id TYPE VARCHAR(100);

-- ============================================================
-- audit_log: fix client_ip type (INET -> VARCHAR)
-- ============================================================
-- GORM model uses VARCHAR(45), some migrations created INET.
-- This is a no-op if already VARCHAR.
ALTER TABLE IF EXISTS audit_log ALTER COLUMN client_ip TYPE VARCHAR(45);

-- ============================================================
-- audit_outbox: fix record_id type (UUID -> VARCHAR)
-- ============================================================
ALTER TABLE IF EXISTS audit_outbox ALTER COLUMN record_id TYPE VARCHAR(100);

-- ============================================================
-- audit_outbox: fix client_ip type (INET -> VARCHAR)
-- ============================================================
ALTER TABLE IF EXISTS audit_outbox ALTER COLUMN client_ip TYPE VARCHAR(45);

-- ============================================================
-- Recreate change_summary view with updated column types
-- (only if audit_log exists - handles partial migration scenarios)
-- ============================================================
DO $do$ BEGIN
  IF EXISTS (SELECT 1 FROM information_schema.tables WHERE table_name = 'audit_log' AND table_schema = current_schema()) THEN
    EXECUTE $view$
      CREATE OR REPLACE VIEW change_summary AS
      SELECT
          id,
          table_name AS table_full_name,
          operation,
          record_id,
          changed_at,
          changed_by,
          CASE
              WHEN operation = 'UPDATE' AND new_values IS NOT NULL AND old_values IS NOT NULL THEN
                  (SELECT json_object_agg(key, value)
                   FROM jsonb_each(new_values::jsonb)
                   WHERE (new_values::jsonb)->key IS DISTINCT FROM (old_values::jsonb)->key)
              ELSE NULL
          END AS changed_fields,
          transaction_id
      FROM audit_log
      ORDER BY changed_at DESC
    $view$;
  END IF;
END $do$;
