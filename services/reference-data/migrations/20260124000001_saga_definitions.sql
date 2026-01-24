-- Saga Definition Storage
-- Implements FR-1: Saga definitions stored in Reference Data with lifecycle management
-- Multi-tenancy: Schema-per-tenant architecture means no tenant_id column needed
-- Each tenant's database connection routes to their isolated schema

-- Create "saga_definition" table (singular, unqualified)
-- Stores Starlark saga definitions with lifecycle management (DRAFT/ACTIVE/DEPRECATED)
CREATE TABLE "saga_definition" (
  "id" uuid NOT NULL DEFAULT gen_random_uuid(),
  "name" character varying(64) NOT NULL,
  "version" integer NOT NULL DEFAULT 1,
  "script" text NOT NULL,
  "status" character varying(16) NOT NULL DEFAULT 'DRAFT',
  "is_system" boolean NOT NULL DEFAULT FALSE,
  "preconditions_expression" text NULL,
  "display_name" character varying(128) NULL,
  "description" text NULL,
  "created_at" timestamptz NOT NULL DEFAULT now(),
  "updated_at" timestamptz NOT NULL DEFAULT now(),
  "activated_at" timestamptz NULL,
  "deprecated_at" timestamptz NULL,
  "successor_id" uuid NULL,
  PRIMARY KEY ("id")
);

-- Add validation constraints
ALTER TABLE "saga_definition"
  ADD CONSTRAINT "chk_saga_definition_status"
  CHECK ("status" IN ('DRAFT', 'ACTIVE', 'DEPRECATED'));

-- Limit script to 64KB to prevent abuse
ALTER TABLE "saga_definition"
  ADD CONSTRAINT "chk_saga_definition_script_length"
  CHECK (length("script") <= 65536);

-- Limit preconditions expression to 4KB
ALTER TABLE "saga_definition"
  ADD CONSTRAINT "chk_saga_definition_preconditions_length"
  CHECK ("preconditions_expression" IS NULL OR length("preconditions_expression") <= 4096);

-- Ensure name+version is unique (allows multiple versions of same saga)
ALTER TABLE "saga_definition"
  ADD CONSTRAINT "uq_saga_definition_name_version"
  UNIQUE ("name", "version");

-- Self-referential foreign key for successor lineage
ALTER TABLE "saga_definition"
  ADD CONSTRAINT "fk_saga_definition_successor"
  FOREIGN KEY ("successor_id") REFERENCES "saga_definition" ("id");

-- Create indexes for efficient lookups
-- Partial index for quickly finding active sagas by name
CREATE INDEX "idx_saga_definition_name_active" ON "saga_definition" ("name") WHERE "status" = 'ACTIVE';

-- Index for listing sagas by name and status (tenant default resolution)
CREATE INDEX "idx_saga_definition_lookup" ON "saga_definition" ("name", "status");

-- Index for bi-temporal queries: What saga was active at a given point in time?
CREATE INDEX "idx_saga_definition_temporal" ON "saga_definition" ("name", "activated_at", "deprecated_at");

-- Index for successor lineage traversal
CREATE INDEX "idx_saga_definition_successor_id" ON "saga_definition" ("successor_id")
  WHERE "successor_id" IS NOT NULL;

-- Trigger function to enforce saga lifecycle rules
-- Immutable fields cannot be changed once saga is ACTIVE or DEPRECATED
-- Status transitions are strictly controlled
CREATE OR REPLACE FUNCTION "enforce_saga_lifecycle"()
RETURNS TRIGGER AS $$
DECLARE
  successor_record RECORD;
BEGIN
  -- Always update updated_at on any change
  NEW."updated_at" = NOW();

  -- Enforce write-once semantics for successor_id
  -- Once set, successor_id cannot be changed (regardless of status)
  IF OLD."successor_id" IS NOT NULL AND OLD."successor_id" IS DISTINCT FROM NEW."successor_id" THEN
    RAISE EXCEPTION 'Cannot modify successor_id once set (write-once semantics)';
  END IF;

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

  -- For ACTIVE or DEPRECATED sagas, script and preconditions become immutable
  IF OLD."status" IN ('ACTIVE', 'DEPRECATED') THEN
    -- Prevent changes to immutable fields (workflow logic that affects execution)
    IF OLD."script" IS DISTINCT FROM NEW."script" THEN
      RAISE EXCEPTION 'Cannot modify script for % saga', OLD."status";
    END IF;
    IF OLD."preconditions_expression" IS DISTINCT FROM NEW."preconditions_expression" THEN
      RAISE EXCEPTION 'Cannot modify preconditions_expression for % saga', OLD."status";
    END IF;
  END IF;

  -- Prevent invalid status transitions
  IF OLD."status" = 'ACTIVE' THEN
    IF NEW."status" = 'DRAFT' THEN
      RAISE EXCEPTION 'Cannot transition from ACTIVE back to DRAFT';
    END IF;
    -- Allow ACTIVE to DEPRECATED, set deprecated_at and validate successor
    IF NEW."status" = 'DEPRECATED' THEN
      NEW."deprecated_at" = NOW();

      -- If successor_id is provided, validate it
      IF NEW."successor_id" IS NOT NULL THEN
        -- Cannot designate self as successor
        IF NEW."successor_id" = NEW."id" THEN
          RAISE EXCEPTION 'Saga cannot designate itself as its own successor';
        END IF;

        -- Look up the successor saga
        SELECT "id", "status", "name"
        INTO successor_record
        FROM "saga_definition"
        WHERE "id" = NEW."successor_id";

        -- Successor must exist
        IF successor_record.id IS NULL THEN
          RAISE EXCEPTION 'Successor saga does not exist: %', NEW."successor_id";
        END IF;

        -- Successor must be ACTIVE
        IF successor_record.status != 'ACTIVE' THEN
          RAISE EXCEPTION 'Successor saga must be ACTIVE, but is %', successor_record.status;
        END IF;

        -- Successor must have same name (it's a newer version of the same saga)
        IF successor_record.name != NEW."name" THEN
          RAISE EXCEPTION 'Successor saga name (%) must match current saga name (%)',
            successor_record.name, NEW."name";
        END IF;
      END IF;
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
CREATE TRIGGER "trg_enforce_saga_lifecycle"
  BEFORE UPDATE ON "saga_definition"
  FOR EACH ROW
  EXECUTE FUNCTION "enforce_saga_lifecycle"();
