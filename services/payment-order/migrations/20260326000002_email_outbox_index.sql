CREATE INDEX idx_email_outbox_pending
    ON email_outbox (next_attempt_at)
    WHERE status IN ('PENDING', 'FAILED')
    AND attempts < max_attempts;
