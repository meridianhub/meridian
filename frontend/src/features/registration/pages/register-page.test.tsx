import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { MemoryRouter, Route, Routes } from 'react-router-dom'
import { renderWithProviders } from '@/test/test-utils'
import { RegisterPage } from './register-page'

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
    vi.spyOn(global, 'fetch').mockResolvedValue(
      new Response(JSON.stringify({ tenant_id: 'my-org', login_url: '/login?tenant=my-org' }), {
        status: 201,
        headers: { 'Content-Type': 'application/json' },
      }),
    )
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
    expect(screen.getByLabelText(/password/i)).toBeInTheDocument()
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
    await new Promise((resolve) => setTimeout(resolve, 400))
    await waitFor(() => {
      expect(screen.getByText(/at least 3 characters/i)).toBeInTheDocument()
    })
  }, 8000)

  it('shows slug format ok message for valid slug after debounce', async () => {
    const { user } = setup()
    await user.type(screen.getByLabelText(/organization slug/i), 'my-org')
    await new Promise((resolve) => setTimeout(resolve, 400))
    await waitFor(() => {
      expect(screen.getByText(/slug format looks good/i)).toBeInTheDocument()
    })
  }, 8000)

  it('shows password strength indicator as user types', async () => {
    const { user } = setup()
    await user.type(screen.getByLabelText(/password/i), 'weakpass')
    expect(screen.getByText(/weak|fair|good|strong/i)).toBeInTheDocument()
  })

  it('submits the form and redirects to login on success', async () => {
    const { user } = setup()

    await user.type(screen.getByLabelText(/organization slug/i), 'my-org')
    await user.type(screen.getByLabelText(/email/i), 'admin@example.com')
    await user.type(screen.getByLabelText(/password/i), 'SecurePass123!')
    await user.click(screen.getByRole('button', { name: /create account/i }))

    await waitFor(() => {
      expect(screen.getByTestId('login-page')).toBeInTheDocument()
    })

    expect(global.fetch).toHaveBeenCalledWith(
      '/api/v1/register',
      expect.objectContaining({ method: 'POST' }),
    )
  })

  it('shows error on 409 slug conflict', async () => {
    vi.spyOn(global, 'fetch').mockResolvedValue(
      new Response(JSON.stringify({ error: 'slug is already taken' }), {
        status: 409,
        headers: { 'Content-Type': 'application/json' },
      }),
    )

    const { user } = setup()
    await user.type(screen.getByLabelText(/organization slug/i), 'taken-slug')
    await user.type(screen.getByLabelText(/email/i), 'admin@example.com')
    await user.type(screen.getByLabelText(/password/i), 'SecurePass123!')
    await user.click(screen.getByRole('button', { name: /create account/i }))

    await waitFor(() => {
      expect(screen.getByRole('alert')).toHaveTextContent(/already taken/i)
    })
  })

  it('shows error on 429 rate limit', async () => {
    vi.spyOn(global, 'fetch').mockResolvedValue(
      new Response(JSON.stringify({ error: 'too many requests' }), {
        status: 429,
        headers: { 'Content-Type': 'application/json' },
      }),
    )

    const { user } = setup()
    await user.type(screen.getByLabelText(/organization slug/i), 'my-org')
    await user.type(screen.getByLabelText(/email/i), 'admin@example.com')
    await user.type(screen.getByLabelText(/password/i), 'SecurePass123!')
    await user.click(screen.getByRole('button', { name: /create account/i }))

    await waitFor(() => {
      expect(screen.getByRole('alert')).toHaveTextContent(/too many registration attempts/i)
    })
  })

  it('shows error when password is too short', async () => {
    const { user } = setup()
    await user.type(screen.getByLabelText(/organization slug/i), 'my-org')
    await user.type(screen.getByLabelText(/email/i), 'admin@example.com')
    await user.type(screen.getByLabelText(/password/i), 'short')
    await user.click(screen.getByRole('button', { name: /create account/i }))

    await waitFor(() => {
      expect(screen.getByRole('alert')).toHaveTextContent(/at least 8 characters/i)
    })
  })

  it('shows error on network failure', async () => {
    vi.spyOn(global, 'fetch').mockRejectedValue(new Error('Network error'))

    const { user } = setup()
    await user.type(screen.getByLabelText(/organization slug/i), 'my-org')
    await user.type(screen.getByLabelText(/email/i), 'admin@example.com')
    await user.type(screen.getByLabelText(/password/i), 'SecurePass123!')
    await user.click(screen.getByRole('button', { name: /create account/i }))

    await waitFor(() => {
      expect(screen.getByRole('alert')).toHaveTextContent(/unable to reach/i)
    })
  })

  it('shows loading state while submitting', async () => {
    let resolveFetch!: (value: Response) => void
    vi.spyOn(global, 'fetch').mockImplementation(
      () => new Promise<Response>((resolve) => { resolveFetch = resolve }),
    )

    const { user } = setup()
    await user.type(screen.getByLabelText(/organization slug/i), 'my-org')
    await user.type(screen.getByLabelText(/email/i), 'admin@example.com')
    await user.type(screen.getByLabelText(/password/i), 'SecurePass123!')
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
    expect(document.activeElement).toBe(screen.getByLabelText(/password/i))
  })
})
