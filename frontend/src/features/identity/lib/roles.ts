import { Role } from '@/api/gen/meridian/identity/v1/identity_pb'

export const ROLE_LABELS: Record<number, string> = {
  [Role.ADMIN]: 'Admin',
  [Role.OPERATOR]: 'Operator',
  [Role.AUDITOR]: 'Auditor',
  [Role.TENANT_OWNER]: 'Tenant Owner',
  [Role.PLATFORM_ADMIN]: 'Platform Admin',
  [Role.SUPER_ADMIN]: 'Super Admin',
}

/**
 * Returns the list of roles the current user can grant based on role hierarchy.
 * SUPER_ADMIN can grant all. ADMIN can grant OPERATOR and AUDITOR.
 * PLATFORM_ADMIN can grant all tenant-level roles.
 */
export function getGrantableRoles(currentUserRoles: string[]): Role[] {
  const isSuperAdmin = currentUserRoles.includes('super-admin')
  const isPlatformAdmin = currentUserRoles.includes('platform-admin')
  const isAdmin =
    currentUserRoles.includes('admin') || currentUserRoles.includes('tenant-admin')

  if (isSuperAdmin) {
    return [
      Role.ADMIN,
      Role.OPERATOR,
      Role.AUDITOR,
      Role.TENANT_OWNER,
      Role.PLATFORM_ADMIN,
      Role.SUPER_ADMIN,
    ]
  }
  if (isPlatformAdmin) {
    return [Role.ADMIN, Role.OPERATOR, Role.AUDITOR, Role.TENANT_OWNER]
  }
  if (isAdmin) {
    return [Role.OPERATOR, Role.AUDITOR]
  }
  return []
}
