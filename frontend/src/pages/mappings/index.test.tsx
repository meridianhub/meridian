import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, waitFor } from '@testing-library/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { BrowserRouter } from 'react-router-dom'

// Mock the API context to avoid loading ungenerated proto files
vi.mock('@/api/context', () => ({
  useApiClients: vi.fn(),
}))

import { useApiClients } from '@/api/context'
import { MappingsPage } from './index'

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
      <BrowserRouter>{children}</BrowserRouter>
    </QueryClientProvider>
  )
}

function makeDefaultClients(mappings = []) {
  return {
    mapping: {
      listMappings: vi.fn().mockResolvedValue({
        mappings,
        nextPageToken: undefined,
        totalCount: mappings.length,
      }),
    },
  }
}

describe('MappingsPage', () => {
  beforeEach(() => {
    vi.mocked(useApiClients).mockReturnValue(makeDefaultClients() as never)
  })

  it('renders the page title', () => {
    render(
      <Wrapper>
        <MappingsPage />
      </Wrapper>,
    )
    expect(screen.getByRole('heading', { name: /gateway mappings/i })).toBeInTheDocument()
  })

  it('renders data table with mapping columns', async () => {
    render(
      <Wrapper>
        <MappingsPage />
      </Wrapper>,
    )

    await waitFor(() => {
      expect(screen.getByRole('columnheader', { name: /name/i })).toBeInTheDocument()
    })
  })

  it('renders target service column', async () => {
    render(
      <Wrapper>
        <MappingsPage />
      </Wrapper>,
    )

    await waitFor(() => {
      expect(screen.getByRole('columnheader', { name: /target service/i })).toBeInTheDocument()
    })
  })

  it('renders target rpc column', async () => {
    render(
      <Wrapper>
        <MappingsPage />
      </Wrapper>,
    )

    await waitFor(() => {
      expect(screen.getByRole('columnheader', { name: /target rpc/i })).toBeInTheDocument()
    })
  })

  it('renders version column', async () => {
    render(
      <Wrapper>
        <MappingsPage />
      </Wrapper>,
    )

    await waitFor(() => {
      expect(screen.getByRole('columnheader', { name: /version/i })).toBeInTheDocument()
    })
  })

  it('renders status column', async () => {
    render(
      <Wrapper>
        <MappingsPage />
      </Wrapper>,
    )

    await waitFor(() => {
      expect(screen.getByRole('columnheader', { name: /status/i })).toBeInTheDocument()
    })
  })

  it('renders mapping rows when data is returned', async () => {
    vi.mocked(useApiClients).mockReturnValue(
      makeDefaultClients([
        {
          id: 'mapping-1',
          name: 'Stripe Webhook',
          targetService: 'meridian.payment_order.v1.PaymentOrderService',
          targetRpc: 'InitiatePaymentOrder',
          version: 1,
          status: 'MAPPING_STATUS_ACTIVE',
        },
      ]) as never,
    )

    render(
      <Wrapper>
        <MappingsPage />
      </Wrapper>,
    )

    await waitFor(() => {
      expect(screen.getByText('Stripe Webhook')).toBeInTheDocument()
    })
  })

  it('renders status filter', async () => {
    render(
      <Wrapper>
        <MappingsPage />
      </Wrapper>,
    )

    const selects = screen.getAllByRole('combobox')
    expect(selects.length).toBeGreaterThan(0)
  })

  it('renders empty state when no mappings', async () => {
    render(
      <Wrapper>
        <MappingsPage />
      </Wrapper>,
    )

    await waitFor(() => {
      expect(screen.getByTestId('empty-state')).toBeInTheDocument()
    })
  })
})
