CREATE TABLE IF NOT EXISTS communication_preferences (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id VARCHAR(255) NOT NULL,
    party_id VARCHAR(255) NOT NULL,
    channel VARCHAR(50) NOT NULL,
    category VARCHAR(50) NOT NULL CHECK (category IN ('TRANSACTIONAL', 'OPERATIONAL', 'MARKETING')),
    opted_in BOOLEAN NOT NULL,
    consent_source VARCHAR(255) NOT NULL,
    consent_granted_at TIMESTAMPTZ NOT NULL,
    consent_text TEXT NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),

    CONSTRAINT uq_comm_pref_party_channel_category UNIQUE (tenant_id, party_id, channel, category)
);

CREATE TABLE IF NOT EXISTS party_global_unsubscribe (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id VARCHAR(255) NOT NULL,
    party_id VARCHAR(255) NOT NULL,
    unsubscribed BOOLEAN NOT NULL DEFAULT false,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),

    CONSTRAINT uq_party_global_unsub UNIQUE (tenant_id, party_id)
);

CREATE INDEX idx_comm_pref_party ON communication_preferences (tenant_id, party_id);
