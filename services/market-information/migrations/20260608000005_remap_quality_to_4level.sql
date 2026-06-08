-- Re-encode existing quality values from the legacy three-level domain encoding
-- to the four-level confidence ladder (Axis A) per ADR-0017.
--
-- This migration is the data half of the Wave 2 cutover. The widened CHECK
-- (IN (1,2,3,4)) landed in 20260608000002_quality_level_verified.sql, but the
-- stored integers still carry the LEGACY meaning:
--   legacy: 1=ESTIMATE, 2=ACTUAL,      3=VERIFIED
-- The new domain/proto encoding the application now reads is:
--   new:    1=ESTIMATE, 2=PROVISIONAL, 3=ACTUAL,     4=VERIFIED
-- Without this remap every stored 2 would silently read as PROVISIONAL and every
-- stored 3 as ACTUAL: a silent confidence downgrade. The code flip (domain enum +
-- lossless adapter) ships in the same PR, so the re-encode and the read change are
-- atomic.
--
-- Mapping: 1 stays 1 (ESTIMATE unchanged), 2 (legacy ACTUAL) -> 3 (ACTUAL),
-- 3 (legacy VERIFIED) -> 4 (VERIFIED). No legacy row uses the new PROVISIONAL(2).
--
-- Order matters: re-encode 3->4 BEFORE 2->3, otherwise the 2->3 step would create
-- new value-3 rows that the 3->4 step would then wrongly promote to 4. Doing 3->4
-- first clears value 3 so the subsequent 2->3 cannot collide.
--
-- CockroachDB: plain UPDATEs, no plpgsql, no CONCURRENTLY. The CHECK already
-- admits 1-4, so both intermediate and final states validate.

UPDATE market_price_observation SET quality = 4 WHERE quality = 3;
UPDATE market_price_observation SET quality = 3 WHERE quality = 2;
