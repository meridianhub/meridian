import { describe, it, expect } from 'vitest'
import { Role } from '@/api/gen/meridian/identity/v1/identity_pb'
import { getGrantableRoles, ROLE_LABELS } from './roles'

describe('ROLE_LABELS', () => {
  it('has labels for all non-unspecified roles', () => {
    expect(ROLE_LABELS[Role.ADMIN]).toBe('Admin')
    expect(ROLE_LABELS[Role.OPERATOR]).toBe('Operator')
    expect(ROLE_LABELS[Role.AUDITOR]).toBe('Auditor')
    expect(ROLE_LABELS[Role.TENANT_OWNER]).toBe('Tenant Owner')
    expect(ROLE_LABELS[Role.PLATFORM_ADMIN]).toBe('Platform Admin')
    expect(ROLE_LABELS[Role.SUPER_ADMIN]).toBe('Super Admin')
  })
})

describe('getGrantableRoles', () => {
  it('super-admin can grant all roles', () => {
    const result = getGrantableRoles(['super-admin'])
    expect(result).toContain(Role.ADMIN)
    expect(result).toContain(Role.SUPER_ADMIN)
    expect(result).toContain(Role.PLATFORM_ADMIN)
    expect(result).toHaveLength(6)
  })

  it('platform-admin can grant tenant-level roles', () => {
    const result = getGrantableRoles(['platform-admin'])
    expect(result).toContain(Role.ADMIN)
    expect(result).toContain(Role.OPERATOR)
    expect(result).toContain(Role.AUDITOR)
    expect(result).toContain(Role.TENANT_OWNER)
    expect(result).not.toContain(Role.PLATFORM_ADMIN)
    expect(result).not.toContain(Role.SUPER_ADMIN)
  })

  it('admin can grant operator and auditor', () => {
    const result = getGrantableRoles(['admin'])
    expect(result).toEqual([Role.OPERATOR, Role.AUDITOR])
  })

  it('tenant-admin can grant operator and auditor', () => {
    const result = getGrantableRoles(['tenant-admin'])
    expect(result).toEqual([Role.OPERATOR, Role.AUDITOR])
  })

  it('operator cannot grant any roles', () => {
    const result = getGrantableRoles(['operator'])
    expect(result).toEqual([])
  })

  it('empty roles cannot grant any roles', () => {
    const result = getGrantableRoles([])
    expect(result).toEqual([])
  })
})
