-- Add PENDING_VERIFICATION to the identity status check constraint.
-- CockroachDB supports DROP + ADD CONSTRAINT within a single transaction.
ALTER TABLE "identity" DROP CONSTRAINT IF EXISTS chk_identity_status;
ALTER TABLE "identity" ADD CONSTRAINT chk_identity_status CHECK (
    status IN ('PENDING_INVITE', 'ACTIVE', 'SUSPENDED', 'LOCKED', 'PENDING_VERIFICATION')
);
