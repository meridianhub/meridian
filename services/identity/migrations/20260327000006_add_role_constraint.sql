-- Recreate role check constraint accepting both encoding conventions:
-- - Uppercase (identity domain): VIEWER, OPERATOR, ADMIN, TENANT_OWNER, PLATFORM
-- - Kebab-case (auth package): viewer, operator, admin, tenant-owner, platform-admin
-- Also adds auditor, service, and super-admin used by the auth RBAC layer.

ALTER TABLE "role_assignment" ADD CONSTRAINT "chk_role_assignment_role"
  CHECK (role IN ('viewer', 'operator', 'admin', 'auditor', 'service', 'tenant-owner', 'platform-admin', 'super-admin',
                  'VIEWER', 'OPERATOR', 'ADMIN', 'TENANT_OWNER', 'PLATFORM'));
