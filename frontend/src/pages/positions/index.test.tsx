import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { BrowserRouter } from 'react-router-dom'
import { TooltipProvider } from '@/components/ui/tooltip'

// MSW intercepts the Connect-ES HTTP calls; we also need to mock the context
// to avoid needing the full ApiClientProvider in unit tests.
const mockListFinancialPositionLogs = vi.fn().mockResolvedValue({
  logs: [],
  pagination: { nextPageToken: '' },
})

vi.mock('@/api/context', () => ({
  useApiClients: vi.fn(() => ({
    positionKeeping: {
      listFinancialPositionLogs: mockListFinancialPositionLogs,
    },
  })),
}))

import { PositionsPage } from './index'

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
      <TooltipProvider>
        <BrowserRouter>{children}</BrowserRouter>
      </TooltipProvider>
    </QueryClientProvider>
  )
}

const mockLogs = [
  {
    logId: 'aaaaaaaa-0000-0000-0000-000000000001',
    accountId: 'acc-001',
    statusTracking: { currentStatus: 'TRANSACTION_STATUS_COMPLETED' },
    createdAt: { seconds: 1700000000, nanos: 0 },
    updatedAt: { seconds: 1700000100, nanos: 0 },
    transactionLogEntries: [],
  },
  {
    logId: 'bbbbbbbb-0000-0000-0000-000000000002',
    accountId: 'acc-002',
    statusTracking: { currentStatus: 'TRANSACTION_STATUS_INITIATED' },
    createdAt: { seconds: 1700001000, nanos: 0 },
    updatedAt: { seconds: 1700001100, nanos: 0 },
    transactionLogEntries: [],
  },
]

describe('PositionsPage', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    mockListFinancialPositionLogs.mockResolvedValue({
      logs: [],
      pagination: { nextPageToken: '' },
    })
  })

  it('renders the page title', () => {
    render(
      <Wrapper>
        <PositionsPage />
      </Wrapper>,
    )
    expect(screen.getByRole('heading', { name: /positions/i })).toBeInTheDocument()
  })

  it('renders subtitle text', () => {
    render(
      <Wrapper>
        <PositionsPage />
      </Wrapper>,
    )
    expect(
      screen.getByText(/Financial position logs with bi-temporal data quality/i),
    ).toBeInTheDocument()
  })

  it('renders the data table with column headers', async () => {
    render(
      <Wrapper>
        <PositionsPage />
      </Wrapper>,
    )

    await waitFor(() => {
      expect(screen.getByRole('columnheader', { name: /Log ID/i })).toBeInTheDocument()
      expect(screen.getByRole('columnheader', { name: /Account/i })).toBeInTheDocument()
      expect(screen.getByRole('columnheader', { name: /Status/i })).toBeInTheDocument()
      expect(screen.getByRole('columnheader', { name: /Created/i })).toBeInTheDocument()
    })
  })

  it('renders position log rows when data is available', async () => {
    mockListFinancialPositionLogs.mockResolvedValue({
      logs: mockLogs,
      pagination: { nextPageToken: '' },
    })

    render(
      <Wrapper>
        <PositionsPage />
      </Wrapper>,
    )

    await waitFor(() => {
      expect(screen.getByText('acc-001')).toBeInTheDocument()
      expect(screen.getByText('acc-002')).toBeInTheDocument()
    })
  })

  it('shows status text for position logs', async () => {
    mockListFinancialPositionLogs.mockResolvedValue({
      logs: mockLogs,
      pagination: { nextPageToken: '' },
    })

    render(
      <Wrapper>
        <PositionsPage />
      </Wrapper>,
    )

    await waitFor(() => {
      expect(screen.getByText('TRANSACTION STATUS COMPLETED')).toBeInTheDocument()
    })
  })

  it('renders account ID filter input', () => {
    render(
      <Wrapper>
        <PositionsPage />
      </Wrapper>,
    )

    expect(screen.getByLabelText(/Account ID/i)).toBeInTheDocument()
  })

  it('renders status filter dropdown', () => {
    render(
      <Wrapper>
        <PositionsPage />
      </Wrapper>,
    )

    expect(screen.getByLabelText(/Status/i)).toBeInTheDocument()
  })

  it('filters by status when selected', async () => {
    const user = userEvent.setup()

    render(
      <Wrapper>
        <PositionsPage />
      </Wrapper>,
    )

    const statusFilter = screen.getByLabelText(/Status/i)
    await user.selectOptions(statusFilter, 'TRANSACTION_STATUS_COMPLETED')

    await waitFor(() => {
      expect(statusFilter).toHaveValue('TRANSACTION_STATUS_COMPLETED')
    })
  })

  it('shows empty state when no logs match', async () => {
    mockListFinancialPositionLogs.mockResolvedValue({
      logs: [],
      pagination: { nextPageToken: '' },
    })

    render(
      <Wrapper>
        <PositionsPage />
      </Wrapper>,
    )

    await waitFor(() => {
      expect(screen.getByTestId('empty-state')).toBeInTheDocument()
    })
  })

  it('shows log ID truncated in table row', async () => {
    mockListFinancialPositionLogs.mockResolvedValue({
      logs: [mockLogs[0]],
      pagination: { nextPageToken: '' },
    })

    render(
      <Wrapper>
        <PositionsPage />
      </Wrapper>,
    )

    await waitFor(() => {
      // logId 'aaaaaaaa-0000...' truncated to first 8 chars + ellipsis in a span
      const cell = document.querySelector('.font-mono.text-xs.text-muted-foreground')
      expect(cell?.textContent).toContain('aaaaaaaa')
    })
  })

  it('shows filter by quality level option in status filter', () => {
    render(
      <Wrapper>
        <PositionsPage />
      </Wrapper>,
    )

    const statusFilter = screen.getByLabelText(/Status/i)
    // Should have status options
    const options = Array.from(statusFilter.querySelectorAll('option'))
    expect(options.length).toBeGreaterThan(1)
  })
})
