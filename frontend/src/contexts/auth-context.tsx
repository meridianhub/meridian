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

export function parseJWT(token: string): JWTClaims | null {
  if (!token) return null

  const parts = token.split('.')
  if (parts.length !== 3) return null

  try {
    const payload = parts[1]
    // Pad base64url to standard base64
    const padded = payload.replace(/-/g, '+').replace(/_/g, '/').padEnd(
      payload.length + ((4 - (payload.length % 4)) % 4),
      '=',
    )
    const decoded = JSON.parse(atob(padded))

    if (!decoded.userId || !decoded.exp || !decoded.iss || !decoded.aud) {
      return null
    }

    return {
      userId: decoded.userId,
      tenantId: decoded.tenantId,
      roles: Array.isArray(decoded.roles) ? decoded.roles : [],
      scopes: Array.isArray(decoded.scopes) ? decoded.scopes : [],
      exp: decoded.exp,
      iss: decoded.iss,
      aud: decoded.aud,
      sub: decoded.sub,
    }
  } catch {
    return null
  }
}

function isTokenExpired(claims: JWTClaims): boolean {
  return claims.exp < Math.floor(Date.now() / 1000)
}

function getUserLens(claims: JWTClaims | null): 'platform' | 'tenant' {
  if (!claims) return 'tenant'
  if (claims.tenantId) return 'tenant'
  const isPlatformLevel =
    claims.roles.includes('platform-admin') || claims.roles.includes('super-admin')
  return isPlatformLevel ? 'platform' : 'tenant'
}

const AuthContext = createContext<AuthContextValue | null>(null)

interface AuthProviderProps {
  children: ReactNode
  initialToken?: string
}

export function AuthProvider({ children, initialToken }: AuthProviderProps) {
  // Tokens are stored in memory only - never persisted to localStorage/sessionStorage
  const [accessToken, setAccessToken] = useState<string | null>(initialToken ?? null)
  const [claims, setClaims] = useState<JWTClaims | null>(() => {
    if (!initialToken) return null
    return parseJWT(initialToken)
  })

  const updateToken = useCallback((token: string | null) => {
    setAccessToken(token)
    if (!token) {
      setClaims(null)
      return
    }
    const parsed = parseJWT(token)
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
        updateToken(null)
        return false
      }

      const data = (await response.json()) as { accessToken: string }
      updateToken(data.accessToken)
      return true
    } catch {
      updateToken(null)
      return false
    }
  }, [updateToken])

  // Check token expiry on mount and set up refresh timer
  useEffect(() => {
    if (!claims || !accessToken) return
    if (isTokenExpired(claims)) {
      // Token already expired - attempt refresh
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

export function useAuth(): AuthContextValue {
  const ctx = useContext(AuthContext)
  if (!ctx) {
    throw new Error('useAuth must be used within an AuthProvider')
  }
  return ctx
}
