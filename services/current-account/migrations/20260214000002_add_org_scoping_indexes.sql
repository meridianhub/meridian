-- Indexes for org-scoped account queries (column must be public first - separate migration)

-- Lookup: find all org-scoped accounts for a given participant
CREATE INDEX "idx_account_participant_syndicate" ON "account" ("party_id", "org_party_id") WHERE "org_party_id" IS NOT NULL;

-- Lookup: find all participants in a given org-scoped syndicate
CREATE INDEX "idx_account_syndicate_participants" ON "account" ("org_party_id") WHERE "org_party_id" IS NOT NULL;

-- Constraint: a party can only have one account per currency within an org scope
CREATE UNIQUE INDEX "idx_account_syndicate_scope_integrity" ON "account" ("party_id", "org_party_id", "currency") WHERE "org_party_id" IS NOT NULL;
