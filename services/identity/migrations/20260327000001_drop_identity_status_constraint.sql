-- atlas:txn false
-- Drop the existing identity status check constraint so it can be recreated
-- with the PENDING_VERIFICATION value in the next migration.
-- CockroachDB does not allow DROP + ADD of the same constraint name in one transaction.
ALTER TABLE "identity" DROP CONSTRAINT "chk_identity_status";
