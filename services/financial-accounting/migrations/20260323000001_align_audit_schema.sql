-- Align audit_log and audit_outbox with shared audit infrastructure (DDL)
--
-- Fixes schema mismatches between migration-created tables and application code:
--   1. GORM AuditLog model and AuditService query use "created_at", migrations have "changed_at"
--   2. tenant_audit_writer writes "event_id" for idempotency, but column doesn't exist
--   3. Missing columns: schema_name, correlation_id, causation_id, idempotency_key
--
-- CockroachDB constraints:
--   - Column must be "public" before it can be indexed or used in DML
--   - Newly-added columns cannot be referenced in DML in the same migration
--
-- This migration adds columns only. DML (backfill) is in the next migration.
-- Note: record_id type (UUID -> VARCHAR) and client_ip type (INET -> VARCHAR)
-- are NOT altered here because CockroachDB does not support ALTER COLUMN TYPE
-- from UUID to VARCHAR without experimental flags. Services that need this fix
-- already have it via their compatibility migrations (DROP/RECREATE approach).

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
