import { describe, it, expect, vi } from 'vitest'
import { render, screen, act } from '@testing-library/react'
import { AuthProvider } from '@/contexts/auth-context'
import {
  useIsAuthenticated,
  useUserClaims,
  useUserLens,
  useHasRole,
  useHasScope,
} from '@/hooks/use-auth'
import {
  createTestToken,
  createPlatformAdminToken,
  createTenantUserToken,
} from '@/test/jwt-helpers'

// Mock fetch for token refresh
global.fetch = vi.fn()

function renderWithAuth(
  component: React.ReactNode,
  token?: string,
): ReturnType<typeof render> {
  return render(<AuthProvider initialToken={token}>{component}</AuthProvider>)
}

describe('useIsAuthenticated', () => {
  it('returns false when not authenticated', async () => {
    const TestComponent = () => {
      const isAuthenticated = useIsAuthenticated()
      return <span data-testid="result">{String(isAuthenticated)}</span>
    }

    await act(async () => {
      renderWithAuth(<TestComponent />)
    })

    expect(screen.getByTestId('result').textContent).toBe('false')
  })

  it('returns true when authenticated', async () => {
    const token = createTestToken({ userId: 'user-123' })

    const TestComponent = () => {
      const isAuthenticated = useIsAuthenticated()
      return <span data-testid="result">{String(isAuthenticated)}</span>
    }

    await act(async () => {
      renderWithAuth(<TestComponent />, token)
    })

    expect(screen.getByTestId('result').textContent).toBe('true')
  })
})

describe('useUserClaims', () => {
  it('returns null when not authenticated', async () => {
    const TestComponent = () => {
      const claims = useUserClaims()
      return <span data-testid="result">{claims ? 'has-claims' : 'null'}</span>
    }

    await act(async () => {
      renderWithAuth(<TestComponent />)
    })

    expect(screen.getByTestId('result').textContent).toBe('null')
  })

  it('returns claims when authenticated', async () => {
    const token = createTestToken({ userId: 'user-456', roles: ['editor'] })

    const TestComponent = () => {
      const claims = useUserClaims()
      return (
        <div>
          <span data-testid="userId">{claims?.userId ?? 'none'}</span>
          <span data-testid="roles">{claims?.roles.join(',') ?? 'none'}</span>
        </div>
      )
    }

    await act(async () => {
      renderWithAuth(<TestComponent />, token)
    })

    expect(screen.getByTestId('userId').textContent).toBe('user-456')
    expect(screen.getByTestId('roles').textContent).toBe('editor')
  })
})

describe('useUserLens', () => {
  it('returns tenant when not authenticated', async () => {
    const TestComponent = () => {
      const lens = useUserLens()
      return <span data-testid="result">{lens}</span>
    }

    await act(async () => {
      renderWithAuth(<TestComponent />)
    })

    expect(screen.getByTestId('result').textContent).toBe('tenant')
  })

  it('returns platform for platform-admin without tenantId', async () => {
    const token = createPlatformAdminToken()

    const TestComponent = () => {
      const lens = useUserLens()
      return <span data-testid="result">{lens}</span>
    }

    await act(async () => {
      renderWithAuth(<TestComponent />, token)
    })

    expect(screen.getByTestId('result').textContent).toBe('platform')
  })

  it('returns tenant for regular tenant user', async () => {
    const token = createTenantUserToken('tenant-001')

    const TestComponent = () => {
      const lens = useUserLens()
      return <span data-testid="result">{lens}</span>
    }

    await act(async () => {
      renderWithAuth(<TestComponent />, token)
    })

    expect(screen.getByTestId('result').textContent).toBe('tenant')
  })
})

describe('useHasRole', () => {
  it('returns false when not authenticated', async () => {
    const TestComponent = () => {
      const hasRole = useHasRole('admin')
      return <span data-testid="result">{String(hasRole)}</span>
    }

    await act(async () => {
      renderWithAuth(<TestComponent />)
    })

    expect(screen.getByTestId('result').textContent).toBe('false')
  })

  it('returns true for matching role', async () => {
    const token = createTestToken({ userId: 'user-1', roles: ['admin', 'viewer'] })

    const TestComponent = () => {
      const hasAdmin = useHasRole('admin')
      return <span data-testid="result">{String(hasAdmin)}</span>
    }

    await act(async () => {
      renderWithAuth(<TestComponent />, token)
    })

    expect(screen.getByTestId('result').textContent).toBe('true')
  })

  it('returns false for non-matching role', async () => {
    const token = createTestToken({ userId: 'user-1', roles: ['viewer'] })

    const TestComponent = () => {
      const hasAdmin = useHasRole('admin')
      return <span data-testid="result">{String(hasAdmin)}</span>
    }

    await act(async () => {
      renderWithAuth(<TestComponent />, token)
    })

    expect(screen.getByTestId('result').textContent).toBe('false')
  })
})

describe('useHasScope', () => {
  it('returns false when not authenticated', async () => {
    const TestComponent = () => {
      const hasScope = useHasScope('write')
      return <span data-testid="result">{String(hasScope)}</span>
    }

    await act(async () => {
      renderWithAuth(<TestComponent />)
    })

    expect(screen.getByTestId('result').textContent).toBe('false')
  })

  it('returns true for matching scope', async () => {
    const token = createTestToken({ userId: 'user-1', scopes: ['read', 'write'] })

    const TestComponent = () => {
      const hasWrite = useHasScope('write')
      return <span data-testid="result">{String(hasWrite)}</span>
    }

    await act(async () => {
      renderWithAuth(<TestComponent />, token)
    })

    expect(screen.getByTestId('result').textContent).toBe('true')
  })

  it('returns false for non-matching scope', async () => {
    const token = createTestToken({ userId: 'user-1', scopes: ['read'] })

    const TestComponent = () => {
      const hasWrite = useHasScope('write')
      return <span data-testid="result">{String(hasWrite)}</span>
    }

    await act(async () => {
      renderWithAuth(<TestComponent />, token)
    })

    expect(screen.getByTestId('result').textContent).toBe('false')
  })
})
