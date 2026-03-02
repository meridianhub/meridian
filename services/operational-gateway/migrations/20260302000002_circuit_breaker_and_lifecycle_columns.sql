-- Add circuit-breaker tracking and instruction lifecycle columns
-- that were introduced after the initial migration.

-- provider_connections: circuit-breaker state tracking
ALTER TABLE provider_connections ADD COLUMN IF NOT EXISTS circuit_opened_at TIMESTAMPTZ NULL;
ALTER TABLE provider_connections ADD COLUMN IF NOT EXISTS failure_count INT NOT NULL DEFAULT 0;
ALTER TABLE provider_connections ADD COLUMN IF NOT EXISTS success_count INT NOT NULL DEFAULT 0;

-- Fix health_status: domain uses UNKNOWN, initial migration used UNSPECIFIED.
-- CockroachDB auto-generates CHECK constraint names; try both PostgreSQL and
-- CockroachDB naming conventions to ensure the drop succeeds.
ALTER TABLE provider_connections DROP CONSTRAINT IF EXISTS provider_connections_health_status_check;
ALTER TABLE provider_connections DROP CONSTRAINT IF EXISTS check_health_status;
ALTER TABLE provider_connections ALTER COLUMN health_status SET DEFAULT 'UNKNOWN';
ALTER TABLE provider_connections ADD CONSTRAINT provider_connections_health_status_check
    CHECK (health_status IN ('UNKNOWN', 'HEALTHY', 'DEGRADED', 'UNHEALTHY'));

-- instructions: dispatch lifecycle and error tracking
ALTER TABLE instructions ADD COLUMN IF NOT EXISTS dispatched_at TIMESTAMPTZ NULL;
ALTER TABLE instructions ADD COLUMN IF NOT EXISTS completed_at TIMESTAMPTZ NULL;
ALTER TABLE instructions ADD COLUMN IF NOT EXISTS failure_reason TEXT NULL;
ALTER TABLE instructions ADD COLUMN IF NOT EXISTS error_code VARCHAR(64) NULL;
ALTER TABLE instructions ADD COLUMN IF NOT EXISTS version BIGINT NOT NULL DEFAULT 1;
