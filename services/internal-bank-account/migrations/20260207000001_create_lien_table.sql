-- Create lien table for fund reservations on internal bank accounts
-- Mirrors the Current Account lien infrastructure for multi-asset valuation support
-- Liens reserve funds (available balance = current balance - sum of active liens)

CREATE TABLE IF NOT EXISTS lien (
    -- Primary key
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),

    -- Foreign key to internal bank account
    account_id UUID NOT NULL,

    -- Monetary amount (stored in account's native instrument as minor units)
    amount_cents BIGINT NOT NULL,
    currency VARCHAR(3) NOT NULL,

    -- Bucket identifier for bucket-aware reservations (fungibility key)
    -- Empty string represents the default bucket for backward compatibility
    bucket_id VARCHAR(255) NOT NULL DEFAULT '',

    -- Lifecycle state: ACTIVE -> EXECUTED or TERMINATED (terminal)
    status VARCHAR(20) NOT NULL DEFAULT 'ACTIVE',

    -- Reference to the payment order that created this lien (unique per payment order)
    payment_order_reference VARCHAR(255) NOT NULL,

    -- Reason for termination (only set when status is TERMINATED)
    termination_reason VARCHAR(1000),

    -- Optional expiration time for automatic termination of stale liens
    expires_at TIMESTAMPTZ,

    -- Valuation fields for atomic price lock (nullable for backward compatibility)
    -- reserved_quantity stores the original input before valuation (e.g., 100 kWh)
    reserved_quantity JSONB,
    -- valued_amount stores the price-locked valuation result (e.g., 35.00 GBP)
    valued_amount JSONB,
    -- valuation_analysis stores the full valuation audit trail
    valuation_analysis JSONB,

    -- Audit fields
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    version INT NOT NULL DEFAULT 1,

    -- Constraints
    CONSTRAINT chk_lien_amount_positive CHECK (amount_cents > 0),
    CONSTRAINT chk_lien_status CHECK (status IN ('ACTIVE', 'EXECUTED', 'TERMINATED')),
    CONSTRAINT fk_lien_internal_bank_account
        FOREIGN KEY (account_id) REFERENCES "internal_bank_account" (id)
        ON UPDATE NO ACTION ON DELETE RESTRICT
);

-- Composite index for querying active liens per account
CREATE INDEX idx_lien_account_status ON lien(account_id, status);

-- Composite index for bucket-scoped queries
CREATE INDEX idx_lien_account_bucket ON lien(account_id, bucket_id);

-- Index for finding stale/expired liens for cleanup
CREATE INDEX idx_lien_expires_at ON lien(expires_at);

-- Unique index for idempotency: each payment order has at most one lien
CREATE UNIQUE INDEX idx_lien_payment_order ON lien(payment_order_reference);

COMMENT ON TABLE lien IS
    'Fund reservations on internal bank accounts. Invariant: Available Balance = Current Balance - Sum(Active Liens). Supports multi-asset valuation with price lock for Ghost Pricing prevention.';
