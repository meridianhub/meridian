-- Backfill the revision column (Axis B) for existing supersession chains per ADR-0017.
--
-- Semantics: revision 0 = original observation, 1+ = correction that supersedes an
-- earlier observation. The correction rows are the newer rows that replaced an
-- earlier one. Because superseded_by is a forward pointer (A.superseded_by = B.id
-- means "A was replaced by B"), the correction rows are exactly those whose id
-- appears in some other row's superseded_by column.
--
-- Runs in its own migration so the revision column (added in
-- 20260608000003_add_revision_column.sql) is public before this DML references it.

UPDATE market_price_observation
SET revision = 1
WHERE id IN (
  SELECT superseded_by
  FROM market_price_observation
  WHERE superseded_by IS NOT NULL
);
