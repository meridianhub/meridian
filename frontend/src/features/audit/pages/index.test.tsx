import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { TooltipProvider } from '@/components/ui/tooltip'

const mockListAuditEntries = vi.hoisted(() =>
  vi.fn().mockResolvedValue({ entries: [], nextPageToken: '' }),
)

vi.mock('@/api/context', () => ({
  useApiClients: vi.fn(() => ({
    audit: {
      listAuditEntries: mockListAuditEntries,
    },
  })),
}))

import { AuditLogPage } from './index'

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
      <TooltipProvider>{children}</TooltipProvider>
    </QueryClientProvider>
  )
}

const mockEntry = {
  entryId: 'entry-1',
  timestamp: { seconds: 1707000000n, nanos: 0 },
  tableName: 'current_account',
  operation: 1, // INSERT enum
  recordId: 'acc-123',
  changedBy: 'user@example.com',
  oldValues: null,
  newValues: { fields: { id: { stringValue: 'acc-123' }, name: { stringValue: 'Test Account' } } },
}

const mockUpdateEntry = {
  entryId: 'entry-2',
  timestamp: { seconds: 1707000001n, nanos: 0 },
  tableName: 'current_account',
  operation: 2, // UPDATE enum
  recordId: 'acc-123',
  changedBy: 'admin@example.com',
  oldValues: { fields: { name: { stringValue: 'Test Account' } } },
  newValues: { fields: { name: { stringValue: 'Updated Account' } } },
}

const mockDeleteEntry = {
  entryId: 'entry-3',
  timestamp: { seconds: 1707000002n, nanos: 0 },
  tableName: 'party',
  operation: 3, // DELETE enum
  recordId: 'party-456',
  changedBy: 'admin@example.com',
  oldValues: { fields: { id: { stringValue: 'party-456' } } },
  newValues: null,
}

describe('AuditLogPage', () => {
  beforeEach(() => {
    vi.clearAllMocks()
  })

  describe('rendering', () => {
    it('renders page title and description', () => {
      mockListAuditEntries.mockResolvedValue({ entries: [] })

      render(
        <Wrapper>
          <AuditLogPage />
        </Wrapper>,
      )

      expect(screen.getByRole('heading', { name: /Audit Log/i })).toBeInTheDocument()
      expect(
        screen.getByText(/Browse and review all audit trail entries/i),
      ).toBeInTheDocument()
    })

    it('renders DataTable with all columns', async () => {
      mockListAuditEntries.mockResolvedValue({ entries: [mockEntry] })

      render(
        <Wrapper>
          <AuditLogPage />
        </Wrapper>,
      )

      await waitFor(() => {
        expect(screen.getByRole('columnheader', { name: /Timestamp/i })).toBeInTheDocument()
        expect(screen.getByRole('columnheader', { name: /Table/i })).toBeInTheDocument()
        expect(screen.getByRole('columnheader', { name: /Operation/i })).toBeInTheDocument()
        expect(screen.getByRole('columnheader', { name: /Record ID/i })).toBeInTheDocument()
        expect(screen.getByRole('columnheader', { name: /Changed By/i })).toBeInTheDocument()
      })
    })
  })

  describe('audit log list', () => {
    it('displays audit entries in table', async () => {
      mockListAuditEntries.mockResolvedValue({ entries: [mockEntry, mockUpdateEntry] })

      render(
        <Wrapper>
          <AuditLogPage />
        </Wrapper>,
      )

      await waitFor(() => {
        expect(screen.getAllByText('acc-123').length).toBeGreaterThan(0)
        expect(screen.getAllByText('user@example.com').length).toBeGreaterThan(0)
      })
    })

    it('renders operation badges with correct styling', async () => {
      mockListAuditEntries.mockResolvedValue({
        entries: [mockEntry, mockUpdateEntry, mockDeleteEntry],
      })

      render(
        <Wrapper>
          <AuditLogPage />
        </Wrapper>,
      )

      await waitFor(() => {
        const insertBadge = screen.getAllByText('INSERT')[0]
        expect(insertBadge).toHaveClass('bg-green-100')

        const updateBadge = screen.getAllByText('UPDATE')[0]
        expect(updateBadge).toHaveClass('bg-blue-100')

        const deleteBadge = screen.getAllByText('DELETE')[0]
        expect(deleteBadge).toHaveClass('bg-red-100')
      })
    })

    it('shows empty state when no entries', async () => {
      mockListAuditEntries.mockResolvedValue({ entries: [] })

      render(
        <Wrapper>
          <AuditLogPage />
        </Wrapper>,
      )

      await waitFor(() => {
        expect(screen.getByTestId('empty-state')).toBeInTheDocument()
      })
    })
  })

  describe('row click and detail panel', () => {
    it('opens detail panel when row is clicked', async () => {
      const user = userEvent.setup()
      mockListAuditEntries.mockResolvedValue({ entries: [mockEntry] })

      render(
        <Wrapper>
          <AuditLogPage />
        </Wrapper>,
      )

      await waitFor(() => {
        expect(screen.getAllByText('acc-123').length).toBeGreaterThan(0)
      })

      const row = screen.getAllByText('acc-123')[0].closest('tr')!
      await user.click(row)

      expect(screen.getByText('Audit Entry Details')).toBeInTheDocument()
    })

    it('closes detail panel when backdrop is clicked', async () => {
      const user = userEvent.setup()
      mockListAuditEntries.mockResolvedValue({ entries: [mockEntry] })

      render(
        <Wrapper>
          <AuditLogPage />
        </Wrapper>,
      )

      await waitFor(() => {
        expect(screen.getAllByText('acc-123').length).toBeGreaterThan(0)
      })

      const row = screen.getAllByText('acc-123')[0].closest('tr')!
      await user.click(row)

      const backdrop = screen.getByTestId('detail-panel-backdrop')
      await user.click(backdrop)

      expect(screen.queryByText('Audit Entry Details')).not.toBeInTheDocument()
    })
  })

  describe('error handling', () => {
    it('shows error state on API failure', async () => {
      mockListAuditEntries.mockRejectedValue(new Error('Network error'))

      render(
        <Wrapper>
          <AuditLogPage />
        </Wrapper>,
      )

      const retryButton = await screen.findByRole('button', { name: /Retry/i })
      expect(retryButton).toBeInTheDocument()
    })
  })
})
