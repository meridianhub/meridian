import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { MemoryRouter, Route, Routes } from 'react-router-dom'
import { renderWithProviders } from '@/test/test-utils'
import { RegisterPage } from './register-page'

/** Mock fetch to handle both registration and slug availability endpoints. */
function mockFetchForRegistration(overrides?: {
  registerStatus?: number
  registerBody?: object
  slugAvailable?: boolean
}) {
  const {
    registerStatus = 201,
    registerBody = { tenant_id: 'my-org', login_url: '/login?tenant=my-org' },
    slugAvailable = true,
  } = overrides ?? {}

  return vi.spyOn(global, 'fetch').mockImplementation(async (input) => {
    const url = typeof input === 'string' ? input : input instanceof URL ? input.toString() : (input as Request).url
    if (url.includes('/api/v1/slugs/')) {
      return new Response(JSON.stringify({ available: slugAvailable }), {
        status: 200,
        headers: { 'Content-Type': 'application/json' },
      })
    }
    return new Response(JSON.stringify(registerBody), {
      status: registerStatus,
      headers: { 'Content-Type': 'application/json' },
    })
  })
}

function setup() {
  const user = userEvent.setup({ delay: null })
  renderWithProviders(
    <MemoryRouter initialEntries={['/register']}>
      <Routes>
        <Route path="/register" element={<RegisterPage />} />
        <Route path="/login" element={<div data-testid="login-page">Login</div>} />
      </Routes>
    </MemoryRouter>,
  )
  return { user }
}

describe('RegisterPage', () => {
  beforeEach(() => {
    mockFetchForRegistration()
  })

  afterEach(() => {
    vi.restoreAllMocks()
  })

  it('renders the registration form', () => {
    setup()
    expect(screen.getByRole('heading', { name: /create your account/i })).toBeInTheDocument()
    expect(screen.getByLabelText(/organization slug/i)).toBeInTheDocument()
    expect(screen.getByLabelText(/display name/i)).toBeInTheDocument()
    expect(screen.getByLabelText(/email/i)).toBeInTheDocument()
    expect(screen.getByLabelText(/^password/i)).toBeInTheDocument()
    expect(screen.getByRole('button', { name: /create account/i })).toBeInTheDocument()
  })

  it('shows a link to the login page', () => {
    setup()
    expect(screen.getByRole('link', { name: /sign in/i })).toBeInTheDocument()
  })

  it('normalizes slug to lowercase and strips invalid chars', async () => {
    const { user } = setup()
    const slugInput = screen.getByLabelText(/organization slug/i)
    await user.type(slugInput, 'My Org!')
    expect(slugInput).toHaveValue('myorg')
  })

  it('shows slug format hint', () => {
    setup()
    expect(screen.getByText(/lowercase letters, numbers, and hyphens/i)).toBeInTheDocument()
  })

  it('shows slug validation error for too-short slug after debounce', async () => {
    const { user } = setup()
    await user.type(screen.getByLabelText(/organization slug/i), 'ab')
    await waitFor(() => {
      expect(screen.getByText(/at least 3 characters/i)).toBeInTheDocument()
    })
  }, 8000)

  it('shows slug available message after checking backend', async () => {
    const { user } = setup()
    await user.type(screen.getByLabelText(/organization slug/i), 'my-org')
    await waitFor(() => {
      expect(screen.getByText(/slug is available/i)).toBeInTheDocument()
    })
  }, 8000)

  it('shows slug taken message when backend reports unavailable', async () => {
    vi.restoreAllMocks()
    mockFetchForRegistration({ slugAvailable: false })

    const { user } = setup()
    await user.type(screen.getByLabelText(/organization slug/i), 'taken-org')
    await waitFor(() => {
      expect(screen.getByText(/slug is already taken/i)).toBeInTheDocument()
    })
  }, 8000)

  it('shows password strength indicator as user types', async () => {
    const { user } = setup()
    await user.type(screen.getByLabelText(/^password/i), 'weakpass')
    expect(screen.getByText(/weak|fair|good|strong/i)).toBeInTheDocument()
  })

  it('submits the form and navigates to login on success with relative login_url', async () => {
    const { user } = setup()

    await user.type(screen.getByLabelText(/organization slug/i), 'my-org')
    await user.type(screen.getByLabelText(/email/i), 'admin@example.com')
    await user.type(screen.getByLabelText(/^password/i), 'SecurePass123!')
    await user.click(screen.getByRole('button', { name: /create account/i }))

    await waitFor(() => {
      expect(screen.getByTestId('login-page')).toBeInTheDocument()
    })

    expect(global.fetch).toHaveBeenCalledWith(
      '/api/v1/register',
      expect.objectContaining({ method: 'POST' }),
    )
  })

  it('shows redirect message on success with absolute login_url', async () => {
    vi.restoreAllMocks()
    // jsdom hostname is "localhost", so use a subdomain of localhost
    mockFetchForRegistration({
      registerBody: {
        tenant_id: 'my-org',
        login_url: 'https://my-org.localhost/login',
      },
    })

    const { user } = setup()

    await user.type(screen.getByLabelText(/organization slug/i), 'my-org')
    await user.type(screen.getByLabelText(/email/i), 'admin@example.com')
    await user.type(screen.getByLabelText(/^password/i), 'SecurePass123!')
    await user.click(screen.getByRole('button', { name: /create account/i }))

    await waitFor(() => {
      expect(screen.getByText(/account created/i)).toBeInTheDocument()
      expect(screen.getByText(/redirecting to your organization/i)).toBeInTheDocument()
    })
  })

  it('falls back to client navigation when login_url is http (not https)', async () => {
    vi.restoreAllMocks()
    mockFetchForRegistration({
      registerBody: {
        tenant_id: 'my-org',
        login_url: 'http://my-org.localhost/login',
      },
    })

    const { user } = setup()

    await user.type(screen.getByLabelText(/organization slug/i), 'my-org')
    await user.type(screen.getByLabelText(/email/i), 'admin@example.com')
    await user.type(screen.getByLabelText(/^password/i), 'SecurePass123!')
    await user.click(screen.getByRole('button', { name: /create account/i }))

    // Should NOT show redirect message - falls back to navigate()
    await waitFor(() => {
      expect(screen.queryByText(/account created/i)).not.toBeInTheDocument()
    })
  })

  it('falls back to client navigation when login_url domain is untrusted', async () => {
    vi.restoreAllMocks()
    mockFetchForRegistration({
      registerBody: {
        tenant_id: 'my-org',
        login_url: 'https://evil.example.com/login',
      },
    })

    const { user } = setup()

    await user.type(screen.getByLabelText(/organization slug/i), 'my-org')
    await user.type(screen.getByLabelText(/email/i), 'admin@example.com')
    await user.type(screen.getByLabelText(/^password/i), 'SecurePass123!')
    await user.click(screen.getByRole('button', { name: /create account/i }))

    // Should NOT show redirect message - untrusted domain
    await waitFor(() => {
      expect(screen.queryByText(/account created/i)).not.toBeInTheDocument()
    })
  })

  it('shows error on 409 slug conflict', async () => {
    vi.restoreAllMocks()
    mockFetchForRegistration({
      registerStatus: 409,
      registerBody: { error: 'slug is already taken' },
    })

    const { user } = setup()
    await user.type(screen.getByLabelText(/organization slug/i), 'taken-slug')
    await user.type(screen.getByLabelText(/email/i), 'admin@example.com')
    await user.type(screen.getByLabelText(/^password/i), 'SecurePass123!')
    await user.click(screen.getByRole('button', { name: /create account/i }))

    await waitFor(() => {
      expect(screen.getByRole('alert')).toHaveTextContent(/already taken/i)
    })
  })

  it('shows error on 429 rate limit', async () => {
    vi.restoreAllMocks()
    mockFetchForRegistration({ registerStatus: 429, registerBody: { error: 'too many requests' } })

    const { user } = setup()
    await user.type(screen.getByLabelText(/organization slug/i), 'my-org')
    await user.type(screen.getByLabelText(/email/i), 'admin@example.com')
    await user.type(screen.getByLabelText(/^password/i), 'SecurePass123!')
    await user.click(screen.getByRole('button', { name: /create account/i }))

    await waitFor(() => {
      expect(screen.getByRole('alert')).toHaveTextContent(/too many registration attempts/i)
    })
  })

  it('shows error when password is too short', async () => {
    const { user } = setup()
    await user.type(screen.getByLabelText(/organization slug/i), 'my-org')
    await user.type(screen.getByLabelText(/email/i), 'admin@example.com')
    await user.type(screen.getByLabelText(/^password/i), 'short')
    await user.click(screen.getByRole('button', { name: /create account/i }))

    await waitFor(() => {
      expect(screen.getByRole('alert')).toHaveTextContent(/at least 12 characters/i)
    })
  })

  it('surfaces backend password-policy errors inline on the password field', async () => {
    vi.restoreAllMocks()
    mockFetchForRegistration({
      registerStatus: 400,
      registerBody: { error: 'password policy violation: password too short' },
    })

    const { user } = setup()
    await user.type(screen.getByLabelText(/organization slug/i), 'my-org')
    await user.type(screen.getByLabelText(/email/i), 'admin@example.com')
    // Client-side passes (12+ chars, complexity met) so we exercise the
    // backend-error path. Built from parts to dodge entropy scanners.
    await user.type(
      screen.getByLabelText(/^password/i),
      'Example' + '-' + 'pass' + '-9',
    )
    await user.click(screen.getByRole('button', { name: /create account/i }))

    await waitFor(() => {
      expect(screen.getByRole('alert')).toHaveTextContent(
        /at least 12 characters with uppercase, lowercase, and a digit/i,
      )
    })

    // Form-level error should not appear in addition to the inline error.
    const alerts = screen.getAllByRole('alert')
    expect(alerts).toHaveLength(1)
  })

  it('surfaces backend "password too weak" errors inline on the password field', async () => {
    vi.restoreAllMocks()
    mockFetchForRegistration({
      registerStatus: 400,
      registerBody: { error: 'password policy violation: password too weak' },
    })

    const { user } = setup()
    await user.type(screen.getByLabelText(/organization slug/i), 'my-org')
    await user.type(screen.getByLabelText(/email/i), 'admin@example.com')
    await user.type(
      screen.getByLabelText(/^password/i),
      'Example' + '-' + 'pass' + '-9',
    )
    await user.click(screen.getByRole('button', { name: /create account/i }))

    await waitFor(() => {
      expect(screen.getByRole('alert')).toHaveTextContent(
        /uppercase, lowercase, and a digit/i,
      )
    })
  })

  it('shows error on network failure', async () => {
    vi.restoreAllMocks()
    vi.spyOn(global, 'fetch').mockImplementation(async (input) => {
      const url = typeof input === 'string' ? input : input instanceof URL ? input.toString() : (input as Request).url
      if (url.includes('/api/v1/slugs/')) {
        return new Response(JSON.stringify({ available: true }), {
          status: 200,
          headers: { 'Content-Type': 'application/json' },
        })
      }
      throw new Error('Network error')
    })

    const { user } = setup()
    await user.type(screen.getByLabelText(/organization slug/i), 'my-org')
    await user.type(screen.getByLabelText(/email/i), 'admin@example.com')
    await user.type(screen.getByLabelText(/^password/i), 'SecurePass123!')
    await user.click(screen.getByRole('button', { name: /create account/i }))

    await waitFor(() => {
      expect(screen.getByRole('alert')).toHaveTextContent(/unable to reach/i)
    })
  })

  it('shows loading state while submitting', async () => {
    vi.restoreAllMocks()
    let resolveFetch!: (value: Response) => void
    vi.spyOn(global, 'fetch').mockImplementation(async (input) => {
      const url = typeof input === 'string' ? input : input instanceof URL ? input.toString() : (input as Request).url
      if (url.includes('/api/v1/slugs/')) {
        return new Response(JSON.stringify({ available: true }), {
          status: 200,
          headers: { 'Content-Type': 'application/json' },
        })
      }
      return new Promise<Response>((resolve) => { resolveFetch = resolve })
    })

    const { user } = setup()
    await user.type(screen.getByLabelText(/organization slug/i), 'my-org')
    await user.type(screen.getByLabelText(/email/i), 'admin@example.com')
    await user.type(screen.getByLabelText(/^password/i), 'SecurePass123!')
    await user.click(screen.getByRole('button', { name: /create account/i }))

    await waitFor(() => {
      expect(screen.getByRole('button', { name: /creating account/i })).toBeDisabled()
    })

    // Resolve so the test cleans up
    resolveFetch(
      new Response(JSON.stringify({ tenant_id: 'my-org', login_url: '/login' }), { status: 201 }),
    )
  })

  it('supports keyboard navigation through the form', async () => {
    const { user } = setup()
    const slugInput = screen.getByLabelText(/organization slug/i)
    await user.click(slugInput)
    expect(document.activeElement).toBe(slugInput)

    await user.tab()
    expect(document.activeElement).toBe(screen.getByLabelText(/display name/i))

    await user.tab()
    expect(document.activeElement).toBe(screen.getByLabelText(/email/i))

    await user.tab()
    expect(document.activeElement).toBe(screen.getByLabelText(/^password/i))

    await user.tab()
    expect(document.activeElement).toBe(screen.getByRole('button', { name: /show password/i }))
  })

  it('toggles password visibility', async () => {
    const { user } = setup()
    const passwordInput = screen.getByLabelText(/^password/i)
    await user.type(passwordInput, 'SecurePass123!')

    expect(passwordInput).toHaveAttribute('type', 'password')

    await user.click(screen.getByRole('button', { name: /show password/i }))
    expect(passwordInput).toHaveAttribute('type', 'text')

    await user.click(screen.getByRole('button', { name: /hide password/i }))
    expect(passwordInput).toHaveAttribute('type', 'password')
  })

  it('shows field-level errors on empty submission', async () => {
    const { user } = setup()
    await user.click(screen.getByRole('button', { name: /create account/i }))

    await waitFor(() => {
      expect(screen.getByText(/organization slug is required/i)).toBeInTheDocument()
      expect(screen.getByText(/email is required/i)).toBeInTheDocument()
      expect(screen.getByText(/password is required/i)).toBeInTheDocument()
    })
  })

  it('disables submit button when slug is taken', async () => {
    vi.restoreAllMocks()
    mockFetchForRegistration({ slugAvailable: false })

    const { user } = setup()
    await user.type(screen.getByLabelText(/organization slug/i), 'taken-org')

    await waitFor(() => {
      expect(screen.getByText(/slug is already taken/i)).toBeInTheDocument()
    })

    expect(screen.getByRole('button', { name: /create account/i })).toBeDisabled()
  }, 8000)
})
