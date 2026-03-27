-- atlas:txn false
-- Add PENDING_VERIFICATION to the identity status check constraint.
-- CockroachDB requires DROP + recreate for CHECK constraint changes.
ALTER TABLE identities DROP CONSTRAINT IF EXISTS chk_identity_status;
ALTER TABLE identities ADD CONSTRAINT chk_identity_status CHECK (
    status IN ('PENDING_INVITE', 'ACTIVE', 'SUSPENDED', 'LOCKED', 'PENDING_VERIFICATION')
);
