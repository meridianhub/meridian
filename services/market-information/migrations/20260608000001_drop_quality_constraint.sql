-- Drop the three-level quality CHECK constraint ahead of widening it to the
-- four-level confidence ladder (ADR-0017).
--
-- CockroachDB rejects dropping and re-adding a constraint of the same name within
-- a single transaction ("duplicate constraint name", SQLSTATE 42710). The re-add
-- therefore lives in a separate migration (20260608000002_quality_level_verified.sql)
-- so the drop commits first.

ALTER TABLE market_price_observation
  DROP CONSTRAINT chk_observation_quality;
