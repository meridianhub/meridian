-- Recreate the unique partial index on instrument_code (replacing the one on currency
-- that was dropped in 20260225000001 before the column backfill).
-- A party can have at most one org-scoped account per instrument within an organization.

CREATE UNIQUE INDEX "idx_account_syndicate_scope_integrity" ON "account" ("party_id", "org_party_id", "instrument_code") WHERE "org_party_id" IS NOT NULL;
