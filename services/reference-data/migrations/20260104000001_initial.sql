-- Reference Data Service Schema
-- Reference Data is independent per BIAN domain (ADR-002) - no cross-schema dependencies
-- Uses unqualified table names (relies on database-per-service architecture)
-- Multi-tenancy: Schema-per-tenant architecture means no tenant_id column needed
-- Each tenant's database connection routes to their isolated schema

-- Create "instrument_definition" table (singular, unqualified)
-- Defines measurement units, currencies, and asset types that can be tracked in the ledger
CREATE TABLE "instrument_definition" (
  "id" uuid NOT NULL DEFAULT gen_random_uuid(),
  "code" character varying(50) NOT NULL,
  "version" integer NOT NULL,
  "dimension" character varying(20) NOT NULL,
  "precision" integer NOT NULL,
  "status" character varying(20) NOT NULL DEFAULT 'DRAFT',
  "validation_expression" text NULL,
  "fungibility_key_expression" text NOT NULL DEFAULT '',
  "error_message_expression" text NULL,
  "attribute_schema" jsonb NULL,
  "display_name" character varying(255) NULL,
  "description" text NULL,
  "created_at" timestamptz NOT NULL DEFAULT now(),
  "updated_at" timestamptz NOT NULL DEFAULT now(),
  "activated_at" timestamptz NULL,
  "deprecated_at" timestamptz NULL,
  PRIMARY KEY ("id")
);

-- Add validation constraints
ALTER TABLE "instrument_definition"
  ADD CONSTRAINT "chk_instrument_definition_dimension"
  CHECK ("dimension" IN ('MONETARY', 'ENERGY', 'QUANTITY', 'COMPUTE', 'TIME', 'MASS', 'VOLUME'));

ALTER TABLE "instrument_definition"
  ADD CONSTRAINT "chk_instrument_definition_precision"
  CHECK ("precision" >= 0 AND "precision" <= 18);

ALTER TABLE "instrument_definition"
  ADD CONSTRAINT "chk_instrument_definition_status"
  CHECK ("status" IN ('DRAFT', 'ACTIVE', 'DEPRECATED'));

-- Limit expression columns to 4KB to prevent abuse
ALTER TABLE "instrument_definition"
  ADD CONSTRAINT "chk_instrument_definition_validation_expression_length"
  CHECK ("validation_expression" IS NULL OR length("validation_expression") <= 4096);

ALTER TABLE "instrument_definition"
  ADD CONSTRAINT "chk_instrument_definition_fungibility_expression_length"
  CHECK (length("fungibility_key_expression") <= 4096);

ALTER TABLE "instrument_definition"
  ADD CONSTRAINT "chk_instrument_definition_error_message_length"
  CHECK ("error_message_expression" IS NULL OR length("error_message_expression") <= 4096);

-- Ensure code+version is unique (allows multiple versions of same instrument)
ALTER TABLE "instrument_definition"
  ADD CONSTRAINT "uq_instrument_definition_code_version"
  UNIQUE ("code", "version");

-- Create indexes for efficient lookups
-- Note: (code, version) index not needed - unique constraint creates implicit index

-- Partial index for quickly finding active instruments by code
CREATE INDEX "idx_instrument_definition_code_active" ON "instrument_definition" ("code") WHERE "status" = 'ACTIVE';

-- Index for listing instruments by status
CREATE INDEX "idx_instrument_definition_status" ON "instrument_definition" ("status");

-- Index for temporal queries
CREATE INDEX "idx_instrument_definition_created_at" ON "instrument_definition" ("created_at");

-- Trigger function to enforce instrument lifecycle rules
-- Immutable fields cannot be changed once instrument is ACTIVE or DEPRECATED
-- Status transitions are strictly controlled
CREATE OR REPLACE FUNCTION "enforce_instrument_lifecycle"()
RETURNS TRIGGER AS $$
BEGIN
  -- Always update updated_at on any change
  NEW."updated_at" = NOW();

  -- Allow all edits when status is DRAFT
  IF OLD."status" = 'DRAFT' THEN
    -- If transitioning from DRAFT to ACTIVE, set activated_at
    IF NEW."status" = 'ACTIVE' THEN
      NEW."activated_at" = NOW();
    -- If transitioning from DRAFT to DEPRECATED, set deprecated_at
    ELSIF NEW."status" = 'DEPRECATED' THEN
      NEW."deprecated_at" = NOW();
    END IF;
    RETURN NEW;
  END IF;

  -- For ACTIVE or DEPRECATED instruments, certain fields become immutable
  IF OLD."status" IN ('ACTIVE', 'DEPRECATED') THEN
    -- Prevent changes to immutable fields (validation rules that affect ledger integrity)
    IF OLD."validation_expression" IS DISTINCT FROM NEW."validation_expression" THEN
      RAISE EXCEPTION 'Cannot modify validation_expression for % instrument', OLD."status";
    END IF;
    IF OLD."fungibility_key_expression" IS DISTINCT FROM NEW."fungibility_key_expression" THEN
      RAISE EXCEPTION 'Cannot modify fungibility_key_expression for % instrument', OLD."status";
    END IF;
    IF OLD."error_message_expression" IS DISTINCT FROM NEW."error_message_expression" THEN
      RAISE EXCEPTION 'Cannot modify error_message_expression for % instrument', OLD."status";
    END IF;
  END IF;

  -- Prevent invalid status transitions
  IF OLD."status" = 'ACTIVE' THEN
    IF NEW."status" = 'DRAFT' THEN
      RAISE EXCEPTION 'Cannot transition from ACTIVE back to DRAFT';
    END IF;
    -- Allow ACTIVE to DEPRECATED, set deprecated_at
    IF NEW."status" = 'DEPRECATED' THEN
      NEW."deprecated_at" = NOW();
    END IF;
  END IF;

  IF OLD."status" = 'DEPRECATED' THEN
    IF NEW."status" IN ('DRAFT', 'ACTIVE') THEN
      RAISE EXCEPTION 'Cannot transition from DEPRECATED to %', NEW."status";
    END IF;
  END IF;

  RETURN NEW;
END;
$$ LANGUAGE plpgsql;

-- Create trigger to enforce lifecycle rules on UPDATE
CREATE TRIGGER "trg_enforce_instrument_lifecycle"
  BEFORE UPDATE ON "instrument_definition"
  FOR EACH ROW
  EXECUTE FUNCTION "enforce_instrument_lifecycle"();
