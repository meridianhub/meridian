-- Add is_system column to instrument_definition table
-- System instruments (USD, EUR, GBP, etc.) are seeded during tenant provisioning
-- and should be read-only. Application layer enforces this constraint via InstrumentRegistry.
-- NOTE: System instruments are seeded by tenant provisioning service, not by this migration.

-- Add is_system column with default false (tenant-created instruments are not system instruments)
ALTER TABLE "instrument_definition"
  ADD COLUMN "is_system" boolean NOT NULL DEFAULT false;

-- Create index for efficient filtering of system vs tenant instruments
CREATE INDEX "idx_instrument_definition_is_system" ON "instrument_definition" ("is_system");

-- Composite index for listing active system instruments efficiently
CREATE INDEX "idx_instrument_definition_is_system_active" ON "instrument_definition" ("is_system", "code")
  WHERE "status" = 'ACTIVE';
