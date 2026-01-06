-- Import Manifest Table for Bulk Import Tool
-- Tracks bulk import operations including source files, progress, and rollback information
-- Uses tenant_id column for explicit tenant isolation as bulk imports may span multiple contexts
-- See Task 36 (Bulk Import Tool) for requirements

-- Create "import_manifest" table (singular, unqualified)
CREATE TABLE "import_manifest" (
  "id" uuid NOT NULL DEFAULT gen_random_uuid(),
  "tenant_id" text NOT NULL,
  "source_file" text NOT NULL,
  "file_checksum" text NOT NULL,
  "total_rows" integer NULL,
  "processed_rows" integer NOT NULL DEFAULT 0,
  "success_count" integer NULL,
  "failure_count" integer NULL,
  "status" text NOT NULL DEFAULT 'RUNNING',
  "rollback_sql" text NULL,
  "created_at" timestamptz NOT NULL DEFAULT now(),
  "updated_at" timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY ("id")
);

-- Add validation constraint for status values
ALTER TABLE "import_manifest"
  ADD CONSTRAINT "chk_import_manifest_status"
  CHECK ("status" IN ('RUNNING', 'COMPLETED', 'FAILED', 'CANCELLED'));

-- Unique constraint to prevent duplicate imports (same file with same checksum for a tenant)
ALTER TABLE "import_manifest"
  ADD CONSTRAINT "uq_import_manifest_tenant_file_checksum"
  UNIQUE ("tenant_id", "source_file", "file_checksum");

-- Index for efficient querying by tenant and status
-- Supports queries: "find all running imports for tenant X", "find all imports for tenant X"
CREATE INDEX "idx_import_manifest_tenant_status" ON "import_manifest" ("tenant_id", "status");

-- Index for querying by created_at (audit/history queries)
CREATE INDEX "idx_import_manifest_created_at" ON "import_manifest" ("created_at");

-- Trigger function to automatically update updated_at timestamp
CREATE OR REPLACE FUNCTION "update_import_manifest_timestamp"()
RETURNS TRIGGER AS $$
BEGIN
  NEW."updated_at" = NOW();
  RETURN NEW;
END;
$$ LANGUAGE plpgsql;

-- Create trigger to update updated_at on every UPDATE
CREATE TRIGGER "trg_import_manifest_updated_at"
  BEFORE UPDATE ON "import_manifest"
  FOR EACH ROW
  EXECUTE FUNCTION "update_import_manifest_timestamp"();

-- Add comments documenting the table and columns
COMMENT ON TABLE "import_manifest" IS 'Tracks bulk import operations including progress, status, and rollback information for data recovery.';
COMMENT ON COLUMN "import_manifest"."tenant_id" IS 'Tenant identifier for multi-tenant isolation. All imports are scoped to a tenant.';
COMMENT ON COLUMN "import_manifest"."source_file" IS 'Original filename or path of the imported file.';
COMMENT ON COLUMN "import_manifest"."file_checksum" IS 'SHA256 checksum of the source file to detect duplicate imports.';
COMMENT ON COLUMN "import_manifest"."total_rows" IS 'Total number of rows in the import file (set after file parsing).';
COMMENT ON COLUMN "import_manifest"."processed_rows" IS 'Number of rows processed so far (for progress tracking).';
COMMENT ON COLUMN "import_manifest"."success_count" IS 'Number of rows successfully imported.';
COMMENT ON COLUMN "import_manifest"."failure_count" IS 'Number of rows that failed to import.';
COMMENT ON COLUMN "import_manifest"."status" IS 'Import status: RUNNING (in progress), COMPLETED (finished successfully), FAILED (error occurred), CANCELLED (user cancelled).';
COMMENT ON COLUMN "import_manifest"."rollback_sql" IS 'SQL statement to rollback this import (e.g., DELETE WHERE reference_id IN (...)).';
COMMENT ON COLUMN "import_manifest"."created_at" IS 'Timestamp when the import was initiated.';
COMMENT ON COLUMN "import_manifest"."updated_at" IS 'Timestamp of last update, automatically managed by trigger.';
