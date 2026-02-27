-- atlas:txn false

-- Add CARBON and DATA dimensions to the instrument definition check constraint.
-- These are needed for carbon credit instruments and data-volume instruments.
-- Runs outside a transaction because CockroachDB cannot DROP and re-ADD a
-- constraint with the same name within a single transaction.

ALTER TABLE instrument_definition
  DROP CONSTRAINT "chk_instrument_definition_dimension";

ALTER TABLE instrument_definition
  ADD CONSTRAINT "chk_instrument_definition_dimension"
  CHECK ("dimension" IN ('MONETARY', 'ENERGY', 'QUANTITY', 'COMPUTE', 'TIME', 'MASS', 'VOLUME', 'CARBON', 'DATA'));
