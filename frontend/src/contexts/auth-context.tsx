import { createContext, useContext, useState, useEffect, useCallback, useRef, type ReactNode } from 'react'
import { toast } from 'sonner'
import { getTenantSlugFromSubdomain } from '@/lib/tenant-utils'

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
      ? (decoded.roles as unknown[]).filter((r): r is string => typeof r === 'string').map(r => r.toLowerCase())
      : []
    const groups = Array.isArray(decoded.groups)
      ? (decoded.groups as unknown[]).filter((g): g is string => typeof g === 'string').map(g => g.toLowerCase())
      : []
    // Use roles if present, else fall back to groups (Dex uses groups instead of roles)
    const effectiveRoles = roles.length > 0 ? roles : groups
    const scopes = Array.isArray(decoded.scopes)
      ? (decoded.scopes as unknown[]).filter((s): s is string => typeof s === 'string')
      : []

    return {
      userId,
      tenantId: typeof decoded.tenantId === 'string' ? decoded.tenantId : undefined,
      roles: effectiveRoles,
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
  // Default to 'platform' lens only on the root domain so
  // DevTenantAutoSelector can pick a tenant. On a tenant subdomain the
  // user is implicitly scoped to that tenant and should see tenant lens
  // (no tenant selector dropdown).
  if (import.meta.env.VITE_DEMO_MODE === 'true') {
    const slug = getTenantSlugFromSubdomain(window.location.hostname)
    if (!slug) return 'platform'
  }
  return 'tenant'
}

const AuthContext = createContext<AuthContextValue | null>(null)

interface AuthProviderProps {
  children: ReactNode
  initialToken?: string
}

const SESSION_STORAGE_KEY = 'meridian_access_token'

/** How far before expiry (ms) to show the warning toast */
export const SESSION_WARNING_BEFORE_EXPIRY_MS = 120_000 // 2 minutes

/** Stable toast ID so we can dismiss programmatically */
const SESSION_WARNING_TOAST_ID = 'session-expiry-warning'

function restoreToken(initialToken?: string): { token: string | null; claims: JWTClaims | null } {
  // Prefer explicit initialToken over stored token
  const candidate = initialToken ?? sessionStorage.getItem(SESSION_STORAGE_KEY)
  if (!candidate) return { token: null, claims: null }

  const parsed = parseJWT(candidate)
  if (!parsed || isTokenExpired(parsed)) {
    sessionStorage.removeItem(SESSION_STORAGE_KEY)
    return { token: null, claims: null }
  }

  // Validate tenant match on restore (updateToken handles login/refresh paths)
  const currentSlug = getTenantSlugFromSubdomain(window.location.hostname)
  if (currentSlug && parsed.tenantId && parsed.tenantId !== currentSlug) {
    sessionStorage.removeItem(SESSION_STORAGE_KEY)
    return { token: null, claims: null }
  }

  sessionStorage.setItem(SESSION_STORAGE_KEY, candidate)
  return { token: candidate, claims: parsed }
}

export function AuthProvider({ children, initialToken }: AuthProviderProps) {
  const [restored] = useState(() => restoreToken(initialToken))
  const [accessToken, setAccessToken] = useState<string | null>(restored.token)
  const [claims, setClaims] = useState<JWTClaims | null>(restored.claims)

  // Auth generation counter: incremented whenever auth is explicitly cleared
  // (logout, 401, tenant mismatch). Any in-flight refresh that started before
  // the last clear will see a generation mismatch and discard its result,
  // preventing stale responses from reviving a cleared session.
  const authGenerationRef = useRef(0)

  const updateToken = useCallback((token: string | null, generation?: number): boolean => {
    // Discard response if auth was cleared after this refresh started.
    if (generation !== undefined && generation !== authGenerationRef.current) {
      return false
    }
    if (!token) {
      authGenerationRef.current += 1
      setAccessToken(null)
      setClaims(null)
      sessionStorage.removeItem(SESSION_STORAGE_KEY)
      return false
    }
    const parsed = parseJWT(token)
    if (!parsed) {
      // Malformed token - clear both token and claims
      authGenerationRef.current += 1
      setAccessToken(null)
      setClaims(null)
      sessionStorage.removeItem(SESSION_STORAGE_KEY)
      return false
    }
    // Validate that the token's tenantId matches the current subdomain tenant.
    // This prevents session bleeding across subdomains for all token paths
    // (restore, login, refresh).
    // Invariant: JWT tenantId holds the tenant slug (same value used in subdomains).
    // See tenant-context.tsx where claims.tenantId is used directly as tenantSlug.
    const currentSlug = getTenantSlugFromSubdomain(window.location.hostname)
    if (currentSlug && parsed.tenantId && parsed.tenantId !== currentSlug) {
      authGenerationRef.current += 1
      setAccessToken(null)
      setClaims(null)
      sessionStorage.removeItem(SESSION_STORAGE_KEY)
      return false
    }
    setAccessToken(token)
    setClaims(parsed)
    sessionStorage.setItem(SESSION_STORAGE_KEY, token)
    return true
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

  // In-flight promise ref: shared across the toast action and the background
  // timer so concurrent callers (e.g. button click racing with the auto-refresh
  // timer) reuse the same request instead of issuing two.
  const refreshInFlightRef = useRef<Promise<boolean> | null>(null)

  const refreshToken = useCallback((): Promise<boolean> => {
    if (refreshInFlightRef.current) return refreshInFlightRef.current

    // Capture generation at call time; discard result if auth is cleared
    // while the request is in flight (e.g., user logs out mid-refresh).
    const callGeneration = authGenerationRef.current

    const request = (async () => {
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
            updateToken(null, callGeneration)
          }
          return false
        }

        const data = (await response.json()) as { accessToken: string }
        const parsed = parseJWT(data.accessToken)
        if (!parsed || isTokenExpired(parsed)) {
          // Server returned a malformed or already-expired token - treat as refresh failure
          return false
        }
        return updateToken(data.accessToken, callGeneration)
      } catch {
        // Network error - do not clear auth state (may be transient)
        return false
      } finally {
        refreshInFlightRef.current = null
      }
    })()

    refreshInFlightRef.current = request
    return request
  }, [updateToken])

  // Prevents duplicate toast handlers when the user clicks "Extend session"
  // multiple times while the same in-flight request is still pending.
  const warningActionInFlightRef = useRef(false)

  // Show session expiry warning toast
  const showSessionWarning = useCallback(() => {
    toast.warning('Your session is about to expire.', {
      id: SESSION_WARNING_TOAST_ID,
      duration: Infinity,
      action: {
        label: 'Extend session',
        onClick: () => {
          if (warningActionInFlightRef.current) return
          warningActionInFlightRef.current = true
          void refreshToken()
            .then((ok) => {
              if (ok) {
                toast.dismiss(SESSION_WARNING_TOAST_ID)
                toast.success('Session extended.')
              } else {
                toast.error('Failed to extend session. Please refresh the page to log in again.')
              }
            })
            .finally(() => {
              warningActionInFlightRef.current = false
            })
        },
      },
    })
  }, [refreshToken])

  // Check token expiry on mount and set up refresh + warning timers
  useEffect(() => {
    if (!claims || !accessToken) return
    if (isTokenExpired(claims)) {
      // Token already expired - attempt refresh
      // eslint-disable-next-line react-hooks/set-state-in-effect
      void refreshToken()
      return
    }

    const expiresInMs = claims.exp * 1000 - Date.now()

    // Schedule warning toast ~2 minutes before expiry
    const warningInMs = Math.max(expiresInMs - SESSION_WARNING_BEFORE_EXPIRY_MS, 0)
    const warningTimer = setTimeout(() => {
      showSessionWarning()
    }, warningInMs)

    // Schedule refresh 60 seconds before expiry; dismiss the warning toast
    // only if the refresh succeeds so the CTA stays visible as a manual
    // recovery path on transient errors.
    const refreshInMs = Math.max(expiresInMs - 60_000, 0)
    const refreshTimer = setTimeout(() => {
      void refreshToken().then((ok) => {
        if (ok) {
          toast.dismiss(SESSION_WARNING_TOAST_ID)
        }
      })
    }, refreshInMs)

    return () => {
      clearTimeout(warningTimer)
      clearTimeout(refreshTimer)
      toast.dismiss(SESSION_WARNING_TOAST_ID)
    }
  }, [claims, accessToken, refreshToken, showSessionWarning])

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
