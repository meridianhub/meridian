CREATE TABLE email_audit_log (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id VARCHAR(255) NOT NULL,
    outbox_id UUID NOT NULL,
    provider_id VARCHAR(255),
    to_addresses TEXT[] NOT NULL,
    from_address VARCHAR(255) NOT NULL,
    subject VARCHAR(500) NOT NULL,
    template_name VARCHAR(100) NOT NULL,
    status VARCHAR(20) NOT NULL DEFAULT 'SENT',
    sent_at TIMESTAMPTZ,
    delivered_at TIMESTAMPTZ,
    bounce_reason TEXT,
    provider_response JSONB,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_email_audit_tenant ON email_audit_log (tenant_id, created_at DESC);
CREATE INDEX idx_email_audit_outbox ON email_audit_log (outbox_id);
