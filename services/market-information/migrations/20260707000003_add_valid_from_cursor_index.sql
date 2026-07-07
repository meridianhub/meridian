-- Add a cursor index keyed on valid_from for observation list pagination.
--
-- ListObservations pages by valid_from (business/event time) DESC so downstream
-- consumers such as the forecasting MDS adapter receive observations ordered by
-- the time the value applies rather than the time the system recorded it. The
-- (valid_from DESC, id DESC) composite backs that ORDER BY and cursor predicate.
--
-- Raw-column index by design (mirrors idx_market_price_observation_cursor on
-- created_at, see 20260123000003_add_cursor_pagination_indexes.sql): the query
-- orders on date_trunc('second', valid_from) to match the second-granular cursor
-- token, but CockroachDB does not support context-dependent functions such as
-- date_trunc() in expression indexes. A plain (valid_from DESC, id DESC) index is
-- therefore the intended, equivalent-ordering choice - date_trunc is monotonic in
-- valid_from, so the index still serves the truncated sort (observation valid_from
-- values carry no sub-second component in practice).
--
-- valid_from and id are existing columns, so no split from a same-transaction
-- add-column is required. CONCURRENTLY is omitted: CockroachDB builds all indexes
-- online by default (the Squawk require-concurrent-index-creation hint is a
-- PostgreSQL-oriented false positive here).

CREATE INDEX IF NOT EXISTS idx_market_price_observation_valid_from_cursor
  ON market_price_observation (valid_from DESC, id DESC);
