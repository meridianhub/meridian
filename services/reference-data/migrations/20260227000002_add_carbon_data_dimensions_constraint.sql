-- Re-create the dimension check constraint with CARBON and DATA added.
-- These are needed for carbon credit instruments and data-volume instruments.

ALTER TABLE instrument_definition
  ADD CONSTRAINT "chk_instrument_definition_dimension"
  CHECK ("dimension" IN ('MONETARY', 'ENERGY', 'QUANTITY', 'COMPUTE', 'TIME', 'MASS', 'VOLUME', 'CARBON', 'DATA'));
