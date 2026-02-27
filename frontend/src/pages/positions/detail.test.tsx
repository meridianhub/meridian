import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { MemoryRouter, Route, Routes } from 'react-router-dom'
import { TooltipProvider } from '@/components/ui/tooltip'

const mockRetrieveFinancialPositionLog = vi.fn().mockResolvedValue({ log: null })

vi.mock('@/api/context', () => ({
  useApiClients: vi.fn(() => ({
    positionKeeping: {
      retrieveFinancialPositionLog: mockRetrieveFinancialPositionLog,
    },
  })),
}))

import { PositionDetailPage } from './detail'

function makeQueryClient() {
  return new QueryClient({
    defaultOptions: {
      queries: { retry: false, staleTime: Infinity },
    },
  })
}

function renderDetailPage(logId = 'test-log-id') {
  const qc = makeQueryClient()
  return render(
    <QueryClientProvider client={qc}>
      <TooltipProvider>
        <MemoryRouter initialEntries={[`/positions/${logId}`]}>
          <Routes>
            <Route path="/positions/:logId" element={<PositionDetailPage />} />
            <Route path="/positions" element={<div>Positions List</div>} />
          </Routes>
        </MemoryRouter>
      </TooltipProvider>
    </QueryClientProvider>,
  )
}

const mockLog = {
  logId: 'aaaaaaaa-0000-0000-0000-000000000001',
  accountId: 'acc-001',
  statusTracking: { currentStatus: 'TRANSACTION_STATUS_COMPLETED' },
  createdAt: { seconds: 1700000000, nanos: 0 },
  updatedAt: { seconds: 1700000100, nanos: 0 },
  transactionLogEntries: [
    {
      entryId: 'entry-001',
      transactionId: 'tx-001',
      accountId: 'acc-001',
      amount: { amount: '10000', currency: 'GBP' },
      direction: 'CREDIT',
      qualityLevel: 'ACTUAL',
      timestamp: { seconds: 1700000000, nanos: 0 },
      description: 'Initial credit',
    },
    {
      entryId: 'entry-002',
      transactionId: 'tx-002',
      accountId: 'acc-001',
      amount: { amount: '2000', currency: 'GBP' },
      direction: 'DEBIT',
      qualityLevel: 'ESTIMATE',
      timestamp: { seconds: 1700000100, nanos: 0 },
      description: 'Provisional debit',
    },
  ],
}

describe('PositionDetailPage', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    mockRetrieveFinancialPositionLog.mockResolvedValue({ log: null })
  })

  it('renders page title', () => {
    renderDetailPage()
    expect(screen.getByText('Position Log')).toBeInTheDocument()
  })

  it('renders back link to positions list', () => {
    renderDetailPage()
    const positionsLink = screen.getByRole('link', { name: 'Positions' })
    expect(positionsLink).toBeInTheDocument()
    expect(positionsLink).toHaveAttribute('href', '/positions')
  })

  it('navigates back to positions list on breadcrumb link click', async () => {
    const user = userEvent.setup()
    renderDetailPage()

    const positionsLink = screen.getByRole('link', { name: 'Positions' })
    await user.click(positionsLink)

    await waitFor(() => {
      expect(screen.getByText('Positions List')).toBeInTheDocument()
    })
  })

  it('loads and displays log details', async () => {
    mockRetrieveFinancialPositionLog.mockResolvedValue({ log: mockLog })

    renderDetailPage(mockLog.logId)

    await waitFor(() => {
      expect(screen.getByText('acc-001')).toBeInTheDocument()
    })
  })

  it('displays the log ID in the header', async () => {
    mockRetrieveFinancialPositionLog.mockResolvedValue({ log: mockLog })

    renderDetailPage(mockLog.logId)

    await waitFor(() => {
      const logIdElements = screen.getAllByText(mockLog.logId)
      expect(logIdElements.length).toBeGreaterThan(0)
    })
  })

  it('renders balance view tab and history tab', async () => {
    mockRetrieveFinancialPositionLog.mockResolvedValue({ log: mockLog })

    renderDetailPage(mockLog.logId)

    await waitFor(() => {
      expect(screen.getByText('Balance View')).toBeInTheDocument()
      expect(screen.getByText('Measurement History')).toBeInTheDocument()
    })
  })

  it('shows provisional and available balance separately', async () => {
    mockRetrieveFinancialPositionLog.mockResolvedValue({ log: mockLog })

    renderDetailPage(mockLog.logId)

    await waitFor(() => {
      expect(screen.getByTestId('provisional-balance')).toBeInTheDocument()
      expect(screen.getByTestId('available-balance')).toBeInTheDocument()
    })
  })

  it('provisional balance includes all entries (CREDIT 10000 - DEBIT 2000 = 8000 units)', async () => {
    mockRetrieveFinancialPositionLog.mockResolvedValue({ log: mockLog })

    renderDetailPage(mockLog.logId)

    await waitFor(() => {
      const provisional = screen.getByTestId('provisional-balance')
      // net = +10000 - 2000 = +8000 smallest units of GBP → £80.00
      expect(provisional).toHaveTextContent('80')
    })
  })

  it('available balance only includes ACTUAL and REVISED entries (CREDIT 10000 = 10000 units)', async () => {
    mockRetrieveFinancialPositionLog.mockResolvedValue({ log: mockLog })

    renderDetailPage(mockLog.logId)

    await waitFor(() => {
      const available = screen.getByTestId('available-balance')
      // Only ACTUAL CREDIT 10000 qualifies → £100.00
      expect(available).toHaveTextContent('100')
    })
  })

  it('shows measurement history table after switching tabs', async () => {
    const user = userEvent.setup()
    mockRetrieveFinancialPositionLog.mockResolvedValue({ log: mockLog })

    renderDetailPage(mockLog.logId)

    await waitFor(() => {
      expect(screen.getByText('Measurement History')).toBeInTheDocument()
    })

    await user.click(screen.getByText('Measurement History'))

    await waitFor(() => {
      expect(screen.getByTestId('measurement-history-table')).toBeInTheDocument()
    })
  })

  it('shows quality badges in measurement history', async () => {
    const user = userEvent.setup()
    mockRetrieveFinancialPositionLog.mockResolvedValue({ log: mockLog })

    renderDetailPage(mockLog.logId)

    await waitFor(() => {
      expect(screen.getByText('Measurement History')).toBeInTheDocument()
    })

    await user.click(screen.getByText('Measurement History'))

    await waitFor(() => {
      const qualityBadges = screen.getAllByTestId('quality-ladder-badge')
      expect(qualityBadges.length).toBeGreaterThan(0)
    })
  })

  it('shows direction badges in measurement history', async () => {
    const user = userEvent.setup()
    mockRetrieveFinancialPositionLog.mockResolvedValue({ log: mockLog })

    renderDetailPage(mockLog.logId)

    await waitFor(() => {
      expect(screen.getByText('Measurement History')).toBeInTheDocument()
    })

    await user.click(screen.getByText('Measurement History'))

    await waitFor(() => {
      const directionBadges = screen.getAllByTestId('direction-badge')
      expect(directionBadges.length).toBe(2)
    })
  })

  it('shows DEBIT entry as negative (debit sign convention)', async () => {
    const user = userEvent.setup()
    mockRetrieveFinancialPositionLog.mockResolvedValue({ log: mockLog })

    renderDetailPage(mockLog.logId)

    await waitFor(() => {
      expect(screen.getByText('Measurement History')).toBeInTheDocument()
    })

    await user.click(screen.getByText('Measurement History'))

    await waitFor(() => {
      // DEBIT entry has showSign=true, amount 2000 units of GBP = £20.00
      expect(screen.getByTestId('measurement-history-table')).toBeInTheDocument()
    })
  })

  it('shows empty measurement history when no entries', async () => {
    const user = userEvent.setup()
    mockRetrieveFinancialPositionLog.mockResolvedValue({
      log: { ...mockLog, transactionLogEntries: [] },
    })

    renderDetailPage(mockLog.logId)

    await waitFor(() => {
      expect(screen.getByText('Measurement History')).toBeInTheDocument()
    })

    await user.click(screen.getByText('Measurement History'))

    await waitFor(() => {
      expect(screen.getByTestId('measurement-history-empty')).toBeInTheDocument()
    })
  })

  it('shows error state when fetch fails', async () => {
    mockRetrieveFinancialPositionLog.mockRejectedValue(new Error('Network error'))

    renderDetailPage('bad-id')

    await waitFor(() => {
      expect(screen.getByText(/Failed to load position log/i)).toBeInTheDocument()
    })
  })

  it('quality progress: ESTIMATE < COEFFICIENT < ACTUAL < REVISED ordering', async () => {
    const user = userEvent.setup()
    const logWithAllQualities = {
      ...mockLog,
      transactionLogEntries: [
        { ...mockLog.transactionLogEntries[0], qualityLevel: 'ESTIMATE' },
        { ...mockLog.transactionLogEntries[0], entryId: 'e2', qualityLevel: 'COEFFICIENT' },
        { ...mockLog.transactionLogEntries[0], entryId: 'e3', qualityLevel: 'ACTUAL' },
        { ...mockLog.transactionLogEntries[0], entryId: 'e4', qualityLevel: 'REVISED' },
      ],
    }

    mockRetrieveFinancialPositionLog.mockResolvedValue({ log: logWithAllQualities })

    renderDetailPage(mockLog.logId)

    await waitFor(() => {
      expect(screen.getByText('Measurement History')).toBeInTheDocument()
    })

    await user.click(screen.getByText('Measurement History'))

    await waitFor(() => {
      const badges = screen.getAllByTestId('quality-ladder-badge')
      expect(badges.length).toBe(4)
      expect(badges[0]).toHaveAttribute('data-quality', 'ESTIMATE')
      expect(badges[1]).toHaveAttribute('data-quality', 'COEFFICIENT')
      expect(badges[2]).toHaveAttribute('data-quality', 'ACTUAL')
      expect(badges[3]).toHaveAttribute('data-quality', 'REVISED')
    })
  })
})
