-- Deferred index creation for successor_id column added in 20260106000001.
-- CockroachDB requires the column to be "public" (committed in a prior
-- transaction) before a partial index can reference it.

-- Index for efficient lineage traversal (finding all instruments that point to a given successor)
CREATE INDEX "idx_instrument_definition_successor_id" ON "instrument_definition" ("successor_id")
  WHERE "successor_id" IS NOT NULL;
