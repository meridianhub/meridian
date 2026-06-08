-- Widen the quality ladder to four confidence levels per ADR-0017.
-- Axis A (confidence): ESTIMATE(1) -> PROVISIONAL(2) -> ACTUAL(3) -> VERIFIED(4).
-- The prior constraint allowed only 1, 2, 3. Existing rows use values within that
-- range, so re-adding the widened constraint validates cleanly.
--
-- The matching DROP runs in 20260608000001_drop_quality_constraint.sql; splitting
-- the two avoids CockroachDB's same-transaction duplicate-constraint-name error.

ALTER TABLE market_price_observation
  ADD CONSTRAINT chk_observation_quality
  CHECK (quality IN (1, 2, 3, 4));

COMMENT ON TABLE market_price_observation IS 'Bi-temporal market price observations with a four-level quality ladder (1=ESTIMATE, 2=PROVISIONAL, 3=ACTUAL, 4=VERIFIED) and supersession tracking per ADR-0017. Quality determines confidence/precedence within same source; for cross-source, combine with data_source.trust_level. Lifecycle revisions are tracked separately via the revision column.';
