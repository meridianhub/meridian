/* eslint-disable react-hooks/globals */
import { describe, it, expect, vi, beforeEach, afterEach, beforeAll, afterAll } from 'vitest'
import { render, screen, act, waitFor } from '@testing-library/react'
import { AuthProvider, useAuth, parseJWT } from '@/contexts/auth-context'
import {
  createTestToken,
  createExpiredToken,
  createPlatformAdminToken,
  createTenantUserToken,
  createSuperAdminToken,
} from '@/test/jwt-helpers'

// Mock fetch for token refresh - restore after all tests
const mockFetch = vi.fn()
const originalFetch = global.fetch

beforeAll(() => {
  global.fetch = mockFetch
})

afterAll(() => {
  global.fetch = originalFetch
})

describe('parseJWT', () => {
  it('parses a valid JWT and extracts claims', () => {
    const token = createTestToken({
      userId: 'user-123',
      roles: ['viewer'],
      scopes: ['read'],
      iss: 'meridian-auth',
      aud: 'meridian-console',
    })

    const claims = parseJWT(token)
    expect(claims).not.toBeNull()
    expect(claims!.userId).toBe('user-123')
    expect(claims!.roles).toEqual(['viewer'])
    expect(claims!.scopes).toEqual(['read'])
    expect(claims!.iss).toBe('meridian-auth')
    expect(claims!.aud).toBe('meridian-console')
  })

  it('returns null for a malformed token', () => {
    expect(parseJWT('not-a-jwt')).toBeNull()
    expect(parseJWT('')).toBeNull()
    expect(parseJWT('only.two')).toBeNull()
    expect(parseJWT('invalid.base64!@#.signature')).toBeNull()
  })

  it('parses an expired token (parsing does not check expiry)', () => {
    const token = createExpiredToken()
    const claims = parseJWT(token)
    expect(claims).not.toBeNull()
    expect(claims!.exp).toBeLessThan(Math.floor(Date.now() / 1000))
  })

  it('parses token with tenantId', () => {
    const token = createTenantUserToken('tenant-abc')
    const claims = parseJWT(token)
    expect(claims).not.toBeNull()
    expect(claims!.tenantId).toBe('tenant-abc')
  })

  it('parses token without tenantId for platform admins', () => {
    const token = createPlatformAdminToken()
    const claims = parseJWT(token)
    expect(claims).not.toBeNull()
    expect(claims!.tenantId).toBeUndefined()
    expect(claims!.roles).toContain('platform-admin')
  })

  it('uses groups as effective roles when roles is empty', () => {
    const token = createTestToken({
      userId: 'dex-user',
      roles: [],
      groups: ['platform-admin', 'operator'],
      iss: 'dex',
      aud: 'meridian',
    })
    const claims = parseJWT(token)
    expect(claims).not.toBeNull()
    expect(claims!.roles).toEqual(['platform-admin', 'operator'])
  })

  it('prefers roles over groups when both present', () => {
    const token = createTestToken({
      userId: 'dex-user',
      roles: ['admin'],
      groups: ['platform-admin'],
      iss: 'dex',
      aud: 'meridian',
    })
    const claims = parseJWT(token)
    expect(claims).not.toBeNull()
    expect(claims!.roles).toEqual(['admin'])
  })

  it('returns empty roles when both roles and groups absent', () => {
    const token = createTestToken({
      userId: 'dex-user',
      roles: [],
      iss: 'dex',
      aud: 'meridian',
    })
    const claims = parseJWT(token)
    expect(claims).not.toBeNull()
    expect(claims!.roles).toEqual([])
  })

  it('normalizes UPPERCASE and mixed-case roles to lowercase', () => {
    const token = createTestToken({
      userId: 'user-case',
      roles: ['PLATFORM-ADMIN', 'Tenant-Admin', 'SUPER-ADMIN'],
      iss: 'meridian-auth',
      aud: 'meridian-console',
    })
    const claims = parseJWT(token)
    expect(claims).not.toBeNull()
    expect(claims!.roles).toEqual(['platform-admin', 'tenant-admin', 'super-admin'])
  })

  it('normalizes UPPERCASE groups to lowercase when used as effective roles', () => {
    const token = createTestToken({
      userId: 'dex-user',
      roles: [],
      groups: ['PLATFORM-ADMIN', 'Operator'],
      iss: 'dex',
      aud: 'meridian',
    })
    const claims = parseJWT(token)
    expect(claims).not.toBeNull()
    expect(claims!.roles).toEqual(['platform-admin', 'operator'])
  })
})

describe('getUserLens', () => {
  it('returns platform for platform-admin without tenantId', async () => {
    const token = createPlatformAdminToken()
    mockFetch.mockResolvedValue({
      ok: true,
      json: async () => ({ accessToken: token }),
    })

    const TestComponent = () => {
      const { lens } = useAuth()
      return <div data-testid="lens">{lens}</div>
    }

    await act(async () => {
      render(
        <AuthProvider initialToken={token}>
          <TestComponent />
        </AuthProvider>,
      )
    })

    expect(screen.getByTestId('lens').textContent).toBe('platform')
  })

  it('returns platform for super-admin without tenantId', async () => {
    const token = createSuperAdminToken()

    const TestComponent = () => {
      const { lens } = useAuth()
      return <div data-testid="lens">{lens}</div>
    }

    await act(async () => {
      render(
        <AuthProvider initialToken={token}>
          <TestComponent />
        </AuthProvider>,
      )
    })

    expect(screen.getByTestId('lens').textContent).toBe('platform')
  })

  it('returns tenant for tenant users with tenantId', async () => {
    const token = createTenantUserToken('tenant-xyz')

    const TestComponent = () => {
      const { lens } = useAuth()
      return <div data-testid="lens">{lens}</div>
    }

    await act(async () => {
      render(
        <AuthProvider initialToken={token}>
          <TestComponent />
        </AuthProvider>,
      )
    })

    expect(screen.getByTestId('lens').textContent).toBe('tenant')
  })

  it('returns tenant for platform-admin WITH a tenantId (viewing tenant context)', async () => {
    const token = createTestToken({
      userId: 'admin-456',
      tenantId: 'some-tenant',
      roles: ['platform-admin'],
    })

    const TestComponent = () => {
      const { lens } = useAuth()
      return <div data-testid="lens">{lens}</div>
    }

    await act(async () => {
      render(
        <AuthProvider initialToken={token}>
          <TestComponent />
        </AuthProvider>,
      )
    })

    expect(screen.getByTestId('lens').textContent).toBe('tenant')
  })
})

describe('parseJWT - type validation', () => {
  it('rejects token with non-numeric exp', () => {
    // Manually craft a token with exp as string
    const header = btoa(JSON.stringify({ alg: 'HS256', typ: 'JWT' }))
      .replace(/\+/g, '-').replace(/\//g, '_').replace(/=/g, '')
    const payload = btoa(JSON.stringify({
      userId: 'user-1',
      exp: 'not-a-number',
      iss: 'meridian-auth',
      aud: 'meridian-console',
      roles: [],
      scopes: [],
    })).replace(/\+/g, '-').replace(/\//g, '_').replace(/=/g, '')
    const badToken = `${header}.${payload}.sig`
    expect(parseJWT(badToken)).toBeNull()
  })

  it('rejects token with non-string userId', () => {
    const header = btoa(JSON.stringify({ alg: 'HS256', typ: 'JWT' }))
      .replace(/\+/g, '-').replace(/\//g, '_').replace(/=/g, '')
    const payload = btoa(JSON.stringify({
      userId: 123,
      exp: Math.floor(Date.now() / 1000) + 3600,
      iss: 'meridian-auth',
      aud: 'meridian-console',
      roles: [],
      scopes: [],
    })).replace(/\+/g, '-').replace(/\//g, '_').replace(/=/g, '')
    const badToken = `${header}.${payload}.sig`
    expect(parseJWT(badToken)).toBeNull()
  })

  it('rejects token when exp is NaN', () => {
    const header = btoa(JSON.stringify({ alg: 'HS256', typ: 'JWT' }))
      .replace(/\+/g, '-').replace(/\//g, '_').replace(/=/g, '')
    const payload = btoa(JSON.stringify({
      userId: 'user-1',
      exp: NaN,
      iss: 'meridian-auth',
      aud: 'meridian-console',
      roles: [],
      scopes: [],
    })).replace(/\+/g, '-').replace(/\//g, '_').replace(/=/g, '')
    const badToken = `${header}.${payload}.sig`
    expect(parseJWT(badToken)).toBeNull()
  })
})

describe('AuthProvider', () => {
  beforeEach(() => {
    mockFetch.mockReset()
    localStorage.clear()
    sessionStorage.clear()
  })

  afterEach(() => {
    localStorage.clear()
    sessionStorage.clear()
  })

  it('starts unauthenticated with no token', async () => {
    const TestComponent = () => {
      const { isAuthenticated, claims } = useAuth()
      return (
        <div>
          <span data-testid="auth">{String(isAuthenticated)}</span>
          <span data-testid="claims">{claims ? 'has-claims' : 'no-claims'}</span>
        </div>
      )
    }

    await act(async () => {
      render(
        <AuthProvider>
          <TestComponent />
        </AuthProvider>,
      )
    })

    expect(screen.getByTestId('auth').textContent).toBe('false')
    expect(screen.getByTestId('claims').textContent).toBe('no-claims')
  })

  it('becomes authenticated with a valid token', async () => {
    const token = createTestToken({ userId: 'user-123' })

    const TestComponent = () => {
      const { isAuthenticated, claims } = useAuth()
      return (
        <div>
          <span data-testid="auth">{String(isAuthenticated)}</span>
          <span data-testid="userId">{claims?.userId ?? 'none'}</span>
        </div>
      )
    }

    await act(async () => {
      render(
        <AuthProvider initialToken={token}>
          <TestComponent />
        </AuthProvider>,
      )
    })

    expect(screen.getByTestId('auth').textContent).toBe('true')
    expect(screen.getByTestId('userId').textContent).toBe('user-123')
  })

  it('persists token to sessionStorage but NOT localStorage', async () => {
    const token = createTestToken({ userId: 'user-123' })

    const TestComponent = () => {
      const { isAuthenticated } = useAuth()
      return <span data-testid="auth">{String(isAuthenticated)}</span>
    }

    await act(async () => {
      render(
        <AuthProvider initialToken={token}>
          <TestComponent />
        </AuthProvider>,
      )
    })

    expect(screen.getByTestId('auth').textContent).toBe('true')

    // Token should be in sessionStorage
    expect(sessionStorage.getItem('meridian_access_token')).toBe(token)

    // Token must NOT be in localStorage
    const localStorageKeys = Object.keys(localStorage)
    const tokenInLocalStorage = localStorageKeys.some(
      (key) => localStorage.getItem(key)?.includes(token),
    )
    expect(tokenInLocalStorage).toBe(false)
  })

  it('restores auth state from sessionStorage on mount', async () => {
    const token = createTestToken({ userId: 'user-restored' })
    sessionStorage.setItem('meridian_access_token', token)

    const TestComponent = () => {
      const { isAuthenticated, claims } = useAuth()
      return (
        <div>
          <span data-testid="auth">{String(isAuthenticated)}</span>
          <span data-testid="userId">{claims?.userId ?? 'none'}</span>
        </div>
      )
    }

    await act(async () => {
      render(
        <AuthProvider>
          <TestComponent />
        </AuthProvider>,
      )
    })

    expect(screen.getByTestId('auth').textContent).toBe('true')
    expect(screen.getByTestId('userId').textContent).toBe('user-restored')
  })

  it('clears expired token from sessionStorage on mount', async () => {
    const expiredToken = createExpiredToken()
    sessionStorage.setItem('meridian_access_token', expiredToken)

    const TestComponent = () => {
      const { isAuthenticated } = useAuth()
      return <span data-testid="auth">{String(isAuthenticated)}</span>
    }

    // Mock refresh to fail so we stay unauthenticated
    mockFetch.mockResolvedValueOnce({ ok: false, status: 401 })

    await act(async () => {
      render(
        <AuthProvider>
          <TestComponent />
        </AuthProvider>,
      )
    })

    expect(screen.getByTestId('auth').textContent).toBe('false')
    expect(sessionStorage.getItem('meridian_access_token')).toBeNull()
  })

  it('clears sessionStorage on logout', async () => {
    const token = createTestToken({ userId: 'user-123' })

    let logoutFn: (() => void) | null = null

    const TestComponent = () => {
      const { isAuthenticated, logout } = useAuth()
      logoutFn = logout
      return <span data-testid="auth">{String(isAuthenticated)}</span>
    }

    await act(async () => {
      render(
        <AuthProvider initialToken={token}>
          <TestComponent />
        </AuthProvider>,
      )
    })

    expect(sessionStorage.getItem('meridian_access_token')).toBe(token)

    await act(async () => {
      logoutFn!()
    })

    expect(screen.getByTestId('auth').textContent).toBe('false')
    expect(sessionStorage.getItem('meridian_access_token')).toBeNull()
  })

  it('clears malformed token from sessionStorage on mount', async () => {
    sessionStorage.setItem('meridian_access_token', 'not-a-valid-jwt')

    const TestComponent = () => {
      const { isAuthenticated } = useAuth()
      return <span data-testid="auth">{String(isAuthenticated)}</span>
    }

    await act(async () => {
      render(
        <AuthProvider>
          <TestComponent />
        </AuthProvider>,
      )
    })

    expect(screen.getByTestId('auth').textContent).toBe('false')
    expect(sessionStorage.getItem('meridian_access_token')).toBeNull()
  })

  it('calls refresh endpoint and updates token on refresh', async () => {
    const initialToken = createTestToken({ userId: 'user-123' })
    const newToken = createTestToken({ userId: 'user-123', roles: ['admin'] })

    mockFetch.mockResolvedValueOnce({
      ok: true,
      json: async () => ({ accessToken: newToken }),
    })

    let refreshFn: (() => Promise<boolean>) | null = null

    const TestComponent = () => {
      const { claims, refreshToken } = useAuth()
      refreshFn = refreshToken
      return <span data-testid="roles">{claims?.roles.join(',') ?? 'none'}</span>
    }

    await act(async () => {
      render(
        <AuthProvider initialToken={initialToken}>
          <TestComponent />
        </AuthProvider>,
      )
    })

    expect(screen.getByTestId('roles').textContent).toBe('viewer')

    await act(async () => {
      await refreshFn!()
    })

    await waitFor(() => {
      expect(screen.getByTestId('roles').textContent).toBe('admin')
    })

    expect(mockFetch).toHaveBeenCalledWith('/api/auth/refresh', expect.any(Object))
  })

  it('becomes unauthenticated when refresh returns 401', async () => {
    const initialToken = createTestToken({ userId: 'user-123' })

    mockFetch.mockResolvedValueOnce({
      ok: false,
      status: 401,
    })

    let refreshFn: (() => Promise<boolean>) | null = null

    const TestComponent = () => {
      const { isAuthenticated, refreshToken } = useAuth()
      refreshFn = refreshToken
      return <span data-testid="auth">{String(isAuthenticated)}</span>
    }

    await act(async () => {
      render(
        <AuthProvider initialToken={initialToken}>
          <TestComponent />
        </AuthProvider>,
      )
    })

    expect(screen.getByTestId('auth').textContent).toBe('true')

    await act(async () => {
      await refreshFn!()
    })

    await waitFor(() => {
      expect(screen.getByTestId('auth').textContent).toBe('false')
    })
  })

  it('stays authenticated when refresh returns 5xx (transient error)', async () => {
    const initialToken = createTestToken({ userId: 'user-123' })

    mockFetch.mockResolvedValueOnce({
      ok: false,
      status: 503,
    })

    let refreshFn: (() => Promise<boolean>) | null = null

    const TestComponent = () => {
      const { isAuthenticated, refreshToken } = useAuth()
      refreshFn = refreshToken
      return <span data-testid="auth">{String(isAuthenticated)}</span>
    }

    await act(async () => {
      render(
        <AuthProvider initialToken={initialToken}>
          <TestComponent />
        </AuthProvider>,
      )
    })

    expect(screen.getByTestId('auth').textContent).toBe('true')

    let result: boolean = true
    await act(async () => {
      result = await refreshFn!()
    })

    // Should return false but NOT clear auth state on transient error
    expect(result).toBe(false)
    expect(screen.getByTestId('auth').textContent).toBe('true')
  })

  it('clears accessToken when login is called with a malformed token', async () => {
    let loginFn: ((token: string) => void) | null = null

    const TestComponent = () => {
      const { isAuthenticated, accessToken, login } = useAuth()
      loginFn = login
      return (
        <div>
          <span data-testid="auth">{String(isAuthenticated)}</span>
          <span data-testid="token">{accessToken ?? 'null'}</span>
        </div>
      )
    }

    await act(async () => {
      render(
        <AuthProvider>
          <TestComponent />
        </AuthProvider>,
      )
    })

    await act(async () => {
      loginFn!('not.a.valid.jwt.token')
    })

    // Token parse failed - should not store malformed token
    expect(screen.getByTestId('auth').textContent).toBe('false')
    expect(screen.getByTestId('token').textContent).toBe('null')
  })

  it('provides login function that sets token from auth response', async () => {
    const newToken = createTenantUserToken('tenant-123')

    let loginFn: ((token: string) => void) | null = null

    const TestComponent = () => {
      const { isAuthenticated, claims, login } = useAuth()
      loginFn = login
      return (
        <div>
          <span data-testid="auth">{String(isAuthenticated)}</span>
          <span data-testid="tenantId">{claims?.tenantId ?? 'none'}</span>
        </div>
      )
    }

    await act(async () => {
      render(
        <AuthProvider>
          <TestComponent />
        </AuthProvider>,
      )
    })

    expect(screen.getByTestId('auth').textContent).toBe('false')

    await act(async () => {
      loginFn!(newToken)
    })

    expect(screen.getByTestId('auth').textContent).toBe('true')
    expect(screen.getByTestId('tenantId').textContent).toBe('tenant-123')
  })

  it('provides logout function that clears auth state', async () => {
    const token = createTestToken({ userId: 'user-123' })

    let logoutFn: (() => void) | null = null

    const TestComponent = () => {
      const { isAuthenticated, logout } = useAuth()
      logoutFn = logout
      return <span data-testid="auth">{String(isAuthenticated)}</span>
    }

    await act(async () => {
      render(
        <AuthProvider initialToken={token}>
          <TestComponent />
        </AuthProvider>,
      )
    })

    expect(screen.getByTestId('auth').textContent).toBe('true')

    await act(async () => {
      logoutFn!()
    })

    expect(screen.getByTestId('auth').textContent).toBe('false')
  })

  it('rejects stored token when tenantId mismatches subdomain (session bleeding prevention)', async () => {
    // Store a token for tenant "acme"
    const token = createTenantUserToken('acme')
    sessionStorage.setItem('meridian_access_token', token)

    // Simulate being on a different tenant's subdomain
    const originalHostname = window.location.hostname
    Object.defineProperty(window, 'location', {
      value: { ...window.location, hostname: 'other.meridianhub.cloud' },
      writable: true,
    })

    const TestComponent = () => {
      const { isAuthenticated } = useAuth()
      return <span data-testid="auth">{String(isAuthenticated)}</span>
    }

    await act(async () => {
      render(
        <AuthProvider>
          <TestComponent />
        </AuthProvider>,
      )
    })

    expect(screen.getByTestId('auth').textContent).toBe('false')
    expect(sessionStorage.getItem('meridian_access_token')).toBeNull()

    // Restore hostname
    Object.defineProperty(window, 'location', {
      value: { ...window.location, hostname: originalHostname },
      writable: true,
    })
  })

  it('accepts stored token when tenantId matches subdomain', async () => {
    const token = createTenantUserToken('acme')
    sessionStorage.setItem('meridian_access_token', token)

    const originalHostname = window.location.hostname
    Object.defineProperty(window, 'location', {
      value: { ...window.location, hostname: 'acme.meridianhub.cloud' },
      writable: true,
    })

    const TestComponent = () => {
      const { isAuthenticated, claims } = useAuth()
      return (
        <div>
          <span data-testid="auth">{String(isAuthenticated)}</span>
          <span data-testid="tenantId">{claims?.tenantId ?? 'none'}</span>
        </div>
      )
    }

    await act(async () => {
      render(
        <AuthProvider>
          <TestComponent />
        </AuthProvider>,
      )
    })

    expect(screen.getByTestId('auth').textContent).toBe('true')
    expect(screen.getByTestId('tenantId').textContent).toBe('acme')

    Object.defineProperty(window, 'location', {
      value: { ...window.location, hostname: originalHostname },
      writable: true,
    })
  })

  it('rejects login token when tenantId mismatches subdomain', async () => {
    const originalHostname = window.location.hostname
    Object.defineProperty(window, 'location', {
      value: { ...window.location, hostname: 'other.meridianhub.cloud' },
      writable: true,
    })

    let loginFn: ((token: string) => void) | null = null

    const TestComponent = () => {
      const { isAuthenticated, login } = useAuth()
      loginFn = login
      return <span data-testid="auth">{String(isAuthenticated)}</span>
    }

    await act(async () => {
      render(
        <AuthProvider>
          <TestComponent />
        </AuthProvider>,
      )
    })

    // Try to login with a token for tenant "acme" while on "other" subdomain
    const token = createTenantUserToken('acme')
    await act(async () => {
      loginFn!(token)
    })

    expect(screen.getByTestId('auth').textContent).toBe('false')
    expect(sessionStorage.getItem('meridian_access_token')).toBeNull()

    Object.defineProperty(window, 'location', {
      value: { ...window.location, hostname: originalHostname },
      writable: true,
    })
  })
})
