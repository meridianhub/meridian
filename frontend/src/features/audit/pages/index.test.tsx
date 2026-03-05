import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { AuditLogPage, type AuditLogEntry } from './index'

vi.mock('@/hooks/use-authenticated-fetch', () => ({
  useAuthenticatedFetch: () => fetch,
}))

function makeQueryClient() {
  return new QueryClient({
    defaultOptions: {
      queries: { retry: false, staleTime: Infinity },
    },
  })
}

function Wrapper({ children }: { children: React.ReactNode }) {
  const qc = makeQueryClient()
  return <QueryClientProvider client={qc}>{children}</QueryClientProvider>
}

const mockAuditEntry: AuditLogEntry = {
  entryId: 'entry-1',
  timestamp: { seconds: 1707000000n, nanos: 0 },
  tableName: 'current_account',
  operation: 'INSERT',
  recordId: 'acc-123',
  changedBy: 'user@example.com',
  oldValues: null,
  newValues: { id: 'acc-123', name: 'Test Account' },
}

const mockAuditEntryUpdate: AuditLogEntry = {
  entryId: 'entry-2',
  timestamp: { seconds: 1707000001n, nanos: 0 },
  tableName: 'current_account',
  operation: 'UPDATE',
  recordId: 'acc-123',
  changedBy: 'admin@example.com',
  oldValues: { name: 'Test Account' },
  newValues: { name: 'Updated Account' },
}

const mockAuditEntryDelete: AuditLogEntry = {
  entryId: 'entry-3',
  timestamp: { seconds: 1707000002n, nanos: 0 },
  tableName: 'party',
  operation: 'DELETE',
  recordId: 'party-456',
  changedBy: 'admin@example.com',
  oldValues: { id: 'party-456', name: 'Old Party' },
  newValues: null,
}

describe('AuditLogPage', () => {
  beforeEach(() => {
    // Mock fetch
    global.fetch = vi.fn()
  })

  describe('rendering', () => {
    it('renders page title and description', () => {
      ;(global.fetch as vi.MockedFunction<typeof fetch>).mockResolvedValue({
        ok: true,
        json: async () => ({ entries: [] }),
      } as Response)

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
      ;(global.fetch as vi.MockedFunction<typeof fetch>).mockResolvedValue({
        ok: true,
        json: async () => ({ entries: [mockAuditEntry] }),
      } as Response)

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

    it('renders filter controls', async () => {
      ;(global.fetch as vi.MockedFunction<typeof fetch>).mockResolvedValue({
        ok: true,
        json: async () => ({ entries: [] }),
      })

      render(
        <Wrapper>
          <AuditLogPage />
        </Wrapper>,
      )

      await waitFor(() => {
        // Filter inputs/selects should be present (they render with sr-only labels)
        const filterInputs = screen.getAllByRole('combobox')
        expect(filterInputs.length).toBeGreaterThan(0)
      })
    })
  })

  describe('audit log list', () => {
    it('displays audit entries in table', async () => {
      ;(global.fetch as vi.MockedFunction<typeof fetch>).mockResolvedValue({
        ok: true,
        json: async () => ({ entries: [mockAuditEntry, mockAuditEntryUpdate] }),
      })

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
      ;(global.fetch as vi.MockedFunction<typeof fetch>).mockResolvedValue({
        ok: true,
        json: async () => ({ entries: [mockAuditEntry, mockAuditEntryUpdate, mockAuditEntryDelete] }),
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
      ;(global.fetch as vi.MockedFunction<typeof fetch>).mockResolvedValue({
        ok: true,
        json: async () => ({ entries: [] }),
      })

      render(
        <Wrapper>
          <AuditLogPage />
        </Wrapper>,
      )

      await waitFor(() => {
        expect(screen.getByTestId('empty-state')).toBeInTheDocument()
      })
    })

    it('shows loading skeleton while fetching', () => {
      ;(global.fetch as vi.MockedFunction<typeof fetch>).mockImplementation(() => new Promise(() => {}))

      render(
        <Wrapper>
          <AuditLogPage />
        </Wrapper>,
      )

      expect(screen.getAllByTestId('skeleton-row').length).toBeGreaterThan(0)
    })
  })

  describe('filtering', () => {
    it('filters by table name', async () => {
      const user = userEvent.setup()
      ;(global.fetch as vi.MockedFunction<typeof fetch>).mockResolvedValue({
        ok: true,
        json: async () => ({ entries: [mockAuditEntry] }),
      })

      render(
        <Wrapper>
          <AuditLogPage />
        </Wrapper>,
      )

      const tableFilters = screen.getAllByRole('combobox')
      const tableFilter = tableFilters[0]

      await user.selectOptions(tableFilter, 'current_account')

      await waitFor(() => {
        const calls = (global.fetch as vi.MockedFunction<typeof fetch>).mock.calls
        const lastCall = calls[calls.length - 1]
        const body = JSON.parse(lastCall[1]?.body as string)
        expect(body.tableName).toBe('current_account')
      })
    })

    it('filters by operation', async () => {
      const user = userEvent.setup()
      ;(global.fetch as vi.MockedFunction<typeof fetch>).mockResolvedValue({
        ok: true,
        json: async () => ({ entries: [mockAuditEntryUpdate] }),
      })

      render(
        <Wrapper>
          <AuditLogPage />
        </Wrapper>,
      )

      const operationFilters = screen.getAllByRole('combobox')
      const operationFilter = operationFilters[1]

      await user.selectOptions(operationFilter, 'UPDATE')

      await waitFor(() => {
        const calls = (global.fetch as vi.MockedFunction<typeof fetch>).mock.calls
        const lastCall = calls[calls.length - 1]
        const body = JSON.parse(lastCall[1]?.body as string)
        expect(body.operation).toBe('UPDATE')
      })
    })

    it('filters by user', async () => {
      const user = userEvent.setup()
      ;(global.fetch as vi.MockedFunction<typeof fetch>).mockResolvedValue({
        ok: true,
        json: async () => ({ entries: [mockAuditEntry] }),
      })

      render(
        <Wrapper>
          <AuditLogPage />
        </Wrapper>,
      )

      const inputs = screen.getAllByRole('textbox')
      const userInput = inputs[0]

      await user.type(userInput, 'user@example.com')

      await waitFor(() => {
        const calls = (global.fetch as vi.MockedFunction<typeof fetch>).mock.calls
        const lastCall = calls[calls.length - 1]
        const body = JSON.parse(lastCall[1]?.body as string)
        expect(body.changedBy).toBe('user@example.com')
      })
    })

    it('resets pagination when filters change', async () => {
      const user = userEvent.setup()
      ;(global.fetch as vi.MockedFunction<typeof fetch>).mockResolvedValue({
        ok: true,
        json: async () => ({ entries: [mockAuditEntry], nextPageToken: 'token-1' }),
      })

      render(
        <Wrapper>
          <AuditLogPage />
        </Wrapper>,
      )

      // Navigate to next page
      const nextButton = await screen.findByRole('button', { name: /Next/i })
      await user.click(nextButton)

      // Change filter
      const tableFilters = screen.getAllByRole('combobox')
      const tableFilter = tableFilters[0]
      await user.selectOptions(tableFilter, 'payment_order')

      // Verify pagination token is reset
      await waitFor(() => {
        const calls = (global.fetch as vi.MockedFunction<typeof fetch>).mock.calls
        const lastCall = calls[calls.length - 1]
        const body = JSON.parse(lastCall[1]?.body as string)
        expect(body.pageToken).toBeUndefined()
      })
    })
  })

  describe('row click and detail panel', () => {
    it('opens detail panel when row is clicked', async () => {
      const user = userEvent.setup()
      ;(global.fetch as vi.MockedFunction<typeof fetch>).mockResolvedValue({
        ok: true,
        json: async () => ({ entries: [mockAuditEntry] }),
      })

      render(
        <Wrapper>
          <AuditLogPage />
        </Wrapper>,
      )

      const rows = await screen.findAllByText('about 2 years ago')
      await user.click(rows[0].closest('tr')!)

      expect(screen.getByText('Audit Entry Details')).toBeInTheDocument()
    })

    it('displays entry metadata in detail panel', async () => {
      const user = userEvent.setup()
      ;(global.fetch as vi.MockedFunction<typeof fetch>).mockResolvedValue({
        ok: true,
        json: async () => ({ entries: [mockAuditEntry] }),
      })

      render(
        <Wrapper>
          <AuditLogPage />
        </Wrapper>,
      )

      // Click on the timestamp row element
      const rows = await screen.findAllByText('about 2 years ago')
      await user.click(rows[0].closest('tr')!)

      expect(screen.getByText('Audit Entry Details')).toBeInTheDocument()
      expect(screen.getAllByText('current_account')[1]).toBeInTheDocument()
    })

    it('displays JSON diff in detail panel for INSERT operation', async () => {
      const user = userEvent.setup()
      ;(global.fetch as vi.MockedFunction<typeof fetch>).mockResolvedValue({
        ok: true,
        json: async () => ({ entries: [mockAuditEntry] }),
      })

      render(
        <Wrapper>
          <AuditLogPage />
        </Wrapper>,
      )

      const rows = await screen.findAllByText('about 2 years ago')
      await user.click(rows[0].closest('tr')!)

      await waitFor(() => {
        expect(screen.getByTestId('diff-inserted')).toBeInTheDocument()
      })
    })

    it('displays JSON diff for UPDATE operation (before/after)', async () => {
      const user = userEvent.setup()
      ;(global.fetch as vi.MockedFunction<typeof fetch>).mockResolvedValue({
        ok: true,
        json: async () => ({ entries: [mockAuditEntryUpdate] }),
      })

      render(
        <Wrapper>
          <AuditLogPage />
        </Wrapper>,
      )

      const rows = await screen.findAllByText('about 2 years ago')
      await user.click(rows[0].closest('tr')!)

      await waitFor(() => {
        expect(screen.getByTestId('diff-before')).toBeInTheDocument()
        expect(screen.getByTestId('diff-after')).toBeInTheDocument()
      })
    })

    it('displays JSON diff for DELETE operation', async () => {
      const user = userEvent.setup()
      ;(global.fetch as vi.MockedFunction<typeof fetch>).mockResolvedValue({
        ok: true,
        json: async () => ({ entries: [mockAuditEntryDelete] }),
      })

      render(
        <Wrapper>
          <AuditLogPage />
        </Wrapper>,
      )

      const rows = await screen.findAllByText('party')
      await user.click(rows[0].closest('tr')!)

      await waitFor(() => {
        expect(screen.getByTestId('diff-deleted')).toBeInTheDocument()
      })
    })

    it('closes detail panel when backdrop is clicked', async () => {
      const user = userEvent.setup()
      ;(global.fetch as vi.MockedFunction<typeof fetch>).mockResolvedValue({
        ok: true,
        json: async () => ({ entries: [mockAuditEntry] }),
      })

      render(
        <Wrapper>
          <AuditLogPage />
        </Wrapper>,
      )

      const rows = await screen.findAllByText('about 2 years ago')
      await user.click(rows[0].closest('tr')!)

      const backdrop = screen.getByTestId('detail-panel-backdrop')
      await user.click(backdrop)

      expect(screen.queryByText('Audit Entry Details')).not.toBeInTheDocument()
    })

    it('closes detail panel when close button is clicked', async () => {
      const user = userEvent.setup()
      ;(global.fetch as vi.MockedFunction<typeof fetch>).mockResolvedValue({
        ok: true,
        json: async () => ({ entries: [mockAuditEntry] }),
      })

      render(
        <Wrapper>
          <AuditLogPage />
        </Wrapper>,
      )

      const rows = await screen.findAllByText('about 2 years ago')
      await user.click(rows[0].closest('tr')!)

      const closeButton = screen.getByRole('button', { name: /✕/i })
      await user.click(closeButton)

      expect(screen.queryByText('Audit Entry Details')).not.toBeInTheDocument()
    })
  })

  describe('error handling', () => {
    it('shows loading state on network error', async () => {
      ;(global.fetch as vi.MockedFunction<typeof fetch>).mockRejectedValue(new Error('Network error'))

      render(
        <Wrapper>
          <AuditLogPage />
        </Wrapper>,
      )

      const retryButton = await screen.findByRole('button', { name: /Retry/i })
      expect(retryButton).toBeInTheDocument()
    })

    it('handles stub service (501/503 responses)', async () => {
      ;(global.fetch as vi.MockedFunction<typeof fetch>).mockResolvedValue({
        ok: false,
        status: 501,
      })

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
})
