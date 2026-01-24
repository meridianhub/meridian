-- Saga Reference Tracking
-- Implements FR-9 and FR-10: Reference validation and deprecation impact analysis
-- Tracks all external references made by saga definitions for validation and impact analysis

-- Create "saga_reference" table (singular, unqualified)
-- Stores extracted references from saga definitions for impact analysis
CREATE TABLE "saga_reference" (
  "saga_definition_id" uuid NOT NULL,
  "reference_type" character varying(32) NOT NULL,
  "reference_key" character varying(128) NOT NULL,
  "instrument_code" character varying(50) NULL,
  "attribute_key" character varying(128) NULL,
  "line_number" integer NULL,
  "extracted_at" timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY ("saga_definition_id", "reference_type", "reference_key"),
  CONSTRAINT "fk_saga_reference_definition"
    FOREIGN KEY ("saga_definition_id") REFERENCES "saga_definition" ("id") ON DELETE CASCADE
);

-- Add validation constraints for reference_type
ALTER TABLE "saga_reference"
  ADD CONSTRAINT "chk_saga_reference_type"
  CHECK ("reference_type" IN ('step_handler', 'instrument', 'account', 'saga', 'attribute'));

-- Query: What sagas reference this target (instrument, account, saga, etc.)?
CREATE INDEX "idx_saga_reference_by_target"
  ON "saga_reference" ("reference_type", "reference_key");

-- Query: What does this saga reference?
CREATE INDEX "idx_saga_reference_by_saga"
  ON "saga_reference" ("saga_definition_id");

-- Query: Which sagas reference a specific instrument's attribute?
-- Used for attribute schema change impact analysis
CREATE INDEX "idx_saga_reference_attribute"
  ON "saga_reference" ("instrument_code", "attribute_key")
  WHERE "reference_type" = 'attribute';
