CREATE TABLE IF NOT EXISTS suppressed_addresses (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id VARCHAR(36) NOT NULL,
    email_address VARCHAR(255) NOT NULL,
    suppression_type VARCHAR(20) NOT NULL CHECK (suppression_type IN ('BOUNCE', 'COMPLAINT')),
    provider_id VARCHAR(255),
    reason TEXT,
    suppressed_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),

    CONSTRAINT uq_suppressed_addresses_tenant_email UNIQUE (tenant_id, email_address)
);

CREATE INDEX idx_suppressed_addresses_lookup ON suppressed_addresses (tenant_id, email_address);
