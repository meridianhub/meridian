import { describe, it, expect } from 'vitest'
import { screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { render } from '@testing-library/react'
import { MemoryRouter, Route, Routes } from 'react-router-dom'
import { QueryClientProvider } from '@tanstack/react-query'
import { http, HttpResponse } from 'msw'
import { AuthProvider } from '@/contexts/auth-context'
import { TenantProvider } from '@/contexts/tenant-context'
import { OAuthConsentPage } from '@/pages/oauth-consent'
import { server } from '@/test/msw-handlers'
import { createTestQueryClient } from '@/test/test-utils'
import { createTenantUserToken } from '@/test/jwt-helpers'

const CONSENT_PATH = '/auth/mcp-consent?mcp_state=state-abc&client_id=client-123'

const MOCK_CONSENT_INFO = {
  client_name: 'My MCP App',
  redirect_uri: 'http://localhost:3000/callback',
  scopes: ['read', 'write'],
  is_dynamic_client: false,
}

function TestWrapper({
  children,
  initialToken,
  initialPath = CONSENT_PATH,
}: {
  children: React.ReactNode
  initialToken?: string
  initialPath?: string
}) {
  const queryClient = createTestQueryClient()
  return (
    <QueryClientProvider client={queryClient}>
      <AuthProvider initialToken={initialToken}>
        <TenantProvider>
          <MemoryRouter initialEntries={[initialPath]}>
            <Routes>
              <Route path="/login" element={<div data-testid="login-page">Login Page</div>} />
              <Route path="/auth/mcp-consent" element={children} />
            </Routes>
          </MemoryRouter>
        </TenantProvider>
      </AuthProvider>
    </QueryClientProvider>
  )
}

describe('OAuthConsentPage', () => {
  it('redirects unauthenticated user to /login with return_url', () => {
    render(
      <TestWrapper>
        <OAuthConsentPage />
      </TestWrapper>,
    )

    expect(screen.getByTestId('login-page')).toBeInTheDocument()
    expect(screen.queryByText('Authorize Access')).not.toBeInTheDocument()
  })

  it('fetches consent-info when authenticated', async () => {
    server.use(
      http.get('/mcp/consent-info', () => {
        return HttpResponse.json(MOCK_CONSENT_INFO)
      }),
    )

    const token = createTenantUserToken()
    render(
      <TestWrapper initialToken={token}>
        <OAuthConsentPage />
      </TestWrapper>,
    )

    await waitFor(() => {
      expect(screen.getByText('Authorize Access')).toBeInTheDocument()
    })
  })

  it('displays client name from consent-info response', async () => {
    server.use(
      http.get('/mcp/consent-info', () => {
        return HttpResponse.json(MOCK_CONSENT_INFO)
      }),
    )

    const token = createTenantUserToken()
    render(
      <TestWrapper initialToken={token}>
        <OAuthConsentPage />
      </TestWrapper>,
    )

    await waitFor(() => {
      expect(screen.getByText('My MCP App')).toBeInTheDocument()
    })
  })

  it('approve button sends correct POST with action approve', async () => {
    const user = userEvent.setup()
    let capturedBody: unknown = null

    server.use(
      http.get('/mcp/consent-info', () => {
        return HttpResponse.json(MOCK_CONSENT_INFO)
      }),
      http.post('/api/auth/mcp-consent', async ({ request }) => {
        capturedBody = await request.json()
        // Return a redirect; jsdom will attempt assignment but we just verify the body
        return HttpResponse.json({ redirect_url: 'http://localhost:3000/callback?code=xyz' })
      }),
    )

    const token = createTenantUserToken()
    render(
      <TestWrapper initialToken={token}>
        <OAuthConsentPage />
      </TestWrapper>,
    )

    await waitFor(() => {
      expect(screen.getByText('Approve')).toBeInTheDocument()
    })

    await user.click(screen.getByText('Approve'))

    await waitFor(() => {
      expect(capturedBody).toEqual({
        mcp_state: 'state-abc',
        client_id: 'client-123',
        action: 'approve',
      })
    })
  })

  it('deny button sends correct POST with action deny', async () => {
    const user = userEvent.setup()
    let capturedBody: unknown = null

    server.use(
      http.get('/mcp/consent-info', () => {
        return HttpResponse.json(MOCK_CONSENT_INFO)
      }),
      http.post('/api/auth/mcp-consent', async ({ request }) => {
        capturedBody = await request.json()
        return HttpResponse.json({ redirect_url: 'http://localhost:3000/callback?error=access_denied' })
      }),
    )

    const token = createTenantUserToken()
    render(
      <TestWrapper initialToken={token}>
        <OAuthConsentPage />
      </TestWrapper>,
    )

    await waitFor(() => {
      expect(screen.getByText('Deny')).toBeInTheDocument()
    })

    await user.click(screen.getByText('Deny'))

    await waitFor(() => {
      expect(capturedBody).toEqual({
        mcp_state: 'state-abc',
        client_id: 'client-123',
        action: 'deny',
      })
    })
  })
})
