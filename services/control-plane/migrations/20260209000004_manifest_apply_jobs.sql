-- Manifest Apply Jobs Table
-- Tracks the status of manifest application jobs, linking to saga executions
-- for durable orchestration and causation tree visibility.
-- Multi-tenancy: Schema-per-tenant architecture means no tenant_id column needed.

CREATE TABLE "manifest_apply_job" (
  "id" uuid NOT NULL DEFAULT gen_random_uuid(),
  "manifest_version" integer NOT NULL,
  "saga_execution_id" uuid,
  "status" varchar(32) NOT NULL DEFAULT 'PENDING',
  "error" text,
  "created_at" timestamptz NOT NULL DEFAULT now(),
  "completed_at" timestamptz,
  PRIMARY KEY ("id"),
  CONSTRAINT "chk_manifest_apply_job_status"
    CHECK ("status" IN ('PENDING', 'APPLYING', 'APPLIED', 'FAILED'))
);

-- Index for finding jobs by manifest version
CREATE INDEX "idx_manifest_apply_job_version" ON "manifest_apply_job" ("manifest_version");

-- Index for finding active/pending jobs
CREATE INDEX "idx_manifest_apply_job_status" ON "manifest_apply_job" ("status")
  WHERE "status" IN ('PENDING', 'APPLYING');

-- Index for saga causation tree lookups
CREATE INDEX "idx_manifest_apply_job_saga" ON "manifest_apply_job" ("saga_execution_id")
  WHERE "saga_execution_id" IS NOT NULL;

COMMENT ON TABLE "manifest_apply_job" IS 'Tracks manifest application jobs with status and linkage to saga executions for durable orchestration.';
