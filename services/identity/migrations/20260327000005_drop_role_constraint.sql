-- Drop the outdated role check constraint.
-- The initial migration used uppercase values ('PLATFORM', 'TENANT_OWNER')
-- but the Go domain uses lowercase kebab-case ('platform-admin', 'tenant-owner').
-- A follow-up migration recreates the constraint with the correct values.

ALTER TABLE "role_assignment" DROP CONSTRAINT IF EXISTS "chk_role_assignment_role";
