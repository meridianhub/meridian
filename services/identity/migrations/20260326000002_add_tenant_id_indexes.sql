-- Add indexes for tenant_id columns and update unique constraint for email per-tenant.
-- Split from 20260326000001 because CockroachDB requires columns to be committed
-- before they can be referenced in partial indexes.

-- Index for tenant-scoped queries on identity
CREATE INDEX "idx_identity_tenant_id" ON "identity" ("tenant_id");

-- Replace the global email uniqueness with per-tenant uniqueness.
-- Drop old index first, then create new one including tenant_id.
DROP INDEX IF EXISTS "idx_identity_email";
CREATE UNIQUE INDEX "idx_identity_tenant_email" ON "identity" ("tenant_id", "email") WHERE (deleted_at IS NULL);

-- Index for tenant-scoped queries on role_assignment
CREATE INDEX "idx_role_assignment_tenant_id" ON "role_assignment" ("tenant_id");
