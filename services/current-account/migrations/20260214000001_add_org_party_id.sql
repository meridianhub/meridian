-- Add org_party_id column for org-scoped accounts
ALTER TABLE "account" ADD COLUMN "org_party_id" UUID NULL;

COMMENT ON COLUMN "account"."org_party_id" IS
  'References the organization party for org-scoped accounts. NULL for personal accounts. Used for syndicate-style multi-party account ownership.';
