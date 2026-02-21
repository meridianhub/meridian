-- Add unique index on (tenant_id, party_type) for the party_type_definition table.
-- Separated from table creation per CockroachDB requirements: a partial or unique index
-- on a newly-added column requires the column to be "public" (committed) first.
CREATE UNIQUE INDEX "idx_party_type_def_tenant_type" ON "party_type_definition" ("tenant_id", "party_type");
