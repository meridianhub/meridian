-- Fix audit_outbox status constraint to include 'completed' status
-- The worker sets status to 'completed' after successful processing
-- but the original constraint only allowed 'pending', 'processing', 'failed'

-- Drop the existing constraint and add updated one with 'completed'
ALTER TABLE audit_outbox DROP CONSTRAINT IF EXISTS audit_outbox_status_check;
ALTER TABLE audit_outbox ADD CONSTRAINT audit_outbox_status_check
    CHECK (status IN ('pending', 'processing', 'completed', 'failed'));
