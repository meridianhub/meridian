-- Drop the existing dimension check constraint so it can be re-created
-- with additional values in the next migration file.
-- Split into two files because CockroachDB cannot DROP and re-ADD a
-- constraint with the same name within a single migration/transaction.

ALTER TABLE instrument_definition
  DROP CONSTRAINT "chk_instrument_definition_dimension";
