-- Add circuit-breaker tracking and instruction lifecycle columns
-- that were introduced after the initial migration.

-- provider_connections: circuit-breaker state tracking
ALTER TABLE provider_connections ADD COLUMN IF NOT EXISTS circuit_opened_at TIMESTAMPTZ NULL;
ALTER TABLE provider_connections ADD COLUMN IF NOT EXISTS failure_count INT NOT NULL DEFAULT 0;
ALTER TABLE provider_connections ADD COLUMN IF NOT EXISTS success_count INT NOT NULL DEFAULT 0;

-- instructions: dispatch lifecycle and error tracking
ALTER TABLE instructions ADD COLUMN IF NOT EXISTS dispatched_at TIMESTAMPTZ NULL;
ALTER TABLE instructions ADD COLUMN IF NOT EXISTS completed_at TIMESTAMPTZ NULL;
ALTER TABLE instructions ADD COLUMN IF NOT EXISTS failure_reason TEXT NULL;
ALTER TABLE instructions ADD COLUMN IF NOT EXISTS error_code VARCHAR(64) NULL;
ALTER TABLE instructions ADD COLUMN IF NOT EXISTS version BIGINT NOT NULL DEFAULT 1;
