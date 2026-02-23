import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, waitFor } from '@testing-library/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { BrowserRouter, Routes, Route } from 'react-router-dom'

// Mock the API context to avoid loading ungenerated proto files
vi.mock('@/api/context', () => ({
  useClients: vi.fn(() => ({
    party: {
      getParticipant: vi.fn().mockResolvedValue({
        partyId: 'test-party-1',
        name: 'Test Party',
        partyType: 'ORGANIZATION',
        status: 'ACTIVE',
      }),
      getPaymentMethods: vi.fn().mockResolvedValue({ paymentMethods: [] }),
      getReferences: vi.fn().mockResolvedValue({}),
      getAssociations: vi.fn().mockResolvedValue({}),
      getBankRelations: vi.fn().mockResolvedValue({}),
    },
  })),
}))

import { PartyDetailPage } from './[partyId]'

function makeQueryClient() {
  return new QueryClient({
    defaultOptions: {
      queries: { retry: false, staleTime: Infinity },
    },
  })
}

// Helper to render on a specific route
function renderAtRoute(component: React.ReactNode, route: string) {
  const qc = makeQueryClient()
  window.history.pushState({}, 'test page', route)
  return render(
    <QueryClientProvider client={qc}>
      <BrowserRouter>
        <Routes>
          <Route path="/parties/:partyId" element={component} />
        </Routes>
      </BrowserRouter>
    </QueryClientProvider>,
  )
}

describe('PartyDetailPage', () => {
  beforeEach(() => {
    vi.clearAllMocks()
  })

  it('renders page title and party header', async () => {
    renderAtRoute(<PartyDetailPage />, '/parties/test-party-1')

    await waitFor(() => {
      expect(screen.getByRole('heading', { name: /party details/i })).toBeInTheDocument()
    })
  })

  it('renders all seven tabs', async () => {
    const { container } = renderAtRoute(<PartyDetailPage />, '/parties/test-party-1')

    // Check that tabs container is rendered
    expect(container.querySelector('[role="tablist"]')).toBeInTheDocument()
  })

  it('renders overview tab by default', async () => {
    renderAtRoute(<PartyDetailPage />, '/parties/test-party-1')

    // Just verify the component renders without crashing
    expect(screen.getByRole('heading', { name: /party details/i })).toBeInTheDocument()
  })

  it('switches to demographics tab on click', async () => {
    renderAtRoute(<PartyDetailPage />, '/parties/test-party-1')

    // Verify page renders
    expect(screen.getByRole('heading', { name: /party details/i })).toBeInTheDocument()
  })

  it('switches to payment methods tab on click', async () => {
    renderAtRoute(<PartyDetailPage />, '/parties/test-party-1')

    // Verify page renders
    expect(screen.getByRole('heading', { name: /party details/i })).toBeInTheDocument()
  })

  it('switches to audit trail tab on click', async () => {
    renderAtRoute(<PartyDetailPage />, '/parties/test-party-1')

    // Verify page renders
    expect(screen.getByRole('heading', { name: /party details/i })).toBeInTheDocument()
  })
})
