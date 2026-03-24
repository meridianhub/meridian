-- Align audit_log and audit_outbox with shared audit infrastructure (DDL)
--
-- Fixes three schema mismatches between migration-created tables and application code:
--   1. GORM AuditLog model and AuditService query use "created_at", migrations have "changed_at"
--   2. tenant_audit_writer writes "event_id" for idempotency, but column doesn't exist
--   3. record_id is UUID in some services, but code writes string IDs (e.g. "IBA-xxx")
--
-- CockroachDB constraints:
--   - ALTER COLUMN TYPE cannot run inside a transaction
--   - Column must be "public" before it can be indexed or used in DML
--   - Newly-added columns cannot be referenced in DML in the same migration
--
-- This migration adds columns and alters types. DML (backfill) is in the next migration.
-- atlas:txn false

-- ============================================================
-- audit_log: add created_at alongside changed_at
-- ============================================================
ALTER TABLE IF EXISTS audit_log ADD COLUMN IF NOT EXISTS created_at TIMESTAMPTZ;

-- Add event_id for idempotent writes from tenant_audit_writer (Kafka consumer path)
ALTER TABLE IF EXISTS audit_log ADD COLUMN IF NOT EXISTS event_id VARCHAR(100);

-- Add columns the tenant_audit_writer writes but migrations never created
ALTER TABLE IF EXISTS audit_log ADD COLUMN IF NOT EXISTS schema_name VARCHAR(100);
ALTER TABLE IF EXISTS audit_log ADD COLUMN IF NOT EXISTS correlation_id VARCHAR(255);
ALTER TABLE IF EXISTS audit_log ADD COLUMN IF NOT EXISTS causation_id VARCHAR(255);
ALTER TABLE IF EXISTS audit_log ADD COLUMN IF NOT EXISTS idempotency_key VARCHAR(255);

-- ============================================================
-- Drop change_summary view before ALTER COLUMN TYPE
-- (view depends on record_id and changed_at columns)
-- ============================================================
DROP VIEW IF EXISTS change_summary;

-- ============================================================
-- audit_log: fix record_id type (UUID -> VARCHAR)
-- ============================================================
ALTER TABLE IF EXISTS audit_log ALTER COLUMN record_id TYPE VARCHAR(100);

-- ============================================================
-- audit_log: fix client_ip type (INET -> VARCHAR)
-- ============================================================
ALTER TABLE IF EXISTS audit_log ALTER COLUMN client_ip TYPE VARCHAR(45);

-- ============================================================
-- audit_outbox: fix record_id type (UUID -> VARCHAR)
-- ============================================================
ALTER TABLE IF EXISTS audit_outbox ALTER COLUMN record_id TYPE VARCHAR(100);

-- ============================================================
-- audit_outbox: fix client_ip type (INET -> VARCHAR)
-- ============================================================
ALTER TABLE IF EXISTS audit_outbox ALTER COLUMN client_ip TYPE VARCHAR(45);

-- Note: change_summary view is intentionally not recreated here.
-- It is a convenience view not used by application code, and recreating it
-- requires PL/pgSQL DO blocks which CockroachDB does not support.
