-- Add the revision column for Axis B (lifecycle) of the quality ladder per ADR-0017.
-- revision is orthogonal to quality: it tracks correction lineage independently of
-- the confidence grade. 0 = original observation, 1+ = correction that supersedes an
-- earlier observation.
--
-- No DML in this migration. CockroachDB requires a newly added column to be public
-- before it can be referenced in DML, so the backfill runs in a separate migration
-- (20260608000004_backfill_revision.sql).

ALTER TABLE market_price_observation
  ADD COLUMN revision integer NOT NULL DEFAULT 0;

COMMENT ON COLUMN market_price_observation.revision IS 'Correction lineage counter (Axis B). 0 = original observation; 1+ = correction that supersedes an earlier observation. Orthogonal to quality (Axis A, the confidence grade).';
