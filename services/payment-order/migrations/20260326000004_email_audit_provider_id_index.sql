CREATE INDEX idx_email_audit_provider ON email_audit_log (provider_id) WHERE provider_id IS NOT NULL;
