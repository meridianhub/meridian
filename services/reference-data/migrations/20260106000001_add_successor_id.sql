-- Add successor_id column for instrument lineage tracking
-- When an instrument is DEPRECATED, it can optionally point to its successor (replacement) instrument.
-- This enables clients to follow the lineage chain to find the current active instrument.

-- Add successor_id column (nullable - not all deprecated instruments have successors)
ALTER TABLE "instrument_definition"
  ADD COLUMN "successor_id" uuid NULL;

-- Add self-referential foreign key constraint
ALTER TABLE "instrument_definition"
  ADD CONSTRAINT "fk_instrument_definition_successor"
  FOREIGN KEY ("successor_id") REFERENCES "instrument_definition" ("id");

-- NOTE: Partial index on successor_id deferred to 20260106000002 because CockroachDB
-- cannot create a partial index on a column added in the same transaction.

-- NOTE: Instrument lifecycle enforcement (including successor_id write-once semantics,
-- status transitions, and immutable field protection) is handled at the application
-- layer. CockroachDB does not support PL/pgSQL triggers in user-defined functions.
