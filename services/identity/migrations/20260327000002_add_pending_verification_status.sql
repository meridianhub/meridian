-- atlas:txn false
-- Recreate the identity status check constraint with PENDING_VERIFICATION included.
-- This runs as a separate migration from the DROP to avoid CockroachDB's restriction
-- on same-name constraint DROP+ADD within a single transaction.
ALTER TABLE "identity" ADD CONSTRAINT chk_identity_status CHECK (
    status IN ('PENDING_INVITE', 'ACTIVE', 'SUSPENDED', 'LOCKED', 'PENDING_VERIFICATION')
);
