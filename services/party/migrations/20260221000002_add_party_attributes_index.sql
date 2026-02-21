-- GIN index on party.attributes to support efficient JSONB containment queries
-- Must be a separate migration from the column addition (CockroachDB requires column to be public first)
CREATE INDEX "idx_party_attributes" ON "party" USING GIN("attributes");
