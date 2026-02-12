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

-- NOTE: Instrument lifecycle enforcement (immutable fields, status transitions,
-- timestamp management) is handled at the application layer in the instrument
-- definition service. CockroachDB does not support PL/pgSQL triggers in
-- user-defined functions. CHECK constraints above provide database-level safety.
