-- Add the superseded_at column to record WHEN an observation was replaced.
--
-- superseded_by is a forward pointer (knowledge lineage); superseded_at is the
-- knowledge-time at which the replacement took effect. It enables bi-temporal
-- "as-of" queries that reconstruct the authoritative observation at a past
-- knowledge time: a record was authoritative at time T if it was created at or
-- before T and either was never superseded or was superseded strictly after T.
--
-- No DML in this migration. CockroachDB requires a newly added column to be public
-- before it can be referenced in DML, so the backfill runs in a separate migration
-- (20260707000002_backfill_superseded_at.sql).

ALTER TABLE market_price_observation
  ADD COLUMN superseded_at timestamptz NULL;

COMMENT ON COLUMN market_price_observation.superseded_at IS 'Knowledge-time at which this observation was replaced by a higher-quality observation for the same dataset, resolution key, and period. NULL while the observation remains authoritative. Enables bi-temporal as-of queries.';
