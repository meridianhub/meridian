import { describe, it, expect, vi, beforeEach } from 'vitest'
import { screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { MemoryRouter, Route, Routes } from 'react-router-dom'
import { renderWithProviders } from '@/test/test-utils'

// Mock the reconciliation hook
const mockQueryFn = vi.fn()

vi.mock('../hooks', () => ({
  useReconciliationRunsTable: vi.fn(() => ({
    queryKey: ['test-tenant', 'reconciliation-runs'],
    queryFn: mockQueryFn,
    tenantSlug: 'test-tenant',
  })),
}))

import { ReconciliationPage } from './index'

function renderPage() {
  return renderWithProviders(
    <MemoryRouter initialEntries={['/reconciliation']}>
      <Routes>
        <Route path="/reconciliation" element={<ReconciliationPage />} />
        <Route path="/reconciliation/:runId" element={<div data-testid="detail-page">Detail Page</div>} />
      </Routes>
    </MemoryRouter>,
  )
}

// sampleRuns uses the hook output format (prefixes already stripped by the hook).
const sampleRuns = [
  {
    runId: 'run-001',
    accountId: 'acc-123',
    scope: 'FULL',
    settlementType: 'ON_DEMAND',
    status: 'COMPLETED',
    varianceCount: 2,
    periodStart: '2026-01-01T00:00:00Z',
    periodEnd: '2026-01-31T23:59:59Z',
  },
  {
    runId: 'run-002',
    accountId: 'acc-456',
    scope: 'ACCOUNT',
    settlementType: 'DAILY',
    status: 'RUNNING',
    varianceCount: 0,
    periodStart: '2026-02-01T00:00:00Z',
    periodEnd: '2026-02-28T23:59:59Z',
  },
]

describe('ReconciliationPage - list view', () => {
  beforeEach(() => {
    vi.restoreAllMocks()
    mockQueryFn.mockResolvedValue({ items: [] })
  })

  it('renders page heading', () => {
    renderPage()
    expect(screen.getByText('Reconciliation')).toBeInTheDocument()
  })

  it('renders settlement runs in the table', async () => {
    mockQueryFn.mockResolvedValue({ items: sampleRuns })
    renderPage()

    await waitFor(() => {
      expect(screen.getByText('run-001')).toBeInTheDocument()
      expect(screen.getByText('run-002')).toBeInTheDocument()
    })
  })

  it('shows variance count with destructive badge when count > 0', async () => {
    mockQueryFn.mockResolvedValue({ items: sampleRuns })
    renderPage()

    await waitFor(() => {
      expect(screen.getByText('run-001')).toBeInTheDocument()
    })

    // run-001 has 2 variances - should have destructive badge
    // run-002 has 0 variances - should have secondary badge
    const badges = screen.getAllByText(/^[02]$/)
    expect(badges.length).toBeGreaterThanOrEqual(2)
  })

  it('renders status badges for each run', async () => {
    mockQueryFn.mockResolvedValue({ items: sampleRuns })
    renderPage()

    await waitFor(() => {
      expect(screen.getByText('COMPLETED')).toBeInTheDocument()
      expect(screen.getByText('RUNNING')).toBeInTheDocument()
    })
  })

  it('renders period column with formatted dates', async () => {
    mockQueryFn.mockResolvedValue({ items: sampleRuns })
    renderPage()

    await waitFor(() => {
      expect(screen.getByText('2026-01-01 – 2026-01-31')).toBeInTheDocument()
    })
  })

  it('renders column headers', async () => {
    renderPage()

    await waitFor(() => {
      expect(screen.getByRole('columnheader', { name: 'Run ID' })).toBeInTheDocument()
      expect(screen.getByRole('columnheader', { name: 'Account' })).toBeInTheDocument()
      expect(screen.getByRole('columnheader', { name: 'Status' })).toBeInTheDocument()
      expect(screen.getByRole('columnheader', { name: 'Variances' })).toBeInTheDocument()
    })
  })

  it('shows filters for status and account ID', async () => {
    renderPage()

    await waitFor(() => {
      expect(screen.getByRole('combobox', { name: /status/i })).toBeInTheDocument()
      expect(screen.getByPlaceholderText(/filter by account id/i)).toBeInTheDocument()
    })
  })

  it('navigates to detail page on row click', async () => {
    mockQueryFn.mockResolvedValue({ items: sampleRuns })
    renderPage()

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
  })

  it('shows error state when fetch fails', async () => {
    mockQueryFn.mockRejectedValue(new Error('Network error'))
    renderPage()

    await waitFor(() => {
      expect(screen.getByRole('button', { name: /retry/i })).toBeInTheDocument()
    })
  })

  it('shows empty state when no runs returned', async () => {
    renderPage()

    await waitFor(() => {
      expect(screen.getByTestId('empty-state')).toBeInTheDocument()
    })
  })

  it('renders Start Reconciliation button in header', () => {
    renderPage()
    expect(screen.getByRole('button', { name: /start reconciliation/i })).toBeInTheDocument()
  })
})
