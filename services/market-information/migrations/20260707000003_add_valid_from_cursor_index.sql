-- Add a cursor index keyed on valid_from for observation list pagination.
--
-- ListObservations pages by valid_from (business/event time) DESC so downstream
-- consumers such as the forecasting MDS adapter receive observations ordered by
-- the time the value applies rather than the time the system recorded it. The
-- (valid_from DESC, id DESC) composite matches the ORDER BY and the cursor
-- predicate, giving stable pagination without a full sort.
--
-- valid_from and id are existing columns, so no split from a same-transaction
-- add-column is required. CONCURRENTLY is omitted: CockroachDB builds all indexes
-- online by default.

CREATE INDEX IF NOT EXISTS idx_market_price_observation_valid_from_cursor
  ON market_price_observation (valid_from DESC, id DESC);
