-- Forecasting Service Schema
-- Manages forecasting strategies that generate forward curves from market data
-- Uses CockroachDB with database-per-service architecture

--------------------------------------------------------------------------------
-- Section 1: Forecasting Strategy Table
-- Defines Starlark-based forecasting strategies with lifecycle management
--------------------------------------------------------------------------------

CREATE TABLE forecasting_strategy (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id TEXT NOT NULL,
    name TEXT NOT NULL,
    description TEXT,
    starlark_code TEXT NOT NULL,
    horizon_hours INT NOT NULL CHECK (horizon_hours > 0 AND horizon_hours <= 168),
    granularity_hours INT NOT NULL CHECK (granularity_hours > 0 AND granularity_hours <= horizon_hours),
    schedule TEXT NOT NULL,
    input_dataset_codes TEXT[] NOT NULL,
    output_dataset_code TEXT NOT NULL,
    reference_data_resolution_key TEXT,
    status TEXT NOT NULL DEFAULT 'DRAFT',
    version BIGINT NOT NULL DEFAULT 1,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT chk_forecasting_strategy_status CHECK (status IN ('DRAFT', 'ACTIVE', 'DEPRECATED'))
);

COMMENT ON TABLE forecasting_strategy IS 'Starlark-based forecasting strategies that generate forward curves from market data inputs';
COMMENT ON COLUMN forecasting_strategy.starlark_code IS 'Starlark script source code executed to generate forward curve predictions';
COMMENT ON COLUMN forecasting_strategy.horizon_hours IS 'How far into the future the forecast extends (1-168 hours)';
COMMENT ON COLUMN forecasting_strategy.granularity_hours IS 'Time spacing between forecast data points (must be <= horizon_hours)';
COMMENT ON COLUMN forecasting_strategy.schedule IS 'Cron expression defining when the strategy executes (e.g., "0 16 * * *")';
COMMENT ON COLUMN forecasting_strategy.input_dataset_codes IS 'MDS dataset codes that the strategy reads as input';
COMMENT ON COLUMN forecasting_strategy.output_dataset_code IS 'MDS dataset code where the forward curve is published';
COMMENT ON COLUMN forecasting_strategy.reference_data_resolution_key IS 'Optional hierarchy node context for the forecast';
COMMENT ON COLUMN forecasting_strategy.version IS 'Optimistic locking version, incremented on every update';

--------------------------------------------------------------------------------
-- Section 2: Indexes
--------------------------------------------------------------------------------

-- Partial unique index: only one ACTIVE strategy per tenant+name combination.
-- CockroachDB supports partial unique indexes via WHERE clause.
CREATE UNIQUE INDEX idx_forecasting_strategy_unique_active
    ON forecasting_strategy (tenant_id, name)
    WHERE status = 'ACTIVE';

-- Tenant lookup with status filter for listing queries
CREATE INDEX idx_forecasting_strategy_tenant_status
    ON forecasting_strategy (tenant_id, status, created_at DESC);

-- Active strategies for scheduler lookups
CREATE INDEX idx_forecasting_strategy_active
    ON forecasting_strategy (status, tenant_id)
    WHERE status = 'ACTIVE';
