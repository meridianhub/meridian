-- Index for org-scoped internal bank account queries
-- Separate migration from column addition for CockroachDB compatibility:
-- column must be "public" before a partial index can reference it.
CREATE INDEX "idx_internal_bank_account_org_party" ON "internal_bank_account" ("org_party_id") WHERE "org_party_id" IS NOT NULL;
