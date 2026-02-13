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

-- NOTE: Saga lifecycle enforcement (successor_id write-once semantics, status
-- transitions, immutable field protection) is handled at the application layer.
-- CockroachDB does not support PL/pgSQL triggers in user-defined functions.
-- CHECK constraints above provide database-level safety.
