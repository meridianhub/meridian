-- Create valuation_features table for storing valuation method assignments
-- This table links internal bank accounts to valuation methods for specific instrument transformations
-- Example: IBA with instrument_code=GBP has a valuation feature mapping USD→GBP

CREATE TABLE IF NOT EXISTS valuation_features (
    -- Primary key
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),

    -- Foreign key to internal bank account table
    account_id UUID NOT NULL,

    -- Input instrument that will be valued (e.g., "USD", "EUR", "kWh")
    instrument_code VARCHAR(32) NOT NULL,

    -- Reference to the valuation method (managed by Valuation Engine Service)
    valuation_method_id UUID NOT NULL,
    valuation_method_version INT NOT NULL,

    -- Method-specific parameters (JSON blob for flexibility)
    parameters JSONB,

    -- Lifecycle status following ADR-012 pattern
    lifecycle_status VARCHAR(16) NOT NULL,

    -- Bi-temporal validity tracking
    valid_from TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    valid_to TIMESTAMPTZ NOT NULL DEFAULT '9999-12-31 23:59:59+00',

    -- Audit fields
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    created_by VARCHAR(100) NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_by VARCHAR(100) NOT NULL,

    -- Optimistic locking
    version INT NOT NULL DEFAULT 1,

    -- Constraints
    CONSTRAINT chk_valuation_feature_lifecycle_status
        CHECK (lifecycle_status IN ('INITIATED', 'ACTIVE', 'TERMINATED')),
    CONSTRAINT chk_valuation_feature_temporal_range
        CHECK (valid_from < valid_to),

    -- Foreign key to internal bank account
    CONSTRAINT fk_valuation_feature_internal_bank_account
        FOREIGN KEY (account_id) REFERENCES "internal_bank_account" (id)
        ON UPDATE NO ACTION ON DELETE RESTRICT
);

-- Unique constraint: one active valuation method per (account, instrument) combination
-- This ensures we don't have conflicting methods for the same input instrument
CREATE UNIQUE INDEX idx_valuation_feature_account_instrument_active
    ON valuation_features (account_id, instrument_code)
    WHERE lifecycle_status = 'ACTIVE' AND valid_to = '9999-12-31 23:59:59+00';

-- Index for efficient queries by valuation method
CREATE INDEX idx_valuation_feature_method
    ON valuation_features (valuation_method_id)
    WHERE lifecycle_status = 'ACTIVE';

-- Bi-temporal query index
CREATE INDEX idx_valuation_feature_temporal
    ON valuation_features (account_id, valid_from, valid_to);

-- Index for finding all features for an account
CREATE INDEX idx_valuation_feature_account
    ON valuation_features (account_id);

-- Comment documenting the table purpose
COMMENT ON TABLE valuation_features IS
    'Stores valuation method assignments for internal bank accounts. Each feature maps an input instrument to the account native instrument using a specific valuation method.';
