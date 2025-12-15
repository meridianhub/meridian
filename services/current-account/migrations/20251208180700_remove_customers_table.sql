-- Migration: Remove customers table and FK constraint
-- This migration removes the local customers table as customer data is now managed
-- by the Party Service (accessed via gRPC). The customer_id column is kept for now
-- and will be renamed to party_id in a subsequent migration along with Go code updates.

-- Drop FK constraint from accounts table (IF EXISTS for idempotency with CockroachDB)
ALTER TABLE "current_account"."accounts"
    DROP CONSTRAINT IF EXISTS "fk_current_account_customers_accounts";

-- Add comment documenting that customer_id now references Party Service via gRPC (not FK)
COMMENT ON COLUMN "current_account"."accounts"."customer_id" IS
    'References a party in the Party Service (accessed via gRPC). Not a foreign key constraint as Party Service is a separate microservice. Will be renamed to party_id in a future migration.';

-- Drop the customers table and its indexes (indexes are dropped automatically with the table)
DROP TABLE "current_account"."customers";
