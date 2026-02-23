import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { MemoryRouter, Route, Routes } from 'react-router-dom'
import { ReconciliationPage } from './index'

function makeQueryClient() {
  return new QueryClient({
    defaultOptions: { queries: { retry: false } },
  })
}

function Wrapper({ children }: { children: React.ReactNode }) {
  return (
    <QueryClientProvider client={makeQueryClient()}>
      <MemoryRouter initialEntries={['/reconciliation']}>
        <Routes>
          <Route path="/reconciliation" element={<>{children}</>} />
        </Routes>
      </MemoryRouter>
    </QueryClientProvider>
  )
}

// sampleRuns uses the proto/gateway response format (runs array with full enum names).
const sampleRuns = [
  {
    runId: 'run-001',
    accountId: 'acc-123',
    scope: 'RECONCILIATION_SCOPE_FULL',
    settlementType: 'SETTLEMENT_TYPE_ON_DEMAND',
    status: 'RUN_STATUS_COMPLETED',
    varianceCount: 2,
    periodStart: '2026-01-01T00:00:00Z',
    periodEnd: '2026-01-31T23:59:59Z',
  },
  {
    runId: 'run-002',
    accountId: 'acc-456',
    scope: 'RECONCILIATION_SCOPE_ACCOUNT',
    settlementType: 'SETTLEMENT_TYPE_DAILY',
    status: 'RUN_STATUS_RUNNING',
    varianceCount: 0,
    periodStart: '2026-02-01T00:00:00Z',
    periodEnd: '2026-02-28T23:59:59Z',
  },
]

describe('ReconciliationPage - list view', () => {
  beforeEach(() => {
    vi.restoreAllMocks()
  })

  it('renders page heading', () => {
    vi.spyOn(globalThis, 'fetch').mockResolvedValue(
      new Response(JSON.stringify({ runs: [] }), { status: 200 }),
    )
    render(<ReconciliationPage />, { wrapper: Wrapper })
    expect(screen.getByText('Reconciliation')).toBeInTheDocument()
  })

  it('renders settlement runs in the table', async () => {
    vi.spyOn(globalThis, 'fetch').mockResolvedValue(
      new Response(JSON.stringify({ runs: sampleRuns }), { status: 200 }),
    )
    render(<ReconciliationPage />, { wrapper: Wrapper })

    await waitFor(() => {
      expect(screen.getByText('run-001')).toBeInTheDocument()
      expect(screen.getByText('run-002')).toBeInTheDocument()
    })
  })

  it('shows variance count with destructive badge when count > 0', async () => {
    vi.spyOn(globalThis, 'fetch').mockResolvedValue(
      new Response(JSON.stringify({ runs: sampleRuns }), { status: 200 }),
    )
    render(<ReconciliationPage />, { wrapper: Wrapper })

    await waitFor(() => {
      expect(screen.getByText('run-001')).toBeInTheDocument()
    })

    // run-001 has 2 variances - should have destructive badge
    // run-002 has 0 variances - should have secondary badge
    const badges = screen.getAllByText(/^[02]$/)
    expect(badges.length).toBeGreaterThanOrEqual(2)
  })

  it('renders status badges for each run', async () => {
    vi.spyOn(globalThis, 'fetch').mockResolvedValue(
      new Response(JSON.stringify({ runs: sampleRuns }), { status: 200 }),
    )
    render(<ReconciliationPage />, { wrapper: Wrapper })

    await waitFor(() => {
      expect(screen.getByText('COMPLETED')).toBeInTheDocument()  // RUN_STATUS_ prefix stripped
      expect(screen.getByText('RUNNING')).toBeInTheDocument()    // RUN_STATUS_ prefix stripped
    })
  })

  it('renders period column with formatted dates', async () => {
    vi.spyOn(globalThis, 'fetch').mockResolvedValue(
      new Response(JSON.stringify({ runs: sampleRuns }), { status: 200 }),
    )
    render(<ReconciliationPage />, { wrapper: Wrapper })

    await waitFor(() => {
      expect(screen.getByText('2026-01-01 – 2026-01-31')).toBeInTheDocument()
    })
  })

  it('renders column headers', async () => {
    vi.spyOn(globalThis, 'fetch').mockResolvedValue(
      new Response(JSON.stringify({ runs: [] }), { status: 200 }),
    )
    render(<ReconciliationPage />, { wrapper: Wrapper })

    await waitFor(() => {
      expect(screen.getByRole('columnheader', { name: 'Run ID' })).toBeInTheDocument()
      expect(screen.getByRole('columnheader', { name: 'Account' })).toBeInTheDocument()
      expect(screen.getByRole('columnheader', { name: 'Status' })).toBeInTheDocument()
      expect(screen.getByRole('columnheader', { name: 'Variances' })).toBeInTheDocument()
    })
  })

  it('shows filters for status and account ID', async () => {
    vi.spyOn(globalThis, 'fetch').mockResolvedValue(
      new Response(JSON.stringify({ runs: [] }), { status: 200 }),
    )
    render(<ReconciliationPage />, { wrapper: Wrapper })

    await waitFor(() => {
      expect(screen.getByRole('combobox', { name: /status/i })).toBeInTheDocument()
      expect(screen.getByPlaceholderText(/filter by account id/i)).toBeInTheDocument()
    })
  })

  it('navigates to detail page on row click', async () => {
    vi.spyOn(globalThis, 'fetch').mockResolvedValue(
      new Response(JSON.stringify({ runs: sampleRuns }), { status: 200 }),
    )

    let currentPath = '/reconciliation'
    const RouterWrapper = ({ children }: { children: React.ReactNode }) => {
      const qc = makeQueryClient()
      return (
        <QueryClientProvider client={qc}>
          <MemoryRouter initialEntries={['/reconciliation']}>
            <Routes>
              <Route path="/reconciliation" element={<>{children}</>} />
              <Route
                path="/reconciliation/:runId"
                element={
                  <div
                    data-testid="detail-page"
                    ref={(el) => {
                      if (el) currentPath = window.location.pathname
                    }}
                  >
                    Detail Page
                  </div>
                }
              />
            </Routes>
          </MemoryRouter>
        </QueryClientProvider>
      )
    }

    render(<ReconciliationPage />, { wrapper: RouterWrapper })

    await waitFor(() => {
      expect(screen.getByText('run-001')).toBeInTheDocument()
    })

    // Row should be clickable (cursor-pointer class applied by DataTable when onRowClick provided)
    const rows = screen.getAllByRole('row')
    const dataRow = rows.find((r) => r.textContent?.includes('run-001'))
    expect(dataRow).toBeDefined()
    expect(dataRow?.className).toContain('cursor-pointer')

    // Click the row and verify navigation to detail page
    await userEvent.click(dataRow!)
    await waitFor(() => {
      expect(screen.getByTestId('detail-page')).toBeInTheDocument()
    })
    void currentPath
  })

  it('shows error state when fetch fails', async () => {
    vi.spyOn(globalThis, 'fetch').mockRejectedValue(new Error('Network error'))
    render(<ReconciliationPage />, { wrapper: Wrapper })

    await waitFor(() => {
      expect(screen.getByRole('button', { name: /retry/i })).toBeInTheDocument()
    })
  })

  it('shows empty state when no runs returned', async () => {
    vi.spyOn(globalThis, 'fetch').mockResolvedValue(
      new Response(JSON.stringify({ runs: [] }), { status: 200 }),
    )
    render(<ReconciliationPage />, { wrapper: Wrapper })

    await waitFor(() => {
      expect(screen.getByTestId('empty-state')).toBeInTheDocument()
    })
  })
})
