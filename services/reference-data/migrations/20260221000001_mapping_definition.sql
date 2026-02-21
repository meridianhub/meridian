-- MappingDefinition: versioned registry of external-to-internal mapping schemas.
-- Supports DRAFT → ACTIVE → DEPRECATED lifecycle per tenant.
-- JSONB columns store structured data (fields, computed_fields, idempotency).
-- tenant_id is stored as a column (multi-tenant, schema-per-tenant architecture).

CREATE TABLE "mapping_definition" (
  "id" uuid NOT NULL DEFAULT gen_random_uuid(),
  "tenant_id" text NOT NULL,
  "name" character varying(255) NOT NULL,
  "target_service" character varying(255) NOT NULL,
  "target_rpc" character varying(128) NOT NULL,
  "version" integer NOT NULL DEFAULT 1,
  "status" character varying(20) NOT NULL DEFAULT 'DRAFT',
  "external_schema" text NULL,
  "fields" jsonb NOT NULL DEFAULT '[]',
  "inbound_computed_fields" jsonb NOT NULL DEFAULT '[]',
  "outbound_computed_fields" jsonb NOT NULL DEFAULT '[]',
  "inbound_validation_cel" text NULL,
  "outbound_validation_cel" text NULL,
  "is_batch" boolean NOT NULL DEFAULT false,
  "batch_target_path" character varying(512) NULL,
  "idempotency" jsonb NULL,
  "created_at" timestamptz NOT NULL DEFAULT now(),
  "updated_at" timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY ("id"),
  CONSTRAINT "uq_mapping_tenant_name_version"
    UNIQUE ("tenant_id", "name", "version"),
  CONSTRAINT "chk_mapping_status"
    CHECK ("status" IN ('DRAFT', 'ACTIVE', 'DEPRECATED')),
  CONSTRAINT "chk_mapping_version_positive"
    CHECK ("version" >= 1)
);

-- Index for listing mappings by tenant and status
CREATE INDEX "idx_mapping_tenant_status" ON "mapping_definition" ("tenant_id", "status");

-- Index for looking up all versions of a mapping by name
CREATE INDEX "idx_mapping_tenant_name" ON "mapping_definition" ("tenant_id", "name");

-- NOTE: Lifecycle enforcement (status transitions, immutable fields, timestamp management)
-- is handled at the application layer. CockroachDB does not support PL/pgSQL triggers.
-- CHECK constraints above provide database-level safety.
-- Partial unique index (only one ACTIVE version per tenant+name) is in a separate migration
-- per CockroachDB requirement: partial indexes require the table to be fully committed first.
