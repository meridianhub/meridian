-- Rebucketing Audit Log Table Migration
-- Implements audit logging for position rebucketing operations (Task 24.3)
-- Uses unqualified table names (relies on database-per-service architecture per ADR-002)
--
-- ARCHITECTURAL NOTES:
-- - This table logs EVERY position affected during rebucketing
-- - Each rebucketing operation creates TWO audit entries per position:
--   1. SOFT_DELETE for the old position (sets deleted_at)
--   2. INSERT_NEW for the new position with corrected bucket_key
-- - This maintains full audit trail while respecting append-only position semantics
-- - Schema-per-tenant isolation means NO tenant_id column needed (per ADR-0016)

-- Create "rebucketing_audit_log" table
CREATE TABLE "rebucketing_audit_log" (
  "id" uuid NOT NULL DEFAULT gen_random_uuid(),
  "timestamp" timestamptz NOT NULL DEFAULT now(),
  "admin_user_id" character varying(255) NOT NULL,
  "old_instrument_version" character varying(64) NOT NULL,
  "new_instrument_version" character varying(64) NOT NULL,
  "position_id" uuid NOT NULL,
  "old_bucket_id" character varying(256) NOT NULL,
  "new_bucket_id" character varying(256) NOT NULL,
  "operation" character varying(32) NOT NULL,
  PRIMARY KEY ("id")
);

-- Create indexes for audit log queries
-- Index for time-based queries (audit history)
CREATE INDEX "idx_rebucketing_audit_log_timestamp" ON "rebucketing_audit_log" ("timestamp");

-- Index for position lookups (see rebucketing history for a specific position)
CREATE INDEX "idx_rebucketing_audit_log_position_id" ON "rebucketing_audit_log" ("position_id");

-- Index for admin queries (see all rebucketings by a specific admin)
CREATE INDEX "idx_rebucketing_audit_log_admin_user_id" ON "rebucketing_audit_log" ("admin_user_id");

-- Index for version queries (see all positions affected by a version change)
CREATE INDEX "idx_rebucketing_audit_log_versions" ON "rebucketing_audit_log" ("old_instrument_version", "new_instrument_version");

-- Add validation constraint for operation values
ALTER TABLE "rebucketing_audit_log"
  ADD CONSTRAINT "chk_rebucketing_audit_log_operation"
  CHECK ("operation" IN ('SOFT_DELETE', 'INSERT_NEW'));

-- Add comments documenting the audit log purpose
COMMENT ON TABLE "rebucketing_audit_log" IS 'Audit log for position rebucketing operations. Records every position affected during instrument version migrations.';
COMMENT ON COLUMN "rebucketing_audit_log"."admin_user_id" IS 'The admin user who authorized the rebucketing operation.';
COMMENT ON COLUMN "rebucketing_audit_log"."old_instrument_version" IS 'The instrument version hash before rebucketing.';
COMMENT ON COLUMN "rebucketing_audit_log"."new_instrument_version" IS 'The instrument version hash after rebucketing.';
COMMENT ON COLUMN "rebucketing_audit_log"."position_id" IS 'The ID of the position being rebucketed.';
COMMENT ON COLUMN "rebucketing_audit_log"."old_bucket_id" IS 'The bucket_key before rebucketing.';
COMMENT ON COLUMN "rebucketing_audit_log"."new_bucket_id" IS 'The bucket_key after rebucketing.';
COMMENT ON COLUMN "rebucketing_audit_log"."operation" IS 'The operation type: SOFT_DELETE (marks old position deleted) or INSERT_NEW (creates new position).';
