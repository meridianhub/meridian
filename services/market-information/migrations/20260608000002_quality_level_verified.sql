-- Widen the quality CHECK constraint to admit a fourth confidence level (Axis A)
-- ahead of the domain cutover to the four-level quality ladder (ADR-0017).
-- The prior constraint allowed only 1, 2, 3. Existing rows use values within that
-- range, so re-adding the widened constraint validates cleanly.
--
-- The matching DROP runs in 20260608000001_drop_quality_constraint.sql; splitting
-- the two avoids CockroachDB's same-transaction duplicate-constraint-name error.
--
-- IMPORTANT: this migration widens the constraint only; it does NOT remap data.
-- The service currently writes the legacy three-level domain encoding
-- (1=ESTIMATE, 2=ACTUAL, 3=VERIFIED; see domain/quality_level.go), so value 4 is
-- not yet emitted by application code. The cutover to the proto four-level ladder
-- (1=ESTIMATE, 2=PROVISIONAL, 3=ACTUAL, 4=REVISED; see the QualityLevel proto enum)
-- and the remap of existing rows are handled in a separate task so legacy rows are
-- not silently relabelled. The comment below documents the legacy encoding the
-- column holds today, not the post-cutover meaning.

ALTER TABLE market_price_observation
  ADD CONSTRAINT chk_observation_quality
  CHECK (quality IN (1, 2, 3, 4));

COMMENT ON TABLE market_price_observation IS 'Bi-temporal market price observations with supersession tracking per ADR-0017. The quality column (Axis A, confidence) has its CHECK constraint widened to IN (1,2,3,4) to admit the four-level ladder; rows are currently written with the legacy three-level domain encoding (1=ESTIMATE, 2=ACTUAL, 3=VERIFIED). The cutover to the proto four-level QualityLevel ladder (1=ESTIMATE, 2=PROVISIONAL, 3=ACTUAL, 4=REVISED) and the remap of existing rows land in a separate task. Quality determines confidence/precedence within a source; for cross-source, combine with data_source.trust_level. Lifecycle corrections are tracked separately via the revision column.';
