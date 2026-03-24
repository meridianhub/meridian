import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { MemoryRouter, Route, Routes } from 'react-router-dom'
import { renderWithProviders } from '@/test/test-utils'
import { LoginPage } from './login'

// Mock hooks used by LoginPage
const mockLogin = vi.fn()
const mockNavigate = vi.fn()
const mockStartFlow = vi.fn()
vi.mock('@/contexts/auth-context', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@/contexts/auth-context')>()
  return {
    ...actual,
    useAuth: () => ({ accessToken: null, claims: null, login: mockLogin, logout: vi.fn() }),
  }
})

vi.mock('react-router-dom', async (importOriginal) => {
  const actual = await importOriginal<typeof import('react-router-dom')>()
  return {
    ...actual,
    useNavigate: () => mockNavigate,
  }
})

const mockState = { providers: [] as { id: string; type: string; displayName: string }[] }

vi.mock('@/hooks/use-auth-providers', () => ({
  useAuthProviders: () => ({ data: mockState.providers }),
}))

vi.mock('@/hooks/use-oauth-flow', () => ({
  useOAuthFlow: () => ({ startFlow: mockStartFlow }),
}))

vi.mock('@/lib/tenant-utils', () => ({
  isBaseDomain: () => false,
  getTenantSlugFromSubdomain: () => null,
}))

function setup(initialEntries: string[] = ['/login']) {
  const user = userEvent.setup({ delay: null })
  renderWithProviders(
    <MemoryRouter initialEntries={initialEntries}>
      <Routes>
        <Route path="/login" element={<LoginPage />} />
        <Route path="/register" element={<div data-testid="register-page">Register</div>} />
        <Route path="/" element={<div data-testid="home-page">Home</div>} />
      </Routes>
    </MemoryRouter>,
  )
  return { user }
}

describe('LoginPage', () => {
  beforeEach(() => {
    mockState.providers = []
    vi.stubGlobal('fetch', vi.fn())
  })

  afterEach(() => {
    vi.restoreAllMocks()
    vi.unstubAllGlobals()
  })

  it('renders the page heading', () => {
    setup()
    expect(screen.getByRole('heading', { name: /meridian operations console/i })).toBeInTheDocument()
  })

  it('shows dev login buttons in dev mode', () => {
    setup()
    expect(screen.getByRole('button', { name: /platform admin/i })).toBeInTheDocument()
    expect(screen.getByRole('button', { name: /tenant user/i })).toBeInTheDocument()
  })

  it('dev login as platform admin creates JWT and navigates home', async () => {
    const { user } = setup()
    await user.click(screen.getByRole('button', { name: /platform admin/i }))
    expect(mockLogin).toHaveBeenCalledWith(expect.stringContaining('.'))
    expect(mockNavigate).toHaveBeenCalledWith('/')
  })

  it('dev login as tenant user creates JWT and navigates home', async () => {
    const { user } = setup()
    await user.click(screen.getByRole('button', { name: /tenant user/i }))
    expect(mockLogin).toHaveBeenCalledWith(expect.stringContaining('.'))
    expect(mockNavigate).toHaveBeenCalledWith('/')
  })

  it('dev login JWT for platform-admin includes correct role', async () => {
    const { user } = setup()
    await user.click(screen.getByRole('button', { name: /platform admin/i }))
    const token = mockLogin.mock.calls[0][0] as string
    const payload = JSON.parse(atob(token.split('.')[1]))
    expect(payload.roles).toContain('platform-admin')
    expect(payload.tenantId).toBeUndefined()
  })

  it('dev login JWT for tenant-user includes tenant', async () => {
    const { user } = setup()
    await user.click(screen.getByRole('button', { name: /tenant user/i }))
    const token = mockLogin.mock.calls[0][0] as string
    const payload = JSON.parse(atob(token.split('.')[1]))
    expect(payload.roles).toContain('tenant-user')
    expect(payload.tenantId).toBe('dev-tenant')
  })

  it('shows just-registered banner when registered=1 query param present', () => {
    setup(['/login?registered=1'])
    expect(screen.getByRole('status')).toHaveTextContent(/account created/i)
  })

  it('does not show registered banner without query param', () => {
    setup()
    expect(screen.queryByRole('status')).not.toBeInTheDocument()
  })

  it('shows external provider buttons when providers available', () => {
    mockState.providers = [
      { id: 'google', type: 'oidc', displayName: 'Google' },
    ]
    setup()
    expect(screen.getByText(/sign in with google/i)).toBeInTheDocument()
  })

  it('starts OAuth flow when provider button clicked', async () => {
    mockState.providers = [
      { id: 'google', type: 'oidc', displayName: 'Google' },
    ]
    const { user } = setup()
    await user.click(screen.getByText(/sign in with google/i))
    expect(mockStartFlow).toHaveBeenCalledWith('google')
  })

  it('does not show external providers when none available', () => {
    mockState.providers = []
    setup()
    expect(screen.queryByText(/sign in with google/i)).not.toBeInTheDocument()
  })
})

describe('LoginPage - production mode', () => {
  beforeEach(() => {
    mockState.providers = []
    vi.stubGlobal('fetch', vi.fn())
    // Override import.meta.env.DEV to false for production mode tests
    vi.stubEnv('DEV', false as unknown as string)
  })

  afterEach(() => {
    vi.restoreAllMocks()
    vi.unstubAllGlobals()
  })

  it('shows password login form in production mode', () => {
    setup()
    expect(screen.getByLabelText(/email/i)).toBeInTheDocument()
    expect(screen.getByLabelText(/password/i)).toBeInTheDocument()
    expect(screen.getByRole('button', { name: /sign in/i })).toBeInTheDocument()
  })

  it('shows registration link in production mode', () => {
    setup()
    expect(screen.getByRole('link', { name: /create one/i })).toBeInTheDocument()
  })

  it('submits login form and navigates home on success', async () => {
    vi.mocked(fetch).mockResolvedValue(
      new Response(JSON.stringify({ access_token: 'test-token-123' }), {
        status: 200,
        headers: { 'Content-Type': 'application/json' },
      }),
    )

    const { user } = setup()
    await user.type(screen.getByLabelText(/email/i), 'admin@test.com')
    await user.type(screen.getByLabelText(/password/i), 'password123')
    await user.click(screen.getByRole('button', { name: /sign in/i }))

    await waitFor(() => {
      expect(mockLogin).toHaveBeenCalledWith('test-token-123')
    })
    expect(mockNavigate).toHaveBeenCalledWith('/')
  })

  it('shows error on failed login', async () => {
    vi.mocked(fetch).mockResolvedValue(
      new Response(JSON.stringify({ error: 'Invalid credentials' }), {
        status: 401,
        headers: { 'Content-Type': 'application/json' },
      }),
    )

    const { user } = setup()
    await user.type(screen.getByLabelText(/email/i), 'bad@test.com')
    await user.type(screen.getByLabelText(/password/i), 'wrong')
    await user.click(screen.getByRole('button', { name: /sign in/i }))

    await waitFor(() => {
      expect(screen.getByRole('alert')).toHaveTextContent(/invalid credentials/i)
    })
  })

  it('shows fallback error when response has no error field', async () => {
    vi.mocked(fetch).mockResolvedValue(
      new Response('not json', { status: 401 }),
    )

    const { user } = setup()
    await user.type(screen.getByLabelText(/email/i), 'bad@test.com')
    await user.type(screen.getByLabelText(/password/i), 'wrong')
    await user.click(screen.getByRole('button', { name: /sign in/i }))

    await waitFor(() => {
      expect(screen.getByRole('alert')).toHaveTextContent(/authentication failed/i)
    })
  })

  it('shows error when no token in response', async () => {
    vi.mocked(fetch).mockResolvedValue(
      new Response(JSON.stringify({}), {
        status: 200,
        headers: { 'Content-Type': 'application/json' },
      }),
    )

    const { user } = setup()
    await user.type(screen.getByLabelText(/email/i), 'admin@test.com')
    await user.type(screen.getByLabelText(/password/i), 'password123')
    await user.click(screen.getByRole('button', { name: /sign in/i }))

    await waitFor(() => {
      expect(screen.getByRole('alert')).toHaveTextContent(/no token received/i)
    })
  })

  it('shows error on network failure', async () => {
    vi.mocked(fetch).mockRejectedValue(new Error('Network error'))

    const { user } = setup()
    await user.type(screen.getByLabelText(/email/i), 'admin@test.com')
    await user.type(screen.getByLabelText(/password/i), 'password123')
    await user.click(screen.getByRole('button', { name: /sign in/i }))

    await waitFor(() => {
      expect(screen.getByRole('alert')).toHaveTextContent(/unable to reach/i)
    })
  })

  it('shows loading state while submitting', async () => {
    let resolveFetch!: (value: Response) => void
    vi.mocked(fetch).mockImplementation(
      () => new Promise<Response>((resolve) => { resolveFetch = resolve }),
    )

    const { user } = setup()
    await user.type(screen.getByLabelText(/email/i), 'admin@test.com')
    await user.type(screen.getByLabelText(/password/i), 'password123')
    await user.click(screen.getByRole('button', { name: /sign in/i }))

    await waitFor(() => {
      expect(screen.getByRole('button', { name: /signing in/i })).toBeDisabled()
    })

    resolveFetch(
      new Response(JSON.stringify({ access_token: 'tok' }), { status: 200 }),
    )
  })
})

// Note: bare domain + production mode branch is difficult to test in vitest because
// import.meta.env.DEV is baked in at module level and vi.mock for tenant-utils is
// hoisted. The branch is exercised in E2E tests instead.
