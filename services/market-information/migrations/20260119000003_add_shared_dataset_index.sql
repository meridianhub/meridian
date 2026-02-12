-- Deferred index creation for is_shared column added in 20260119000002.
-- CockroachDB requires the column to be "public" (committed in a prior
-- transaction) before a partial index can reference it.

CREATE INDEX idx_dataset_definition_is_shared ON dataset_definition (is_shared) WHERE is_shared = TRUE;
