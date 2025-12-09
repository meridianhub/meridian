-- Migration: Rename customer_id to party_id
-- This migration completes the transition from local customer management to Party Service.
-- The customer_id column is renamed to party_id to better reflect that it references
-- a party in the Party Service (accessed via gRPC).

-- Rename the column
ALTER TABLE "current_account"."accounts"
    RENAME COLUMN "customer_id" TO "party_id";

-- Drop the old index and create a new one with the correct name
DROP INDEX IF EXISTS "current_account"."idx_current_account_accounts_customer_id";
CREATE INDEX "idx_current_account_accounts_party_id" ON "current_account"."accounts" ("party_id");

-- Update the column comment
COMMENT ON COLUMN "current_account"."accounts"."party_id" IS
    'References a party in the Party Service (accessed via gRPC). Not a foreign key constraint as Party Service is a separate microservice.';
