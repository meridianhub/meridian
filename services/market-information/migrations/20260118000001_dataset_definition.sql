-- Market Information Service Schema - Dataset Definition and Data Source Tables
-- BIAN Service Domain: Market Information Management
-- Manages price benchmarks, market data feeds, and reference prices
-- Uses unqualified table names (database-per-service architecture)
-- Multi-tenancy: Schema-per-tenant architecture means no tenant_id column needed

-- Create "dataset_definition" table
-- Defines types of market data that can be collected (e.g., FX rates, energy prices)
-- Includes CEL expressions for validation, resolution key generation, and error messages
CREATE TABLE "dataset_definition" (
  "id" uuid NOT NULL DEFAULT gen_random_uuid(),
  "code" character varying(50) NOT NULL,
  "version" integer NOT NULL DEFAULT 1,
  "name" character varying(255) NOT NULL,
  "description" text NULL,
  "data_category" character varying(50) NULL,
  "validation_expression" text NULL,
  "resolution_key_expression" text NULL,
  "error_message_expression" text NULL,
  "attribute_schema" jsonb NULL,
  "status" character varying(20) NOT NULL DEFAULT 'DRAFT',
  "created_at" timestamptz NOT NULL DEFAULT now(),
  "updated_at" timestamptz NOT NULL DEFAULT now(),
  "activated_at" timestamptz NULL,
  "deprecated_at" timestamptz NULL,
  PRIMARY KEY ("id")
);

-- Add validation constraints for dataset_definition
ALTER TABLE "dataset_definition"
  ADD CONSTRAINT "chk_dataset_definition_status"
  CHECK ("status" IN ('DRAFT', 'ACTIVE', 'DEPRECATED'));

-- Limit expression columns to 4KB to prevent abuse
ALTER TABLE "dataset_definition"
  ADD CONSTRAINT "chk_dataset_definition_validation_expression_length"
  CHECK ("validation_expression" IS NULL OR length("validation_expression") <= 4096);

ALTER TABLE "dataset_definition"
  ADD CONSTRAINT "chk_dataset_definition_resolution_key_expression_length"
  CHECK ("resolution_key_expression" IS NULL OR length("resolution_key_expression") <= 4096);

ALTER TABLE "dataset_definition"
  ADD CONSTRAINT "chk_dataset_definition_error_message_length"
  CHECK ("error_message_expression" IS NULL OR length("error_message_expression") <= 4096);

-- Ensure code+version is unique (allows multiple versions of same dataset)
ALTER TABLE "dataset_definition"
  ADD CONSTRAINT "uq_dataset_definition_code_version"
  UNIQUE ("code", "version");

-- Create indexes for efficient lookups
-- Partial index for quickly finding active datasets by code
CREATE INDEX "idx_dataset_definition_code_active" ON "dataset_definition" ("code") WHERE "status" = 'ACTIVE';

-- Index for listing datasets by status
CREATE INDEX "idx_dataset_definition_status" ON "dataset_definition" ("status");

-- Index for temporal queries
CREATE INDEX "idx_dataset_definition_created_at" ON "dataset_definition" ("created_at");

-- Create "data_source" table
-- Represents sources of market data (e.g., ECB, Bloomberg, internal systems)
-- trust_level determines precedence in quality ladder (0=lowest, 100=highest)
CREATE TABLE "data_source" (
  "id" uuid NOT NULL DEFAULT gen_random_uuid(),
  "code" character varying(50) NOT NULL,
  "name" character varying(255) NOT NULL,
  "description" text NULL,
  "trust_level" integer NOT NULL DEFAULT 50,
  "created_at" timestamptz NOT NULL DEFAULT now(),
  "updated_at" timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY ("id")
);

-- Add validation constraints for data_source
ALTER TABLE "data_source"
  ADD CONSTRAINT "chk_data_source_trust_level"
  CHECK ("trust_level" >= 0 AND "trust_level" <= 100);

-- Ensure code is unique
ALTER TABLE "data_source"
  ADD CONSTRAINT "uq_data_source_code"
  UNIQUE ("code");

-- Index for listing data sources by trust level
CREATE INDEX "idx_data_source_trust_level" ON "data_source" ("trust_level" DESC);

-- Trigger function to enforce dataset lifecycle rules
-- Immutable fields cannot be changed once dataset is ACTIVE or DEPRECATED
-- Status transitions: DRAFT can go to ACTIVE or DEPRECATED (for abandoned drafts)
--                     ACTIVE can only go to DEPRECATED
--                     DEPRECATED is terminal (no transitions allowed)
CREATE OR REPLACE FUNCTION "enforce_dataset_lifecycle"()
RETURNS TRIGGER AS $$
BEGIN
  -- Always update updated_at on any change
  NEW."updated_at" = NOW();

  -- Allow all edits when status is DRAFT
  IF OLD."status" = 'DRAFT' THEN
    -- If transitioning from DRAFT to ACTIVE, set activated_at
    IF NEW."status" = 'ACTIVE' THEN
      NEW."activated_at" = NOW();
    -- Allow DRAFT to DEPRECATED for abandoned drafts (skips ACTIVE)
    ELSIF NEW."status" = 'DEPRECATED' THEN
      NEW."deprecated_at" = NOW();
    END IF;
    RETURN NEW;
  END IF;

  -- For ACTIVE or DEPRECATED datasets, certain fields become immutable
  IF OLD."status" IN ('ACTIVE', 'DEPRECATED') THEN
    -- Prevent changes to immutable fields (validation rules that affect data integrity)
    IF OLD."validation_expression" IS DISTINCT FROM NEW."validation_expression" THEN
      RAISE EXCEPTION 'Cannot modify validation_expression for % dataset', OLD."status";
    END IF;
    IF OLD."resolution_key_expression" IS DISTINCT FROM NEW."resolution_key_expression" THEN
      RAISE EXCEPTION 'Cannot modify resolution_key_expression for % dataset', OLD."status";
    END IF;
    IF OLD."error_message_expression" IS DISTINCT FROM NEW."error_message_expression" THEN
      RAISE EXCEPTION 'Cannot modify error_message_expression for % dataset', OLD."status";
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
CREATE TRIGGER "trg_enforce_dataset_lifecycle"
  BEFORE UPDATE ON "dataset_definition"
  FOR EACH ROW
  EXECUTE FUNCTION "enforce_dataset_lifecycle"();

-- Trigger function to update updated_at on data_source changes
CREATE OR REPLACE FUNCTION "update_data_source_timestamp"()
RETURNS TRIGGER AS $$
BEGIN
  NEW."updated_at" = NOW();
  RETURN NEW;
END;
$$ LANGUAGE plpgsql;

-- Create trigger for data_source updated_at
CREATE TRIGGER "trg_update_data_source_timestamp"
  BEFORE UPDATE ON "data_source"
  FOR EACH ROW
  EXECUTE FUNCTION "update_data_source_timestamp"();
