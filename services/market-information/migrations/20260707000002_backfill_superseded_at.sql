-- Backfill superseded_at for existing supersession chains.
--
-- superseded_by is a forward pointer (A.superseded_by = B.id means "A was replaced
-- by B"). The knowledge-time at which A stopped being authoritative is when its
-- replacement B was created, so superseded_at is seeded from the replacing row's
-- created_at. Rows that were never superseded keep superseded_at NULL.
--
-- Runs in its own migration so the superseded_at column (added in
-- 20260707000001_add_superseded_at.sql) is public before this DML references it.

UPDATE market_price_observation AS o
SET superseded_at = s.created_at
FROM market_price_observation AS s
WHERE o.superseded_by = s.id
  AND o.superseded_at IS NULL;
