-- Saga Definitions table for version-pinned saga execution.
--
-- Each row captures the script that an in-flight saga instance was started with,
-- so the resume path can re-execute that exact script even after the underlying
-- manifest or reference-data registry has been updated.
--
-- Immutability invariant:
--   * (name, version) is unique.
--   * The script for an existing (name, version) pair is frozen; FindOrCreate
--     rejects mismatched script hashes at the application layer.
--
-- This table mirrors the GORM-managed shared/pkg/saga.SagaDefinition model so
-- services that run sagas (control-plane, current-account, payment-order, ...)
-- all share one schema definition.

CREATE TABLE IF NOT EXISTS "saga_definitions" (
  "id"            uuid NOT NULL DEFAULT gen_random_uuid(),
  "name"          varchar(64) NOT NULL,
  "version"       varchar(32) NOT NULL,
  "script"        text NOT NULL,
  "params_schema" jsonb,
  "script_hash"   varchar(64) NOT NULL,
  "created_at"    timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY ("id")
);

-- Unique constraint on (name, version) enforces immutable per-version rows.
CREATE UNIQUE INDEX IF NOT EXISTS "idx_saga_definitions_name_version"
  ON "saga_definitions" ("name", "version");
