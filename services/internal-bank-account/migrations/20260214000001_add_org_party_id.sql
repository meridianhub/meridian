-- Add org_party_id column for org-scoped internal bank accounts
-- CockroachDB compatibility: Column must be committed ("public") before partial indexes
-- can reference it, so the index is in a separate migration file.
ALTER TABLE "internal_bank_account" ADD COLUMN "org_party_id" UUID NULL;

COMMENT ON COLUMN "internal_bank_account"."org_party_id" IS
  'References the organization party for org-scoped accounts. NULL for global accounts. Org-scoped accounts CANNOT be CLEARING type (enforced at application layer).';
