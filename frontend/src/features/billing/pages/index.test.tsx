import { describe, it, expect, vi } from 'vitest'
import { render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { MemoryRouter } from 'react-router-dom'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { TooltipProvider } from '@/components/ui/tooltip'
import { AuthProvider } from '@/contexts/auth-context'
import { TenantProvider } from '@/contexts/tenant-context'

const mockQueryFn = vi.fn()

vi.mock('../api/hooks', () => ({
  useBillingRunsTable: vi.fn(() => ({
    queryKey: ['test-tenant', 'billing-runs'],
    queryFn: mockQueryFn,
    tenantSlug: 'test-tenant',
  })),
}))

import { BillingRunsPage } from './index'

function makeQueryClient() {
  return new QueryClient({
    defaultOptions: { queries: { retry: false, staleTime: Infinity } },
  })
}

function Wrapper({ children }: { children: React.ReactNode }) {
  const qc = makeQueryClient()
  return (
    <QueryClientProvider client={qc}>
      <AuthProvider>
        <TenantProvider>
          <TooltipProvider>
            <MemoryRouter>{children}</MemoryRouter>
          </TooltipProvider>
        </TenantProvider>
      </AuthProvider>
    </QueryClientProvider>
  )
}

const sampleRuns = [
  {
    id: 'run-001',
    billingPeriod: { start: '2025-01-01T00:00:00.000Z', end: '2025-01-31T23:59:59.000Z' },
    status: 'COMPLETED',
    dunningLevel: 0,
    invoiceCount: 12,
    totalAmountCents: 150000,
    currency: 'GBP',
    createdAt: '2025-02-01T10:00:00.000Z',
  },
  {
    id: 'run-002',
    billingPeriod: { start: '2025-02-01T00:00:00.000Z', end: '2025-02-28T23:59:59.000Z' },
    status: 'FAILED',
    dunningLevel: 1,
    invoiceCount: 5,
    totalAmountCents: 75000,
    currency: 'GBP',
    createdAt: '2025-03-01T10:00:00.000Z',
  },
]

describe('BillingRunsPage - rendering', () => {
  it('renders page heading', async () => {
    mockQueryFn.mockResolvedValue({ items: [] })

    render(
      <Wrapper>
        <BillingRunsPage />
      </Wrapper>,
    )

    expect(screen.getByRole('heading', { name: /billing runs/i })).toBeInTheDocument()
  })

  it('renders column headers', async () => {
    mockQueryFn.mockResolvedValue({ items: [] })

    render(
      <Wrapper>
        <BillingRunsPage />
      </Wrapper>,
    )

    await waitFor(() => {
      expect(screen.getByRole('columnheader', { name: /period/i })).toBeInTheDocument()
      expect(screen.getByRole('columnheader', { name: /status/i })).toBeInTheDocument()
      expect(screen.getByRole('columnheader', { name: /invoices/i })).toBeInTheDocument()
      expect(screen.getByRole('columnheader', { name: /total amount/i })).toBeInTheDocument()
      expect(screen.getByRole('columnheader', { name: /created/i })).toBeInTheDocument()
    })
  })

  it('renders billing run rows with data', async () => {
    mockQueryFn.mockResolvedValue({ items: sampleRuns })

    render(
      <Wrapper>
        <BillingRunsPage />
      </Wrapper>,
    )

    await waitFor(() => {
      expect(screen.getByText('2025-01-01 – 2025-01-31')).toBeInTheDocument()
      expect(screen.getByText('2025-02-01 – 2025-02-28')).toBeInTheDocument()
    })
  })

  it('renders invoice counts', async () => {
    mockQueryFn.mockResolvedValue({ items: sampleRuns })

    render(
      <Wrapper>
        <BillingRunsPage />
      </Wrapper>,
    )

    await waitFor(() => {
      expect(screen.getByText('12')).toBeInTheDocument()
      expect(screen.getByText('5')).toBeInTheDocument()
    })
  })

  it('renders total amount using MoneyDisplay', async () => {
    mockQueryFn.mockResolvedValue({ items: sampleRuns })

    render(
      <Wrapper>
        <BillingRunsPage />
      </Wrapper>,
    )

    await waitFor(() => {
      // 150000 pence = £1,500.00
      expect(screen.getByText('£1,500.00')).toBeInTheDocument()
    })
  })

  it('renders status using StatusBadge', async () => {
    mockQueryFn.mockResolvedValue({ items: sampleRuns })

    render(
      <Wrapper>
        <BillingRunsPage />
      </Wrapper>,
    )

    await waitFor(() => {
      expect(screen.getByText('COMPLETED')).toBeInTheDocument()
      expect(screen.getByText('FAILED')).toBeInTheDocument()
    })
  })
})

describe('BillingRunsPage - navigation', () => {
  it('calls onRowNavigate with the billing run id on row click', async () => {
    mockQueryFn.mockResolvedValue({ items: sampleRuns })

    const onNavigate = vi.fn()

    render(
      <Wrapper>
        <BillingRunsPage onRowNavigate={onNavigate} />
      </Wrapper>,
    )

    await waitFor(() => expect(screen.getByText('2025-01-01 – 2025-01-31')).toBeInTheDocument())
    await userEvent.click(screen.getByText('2025-01-01 – 2025-01-31'))

    expect(onNavigate).toHaveBeenCalledWith('run-001')
  })
})

describe('BillingRunsPage - filters', () => {
  it('renders status filter select', async () => {
    mockQueryFn.mockResolvedValue({ items: [] })

    render(
      <Wrapper>
        <BillingRunsPage />
      </Wrapper>,
    )

    expect(screen.getByRole('combobox', { name: /status/i })).toBeInTheDocument()
  })
})
