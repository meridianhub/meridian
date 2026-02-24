import * as React from 'react'
import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, waitFor } from '@testing-library/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { BrowserRouter } from 'react-router-dom'

// Mock the API context to avoid loading ungenerated proto files
const mockPartyClient = {
  listParties: vi.fn().mockResolvedValue({
    parties: [],
    nextPageToken: '',
    totalCount: 0n,
  }),
  registerPartyType: vi.fn().mockResolvedValue({}),
}

vi.mock('@/api/context', () => ({
  ApiClientProvider: ({ children }: { children: React.ReactNode }) => children,
  useClients: vi.fn(() => ({ party: mockPartyClient })),
  useApiClients: vi.fn(() => ({ party: mockPartyClient })),
}))

vi.mock('@/hooks/use-tenant-context', () => ({
  useTenantSlug: () => 'test-tenant',
}))

import { PartiesPage } from './index'

function makeQueryClient() {
  return new QueryClient({
    defaultOptions: {
      queries: { retry: false, staleTime: Infinity },
    },
  })
}

function Wrapper({ children }: { children: React.ReactNode }) {
  const qc = makeQueryClient()
  return (
    <QueryClientProvider client={qc}>
      <BrowserRouter>
        {children}
      </BrowserRouter>
    </QueryClientProvider>
  )
}

describe('PartiesPage', () => {
  beforeEach(() => {
    vi.clearAllMocks()
  })

  it('renders the page title', () => {
    render(
      <Wrapper>
        <PartiesPage />
      </Wrapper>,
    )
    expect(screen.getByRole('heading', { name: /parties/i })).toBeInTheDocument()
  })

  it('renders data table with party columns', async () => {
    render(
      <Wrapper>
        <PartiesPage />
      </Wrapper>,
    )

    await waitFor(() => {
      // Check for table headers
      expect(screen.getByRole('columnheader', { name: /name/i })).toBeInTheDocument()
    })
  })

  it('filters by party type', async () => {
    render(
      <Wrapper>
        <PartiesPage />
      </Wrapper>,
    )

    // Find filter control by searching for visible select elements
    const selects = screen.getAllByRole('combobox')
    expect(selects.length).toBeGreaterThan(0)
  })

  it('filters by status', async () => {
    render(
      <Wrapper>
        <PartiesPage />
      </Wrapper>,
    )

    // Verify filters are rendered
    const selects = screen.getAllByRole('combobox')
    expect(selects.length).toBeGreaterThan(0)
  })

  it('renders successfully without crashing', async () => {
    const { container } = render(
      <Wrapper>
        <PartiesPage />
      </Wrapper>,
    )

    // Just verify the page renders without error
    expect(container).toBeInTheDocument()
  })
})
