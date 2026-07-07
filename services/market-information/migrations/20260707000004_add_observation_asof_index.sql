-- Add a non-partial index supporting the bi-temporal as-of observation lookup.
--
-- RetrieveObservation / GetLatest filter by (dataset_definition_id, resolution_key)
-- then apply the created_at <= $t and (superseded_at IS NULL OR superseded_at > $t)
-- as-of predicate, ordering by quality DESC, observed_at DESC. Since that predicate
-- no longer implies superseded_by IS NULL, the partial index
-- idx_observation_resolution_bitemporal (WHERE superseded_by IS NULL) can no longer
-- serve it, which would turn the present-time hot path (kbt = now) into a scan of
-- every historical version for a resolution key.
--
-- This non-partial composite lets the planner seek by dataset + resolution_key and
-- walk quality/observed_at order, applying the created_at/superseded_at filters
-- against the bounded per-key version set. dataset_definition_id, resolution_key,
-- quality, and observed_at are existing columns; no split is required.
-- CONCURRENTLY is omitted: CockroachDB builds all indexes online by default.

CREATE INDEX IF NOT EXISTS idx_observation_resolution_asof
  ON market_price_observation (dataset_definition_id, resolution_key, quality DESC, observed_at DESC);
