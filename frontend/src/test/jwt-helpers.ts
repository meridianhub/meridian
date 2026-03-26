/**
 * Test helpers for creating JWT tokens without external libraries.
 * For testing purposes only - these are NOT cryptographically signed.
 */

function base64UrlEncode(str: string): string {
  return btoa(str).replace(/\+/g, '-').replace(/\//g, '_').replace(/=/g, '')
}

interface TokenPayload {
  userId?: string
  tenantId?: string
  tenantDisplayName?: string
  roles?: string[]
  groups?: string[]
  scopes?: string[]
  exp?: number
  iss?: string
  aud?: string
  sub?: string
}

export function createTestToken(payload: TokenPayload): string {
  const header = { alg: 'HS256', typ: 'JWT' }
  const defaults = {
    userId: 'user-123',
    roles: ['viewer'],
    scopes: ['read'],
    exp: Math.floor(Date.now() / 1000) + 3600,
    iss: 'meridian-auth',
    aud: 'meridian-console',
    sub: payload.userId ?? 'user-123',
  }

  const { tenantDisplayName, ...rest } = payload
  const fullPayload: Record<string, unknown> = { ...defaults, ...rest }
  if (tenantDisplayName) {
    fullPayload['x-tenant-display-name'] = tenantDisplayName
  }

  const encodedHeader = base64UrlEncode(JSON.stringify(header))
  const encodedPayload = base64UrlEncode(JSON.stringify(fullPayload))

  // Fake signature for testing
  return `${encodedHeader}.${encodedPayload}.fakesignature`
}

export function createExpiredToken(): string {
  return createTestToken({
    userId: 'user-expired',
    exp: Math.floor(Date.now() / 1000) - 3600,
  })
}

export function createPlatformAdminToken(): string {
  return createTestToken({
    userId: 'admin-456',
    // No tenantId - platform admin
    roles: ['platform-admin'],
    scopes: ['read', 'write', 'admin'],
  })
}

export function createTenantUserToken(tenantId = 'tenant-789', tenantDisplayName = 'Test Tenant'): string {
  return createTestToken({
    userId: 'user-123',
    tenantId,
    tenantDisplayName,
    roles: ['tenant-admin'],
    scopes: ['read', 'write'],
  })
}

export function createSuperAdminToken(): string {
  return createTestToken({
    userId: 'super-000',
    // No tenantId - super admin
    roles: ['super-admin'],
    scopes: ['read', 'write', 'admin', 'super'],
  })
}
