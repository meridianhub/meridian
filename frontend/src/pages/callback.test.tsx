import { describe, it, expect, beforeEach } from 'vitest'
import { render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { MemoryRouter, Routes, Route } from 'react-router-dom'
import { QueryClientProvider, QueryClient } from '@tanstack/react-query'
import { http, HttpResponse } from 'msw'
import { server } from '@/test/msw-handlers'
import { AuthProvider } from '@/contexts/auth-context'
import { CallbackPage } from './callback'

function renderCallback(initialUrl: string) {
  const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  return render(
    <QueryClientProvider client={queryClient}>
      <AuthProvider>
        <MemoryRouter initialEntries={[initialUrl]}>
          <Routes>
            <Route path="/callback" element={<CallbackPage />} />
            <Route path="/login" element={<div data-testid="login-page">Login</div>} />
            <Route path="/" element={<div data-testid="home-page">Home</div>} />
          </Routes>
        </MemoryRouter>
      </AuthProvider>
    </QueryClientProvider>,
  )
}

describe('CallbackPage', () => {
  beforeEach(() => {
    sessionStorage.clear()
  })

  it('shows error when error param is present', async () => {
    renderCallback('/callback?error=access_denied&error_description=User+denied+access')

    await waitFor(() => {
      expect(screen.getByText('User denied access')).toBeInTheDocument()
    })
    expect(screen.getByText('Return to Login')).toBeInTheDocument()
  })

  it('shows error when code or state is missing', async () => {
    renderCallback('/callback')

    await waitFor(() => {
      expect(screen.getByText('Missing authorization code or state parameter')).toBeInTheDocument()
    })
  })

  it('shows error when state does not match', async () => {
    sessionStorage.setItem('meridian_pkce_state', 'expected-state')
    renderCallback('/callback?code=test-code&state=wrong-state')

    await waitFor(() => {
      expect(screen.getByText('Invalid state parameter - possible CSRF attack')).toBeInTheDocument()
    })
  })

  it('shows error when verifier is missing', async () => {
    sessionStorage.setItem('meridian_pkce_state', 'test-state')
    renderCallback('/callback?code=test-code&state=test-state')

    await waitFor(() => {
      expect(screen.getByText('Missing PKCE verifier - please try signing in again')).toBeInTheDocument()
    })
  })

  it('exchanges code for token and navigates home on success', async () => {
    const validJwt = [
      btoa(JSON.stringify({ alg: 'none', typ: 'JWT' })),
      btoa(JSON.stringify({ sub: 'user-1', exp: Math.floor(Date.now() / 1000) + 3600, iss: 'dex', aud: 'meridian-service' })),
      'sig',
    ].join('.')

    server.use(
      http.post('*/dex/token', () => {
        return HttpResponse.json({ id_token: validJwt })
      }),
    )

    sessionStorage.setItem('meridian_pkce_state', 'test-state')
    sessionStorage.setItem('meridian_pkce_verifier', 'test-verifier')
    renderCallback('/callback?code=test-code&state=test-state')

    await waitFor(() => {
      expect(screen.getByTestId('home-page')).toBeInTheDocument()
    })

    // PKCE values should be cleaned up
    expect(sessionStorage.getItem('meridian_pkce_state')).toBeNull()
    expect(sessionStorage.getItem('meridian_pkce_verifier')).toBeNull()
  })

  it('shows error on token exchange failure', async () => {
    server.use(
      http.post('*/dex/token', () => {
        return HttpResponse.json({ error: 'invalid_grant' }, { status: 400 })
      }),
    )

    sessionStorage.setItem('meridian_pkce_state', 'test-state')
    sessionStorage.setItem('meridian_pkce_verifier', 'test-verifier')
    renderCallback('/callback?code=test-code&state=test-state')

    await waitFor(() => {
      expect(screen.getByText('Authorization code expired or invalid')).toBeInTheDocument()
    })
  })

  it('navigates to login when Return to Login is clicked', async () => {
    const user = userEvent.setup()
    renderCallback('/callback?error=access_denied')

    await waitFor(() => {
      expect(screen.getByText('Return to Login')).toBeInTheDocument()
    })

    await user.click(screen.getByText('Return to Login'))

    await waitFor(() => {
      expect(screen.getByTestId('login-page')).toBeInTheDocument()
    })
  })
})
