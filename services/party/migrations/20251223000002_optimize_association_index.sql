-- Optimize party_association indexes for common query patterns
-- Queries typically filter by party_id first, then optionally by relationship_type

-- Add composite index for efficient queries filtering on party_id and relationship_type
CREATE INDEX "idx_party_association_party_relationship" ON "party_association" ("party_id", "relationship_type");

-- Drop standalone relationship_type index (subsumed by composite + not useful alone)
DROP INDEX IF EXISTS "idx_party_association_relationship_type";
