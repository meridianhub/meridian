import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest'
import { render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { MemoryRouter, Routes, Route } from 'react-router-dom'
import { QueryClientProvider, QueryClient } from '@tanstack/react-query'
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
    // Ensure hash is clean before each test
    window.location.hash = ''
  })

  afterEach(() => {
    window.location.hash = ''
    vi.restoreAllMocks()
  })

  it('shows error when error param is present in search params', async () => {
    renderCallback('/callback?error=access_denied&error_description=User+denied+access')

    await waitFor(() => {
      expect(screen.getByText('User denied access')).toBeInTheDocument()
    })
    expect(screen.getByText('Return to Login')).toBeInTheDocument()
  })

  it('shows error when no token and no error params are present', async () => {
    renderCallback('/callback')

    await waitFor(() => {
      expect(screen.getByText('No authentication token received')).toBeInTheDocument()
    })
  })

  it('extracts token from URL fragment and navigates home', async () => {
    const validJwt = [
      btoa(JSON.stringify({ alg: 'none', typ: 'JWT' })),
      btoa(JSON.stringify({ sub: 'user-1', exp: Math.floor(Date.now() / 1000) + 3600, iss: 'dex', aud: 'meridian-service' })),
      'sig',
    ].join('.')

    // Set hash fragment before rendering (simulates BFF redirect)
    window.location.hash = `#access_token=${validJwt}`
    const replaceStateSpy = vi.spyOn(window.history, 'replaceState').mockImplementation(() => {})

    renderCallback('/callback')

    await waitFor(() => {
      expect(screen.getByTestId('home-page')).toBeInTheDocument()
    })

    // Should clear the fragment from URL for security
    expect(replaceStateSpy).toHaveBeenCalled()
  })

  it('shows error when error_description is missing but error is present', async () => {
    renderCallback('/callback?error=server_error')

    await waitFor(() => {
      expect(screen.getByText('server_error')).toBeInTheDocument()
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
