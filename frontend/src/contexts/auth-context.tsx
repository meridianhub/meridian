import { createContext, useContext, useState, useEffect, useCallback, type ReactNode } from 'react'

export interface JWTClaims {
  userId: string
  tenantId?: string
  roles: string[]
  scopes: string[]
  exp: number
  iss: string
  aud: string
  sub?: string
}

export interface AuthState {
  isAuthenticated: boolean
  claims: JWTClaims | null
  accessToken: string | null
  lens: 'platform' | 'tenant'
}

interface AuthContextValue extends AuthState {
  login: (token: string) => void
  logout: () => void
  refreshToken: () => Promise<boolean>
}

// eslint-disable-next-line react-refresh/only-export-components
export function parseJWT(token: unknown): JWTClaims | null {
  if (typeof token !== 'string' || !token) return null

  const parts = token.split('.')
  if (parts.length !== 3) return null

  try {
    const payload = parts[1]
    // Pad base64url to standard base64
    const padded = payload
      .replace(/-/g, '+')
      .replace(/_/g, '/')
      .padEnd(payload.length + ((4 - (payload.length % 4)) % 4), '=')
    const decoded = JSON.parse(atob(padded)) as Record<string, unknown>

    // Determine user ID: prefer custom userId claim, fall back to standard OIDC sub claim
    const userId =
      typeof decoded.userId === 'string' && decoded.userId
        ? decoded.userId
        : typeof decoded.sub === 'string' && decoded.sub
          ? decoded.sub
          : null

    // Strict type validation to prevent expiry bypass and type confusion
    if (
      !userId ||
      typeof decoded.exp !== 'number' ||
      !isFinite(decoded.exp) ||
      typeof decoded.iss !== 'string' ||
      !decoded.iss
    ) {
      return null
    }

    // Standard OIDC tokens may use aud as string or array
    const aud =
      typeof decoded.aud === 'string'
        ? decoded.aud
        : Array.isArray(decoded.aud) && decoded.aud.length > 0 && typeof decoded.aud[0] === 'string'
          ? (decoded.aud[0] as string)
          : ''

    const roles = Array.isArray(decoded.roles)
      ? (decoded.roles as unknown[]).filter((r): r is string => typeof r === 'string')
      : []
    const scopes = Array.isArray(decoded.scopes)
      ? (decoded.scopes as unknown[]).filter((s): s is string => typeof s === 'string')
      : []

    return {
      userId,
      tenantId: typeof decoded.tenantId === 'string' ? decoded.tenantId : undefined,
      roles,
      scopes,
      exp: decoded.exp,
      iss: decoded.iss,
      aud,
      sub: typeof decoded.sub === 'string' ? decoded.sub : undefined,
    }
  } catch {
    return null
  }
}

function isTokenExpired(claims: JWTClaims): boolean {
  // Use <= to match JWT spec: token must not be accepted on or after exp
  return claims.exp <= Math.floor(Date.now() / 1000)
}

function getUserLens(claims: JWTClaims | null): 'platform' | 'tenant' {
  if (!claims) return 'tenant'
  if (claims.tenantId) return 'tenant'
  const isPlatformLevel =
    claims.roles.includes('platform-admin') || claims.roles.includes('super-admin')
  if (isPlatformLevel) return 'platform'
  // In demo mode, standard OIDC tokens lack tenant and role claims.
  // Default to 'platform' lens so DevTenantAutoSelector picks a tenant automatically.
  if (import.meta.env.VITE_DEMO_MODE === 'true') return 'platform'
  return 'tenant'
}

const AuthContext = createContext<AuthContextValue | null>(null)

interface AuthProviderProps {
  children: ReactNode
  initialToken?: string
}

export function AuthProvider({ children, initialToken }: AuthProviderProps) {
  // Tokens are stored in memory only - never persisted to localStorage/sessionStorage
  const [accessToken, setAccessToken] = useState<string | null>(() => {
    if (!initialToken) return null
    // Only store token if it parses successfully
    return parseJWT(initialToken) ? initialToken : null
  })
  const [claims, setClaims] = useState<JWTClaims | null>(() => {
    if (!initialToken) return null
    return parseJWT(initialToken)
  })

  const updateToken = useCallback((token: string | null) => {
    if (!token) {
      setAccessToken(null)
      setClaims(null)
      return
    }
    const parsed = parseJWT(token)
    if (!parsed) {
      // Malformed token - clear both token and claims
      setAccessToken(null)
      setClaims(null)
      return
    }
    setAccessToken(token)
    setClaims(parsed)
  }, [])

  const login = useCallback(
    (token: string) => {
      updateToken(token)
    },
    [updateToken],
  )

  const logout = useCallback(() => {
    updateToken(null)
  }, [updateToken])

  const refreshToken = useCallback(async (): Promise<boolean> => {
    try {
      const response = await fetch('/api/auth/refresh', {
        method: 'POST',
        credentials: 'include',
        headers: { 'Content-Type': 'application/json' },
      })

      if (!response.ok) {
        // Only clear auth state on 401 Unauthorized.
        // Transient errors (5xx, network) should not log the user out.
        if (response.status === 401) {
          updateToken(null)
        }
        return false
      }

      const data = (await response.json()) as { accessToken: string }
      const parsed = parseJWT(data.accessToken)
      if (!parsed) {
        // Server returned a malformed token - treat as refresh failure without clearing auth
        return false
      }
      updateToken(data.accessToken)
      return true
    } catch {
      // Network error - do not clear auth state (may be transient)
      return false
    }
  }, [updateToken])

  // Check token expiry on mount and set up refresh timer
  useEffect(() => {
    if (!claims || !accessToken) return
    if (isTokenExpired(claims)) {
      // Token already expired - attempt refresh
      // eslint-disable-next-line react-hooks/set-state-in-effect
      void refreshToken()
      return
    }

    // Schedule refresh 60 seconds before expiry
    const expiresInMs = claims.exp * 1000 - Date.now()
    const refreshInMs = Math.max(expiresInMs - 60_000, 0)

    const timer = setTimeout(() => {
      void refreshToken()
    }, refreshInMs)

    return () => clearTimeout(timer)
  }, [claims, accessToken, refreshToken])

  const lens = getUserLens(claims)
  const isAuthenticated = claims !== null && !isTokenExpired(claims)

  // Expose login bypass for dev mode and E2E CI builds (VITE_E2E_MODE=true)
  useEffect(() => {
    if (import.meta.env.DEV || import.meta.env.VITE_E2E_MODE === 'true') {
      ;(window as unknown as Record<string, unknown>).__DEV_LOGIN__ = login
      return () => {
        delete (window as unknown as Record<string, unknown>).__DEV_LOGIN__
      }
    }
  }, [login])

  const value: AuthContextValue = {
    isAuthenticated,
    claims,
    accessToken,
    lens,
    login,
    logout,
    refreshToken,
  }

  return <AuthContext.Provider value={value}>{children}</AuthContext.Provider>
}

// eslint-disable-next-line react-refresh/only-export-components
export function useAuth(): AuthContextValue {
  const ctx = useContext(AuthContext)
  if (!ctx) {
    throw new Error('useAuth must be used within an AuthProvider')
  }
  return ctx
}
