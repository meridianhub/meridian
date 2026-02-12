-- Manifest Versions Table
-- Stores versioned snapshots of successfully applied manifests.
-- Follows Kubernetes last-applied-configuration semantics:
-- each apply stores the full manifest JSON for comparison on the next apply.
-- Multi-tenancy: Schema-per-tenant architecture means no tenant_id column needed.

CREATE TABLE "manifest_version" (
  "id" uuid NOT NULL DEFAULT gen_random_uuid(),
  "version" integer NOT NULL,
  "manifest_json" jsonb NOT NULL,
  "applied_at" timestamptz NOT NULL DEFAULT now(),
  "applied_by" character varying(255) NOT NULL,
  PRIMARY KEY ("id")
);

-- Ensure version numbers are unique and monotonically increasing
ALTER TABLE "manifest_version"
  ADD CONSTRAINT "uq_manifest_version_version"
  UNIQUE ("version");

-- Index for quickly finding the latest applied version
CREATE INDEX "idx_manifest_version_applied_at" ON "manifest_version" ("applied_at" DESC);

-- Index for version-ordered lookups
CREATE INDEX "idx_manifest_version_version" ON "manifest_version" ("version" DESC);

COMMENT ON TABLE "manifest_version" IS 'Versioned snapshots of applied manifests. Each successful apply stores the full manifest for diff comparison on subsequent applies.';
