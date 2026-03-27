-- Recreate role check constraint with kebab-case values matching the Go domain,
-- plus uppercase values for backwards compatibility with existing data.
-- Also adds 'auditor', 'service', and 'super-admin' which were missing.

ALTER TABLE "role_assignment" ADD CONSTRAINT "chk_role_assignment_role"
  CHECK (role IN ('viewer', 'operator', 'admin', 'auditor', 'service', 'tenant-owner', 'platform-admin', 'super-admin',
                  'VIEWER', 'OPERATOR', 'ADMIN', 'TENANT_OWNER', 'PLATFORM'));
