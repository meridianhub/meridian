import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { screen, waitFor, fireEvent } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { http, HttpResponse } from 'msw'
import { MemoryRouter, Route, Routes } from 'react-router-dom'
import { renderWithProviders } from '@/test/test-utils'
import { server } from '@/test/msw-handlers'
import { InitiateReconciliationDialog } from './initiate-reconciliation-dialog'

/** Set a date input value (type="date" inputs don't respond to userEvent.type). */
function setDateInput(input: HTMLElement, value: string) {
  fireEvent.change(input, { target: { value } })
}

const mockUUID = 'test-uuid-1234-5678-9012-abcdef'

function renderDialog(props: {
  open?: boolean
  onOpenChange?: (open: boolean) => void
  onSuccess?: (runId: string) => void
}) {
  const onOpenChange = props.onOpenChange ?? vi.fn()
  const onSuccess = props.onSuccess ?? vi.fn()
  return renderWithProviders(
    <MemoryRouter initialEntries={['/reconciliation']}>
      <Routes>
        <Route
          path="/reconciliation"
          element={
            <InitiateReconciliationDialog
              open={props.open ?? true}
              onOpenChange={onOpenChange}
              onSuccess={onSuccess}
            />
          }
        />
        <Route path="/reconciliation/:runId" element={<div data-testid="run-detail">Detail</div>} />
      </Routes>
    </MemoryRouter>,
  )
}

describe('InitiateReconciliationDialog', () => {
  beforeEach(() => {
    vi.spyOn(crypto, 'randomUUID').mockReturnValue(
      mockUUID as `${string}-${string}-${string}-${string}-${string}`,
    )
  })

  afterEach(() => {
    vi.restoreAllMocks()
  })

  describe('rendering', () => {
    it('renders dialog when open', () => {
      renderDialog({ open: true })
      expect(screen.getByRole('dialog')).toBeInTheDocument()
      expect(screen.getByRole('heading', { name: 'Start Reconciliation' })).toBeInTheDocument()
    })

    it('does not render dialog content when closed', () => {
      renderDialog({ open: false })
      expect(screen.queryByRole('dialog')).not.toBeInTheDocument()
    })

    it('renders settlement date input', () => {
      renderDialog({ open: true })
      expect(screen.getByLabelText('Settlement Date')).toBeInTheDocument()
    })

    it('renders scope select with correct options', () => {
      renderDialog({ open: true })
      const scopeSelect = screen.getByRole('combobox', { name: /scope/i })
      expect(scopeSelect).toBeInTheDocument()
      expect(screen.getByText('All Accounts')).toBeInTheDocument()
      expect(screen.getByText('Selected Accounts')).toBeInTheDocument()
    })

    it('renders description field', () => {
      renderDialog({ open: true })
      expect(screen.getByLabelText(/description/i)).toBeInTheDocument()
    })

    it('does not render account ID input when scope is ALL_ACCOUNTS', () => {
      renderDialog({ open: true })
      expect(screen.queryByLabelText('Account ID')).not.toBeInTheDocument()
    })

    it('renders Start Reconciliation and Cancel buttons', () => {
      renderDialog({ open: true })
      expect(screen.getByRole('button', { name: /start reconciliation/i })).toBeInTheDocument()
      expect(screen.getByRole('button', { name: /cancel/i })).toBeInTheDocument()
    })
  })

  describe('settlement date validation', () => {
    it('shows error when settlement date is not provided', async () => {
      const user = userEvent.setup()
      renderDialog({ open: true })

      await user.click(screen.getByRole('button', { name: /start reconciliation/i }))

      expect(await screen.findByText('Settlement date is required')).toBeInTheDocument()
    })

    it('shows error when settlement date is in the future', async () => {
      const user = userEvent.setup()
      renderDialog({ open: true })

      // Build a future date in local timezone (YYYY-MM-DD format, same as date inputs)
      const futureDate = new Date()
      futureDate.setDate(futureDate.getDate() + 2)
      const futureDateStr = `${futureDate.getFullYear()}-${String(futureDate.getMonth() + 1).padStart(2, '0')}-${String(futureDate.getDate()).padStart(2, '0')}`

      setDateInput(screen.getByLabelText('Settlement Date'), futureDateStr)
      await user.click(screen.getByRole('button', { name: /start reconciliation/i }))

      expect(
        await screen.findByText('Settlement date cannot be in the future'),
      ).toBeInTheDocument()
    })

    it('accepts today as a valid settlement date', async () => {
      const user = userEvent.setup()
      const onSuccess = vi.fn()

      server.use(
        http.post('*/InitiateAccountReconciliation', () => {
          return HttpResponse.json({ run: { runId: 'run-abc-123' } })
        }),
      )

      renderDialog({ open: true, onSuccess })

      const now = new Date()
      const today = `${now.getFullYear()}-${String(now.getMonth() + 1).padStart(2, '0')}-${String(now.getDate()).padStart(2, '0')}`
      setDateInput(screen.getByLabelText('Settlement Date'), today)
      await user.click(screen.getByRole('button', { name: /start reconciliation/i }))

      await waitFor(() => {
        expect(onSuccess).toHaveBeenCalledWith('run-abc-123')
      })
    })
  })

  describe('scope selection and conditional account ID', () => {
    it('shows account ID input when scope changes to SELECTED_ACCOUNTS', async () => {
      const user = userEvent.setup()
      renderDialog({ open: true })

      const scopeSelect = screen.getByRole('combobox', { name: /scope/i })
      await user.selectOptions(scopeSelect, 'SELECTED_ACCOUNTS')

      expect(screen.getByLabelText('Account ID')).toBeInTheDocument()
    })

    it('hides account ID input when scope switches back to ALL_ACCOUNTS', async () => {
      const user = userEvent.setup()
      renderDialog({ open: true })

      const scopeSelect = screen.getByRole('combobox', { name: /scope/i })
      await user.selectOptions(scopeSelect, 'SELECTED_ACCOUNTS')
      expect(screen.getByLabelText('Account ID')).toBeInTheDocument()

      await user.selectOptions(scopeSelect, 'ALL_ACCOUNTS')
      expect(screen.queryByLabelText('Account ID')).not.toBeInTheDocument()
    })

    it('shows validation error when SELECTED_ACCOUNTS scope has no account ID', async () => {
      const user = userEvent.setup()
      renderDialog({ open: true })

      const now = new Date()
      const today = `${now.getFullYear()}-${String(now.getMonth() + 1).padStart(2, '0')}-${String(now.getDate()).padStart(2, '0')}`
      setDateInput(screen.getByLabelText('Settlement Date'), today)

      const scopeSelect = screen.getByRole('combobox', { name: /scope/i })
      await user.selectOptions(scopeSelect, 'SELECTED_ACCOUNTS')

      await user.click(screen.getByRole('button', { name: /start reconciliation/i }))

      expect(
        await screen.findByText('Account ID is required when scope is Selected Accounts'),
      ).toBeInTheDocument()
    })
  })

  describe('idempotency key generation', () => {
    it('generates a unique idempotency key when dialog opens', () => {
      renderDialog({ open: true })
      expect(crypto.randomUUID).toHaveBeenCalled()
    })
  })

  describe('form submission', () => {
    it('calls onSuccess with runId on successful submission', async () => {
      const user = userEvent.setup()
      const onSuccess = vi.fn()

      server.use(
        http.post('*/InitiateAccountReconciliation', () => {
          return HttpResponse.json({ run: { runId: 'run-xyz-456' } })
        }),
      )

      renderDialog({ open: true, onSuccess })

      const now = new Date()
      const today = `${now.getFullYear()}-${String(now.getMonth() + 1).padStart(2, '0')}-${String(now.getDate()).padStart(2, '0')}`
      setDateInput(screen.getByLabelText('Settlement Date'), today)
      await user.click(screen.getByRole('button', { name: /start reconciliation/i }))

      await waitFor(() => {
        expect(onSuccess).toHaveBeenCalledWith('run-xyz-456')
      })
    })

    it('closes dialog after successful submission', async () => {
      const user = userEvent.setup()
      const onOpenChange = vi.fn()

      server.use(
        http.post('*/InitiateAccountReconciliation', () => {
          return HttpResponse.json({ run: { runId: 'run-abc-789' } })
        }),
      )

      renderDialog({ open: true, onOpenChange })

      const now = new Date()
      const today = `${now.getFullYear()}-${String(now.getMonth() + 1).padStart(2, '0')}-${String(now.getDate()).padStart(2, '0')}`
      setDateInput(screen.getByLabelText('Settlement Date'), today)
      await user.click(screen.getByRole('button', { name: /start reconciliation/i }))

      await waitFor(() => {
        expect(onOpenChange).toHaveBeenCalledWith(false)
      })
    })

    it('shows server error message on API failure', async () => {
      const user = userEvent.setup()

      server.use(
        http.post('*/InitiateAccountReconciliation', () => {
          return HttpResponse.json({ code: 2, message: 'Internal error' }, { status: 500 })
        }),
      )

      renderDialog({ open: true })

      const now = new Date()
      const today = `${now.getFullYear()}-${String(now.getMonth() + 1).padStart(2, '0')}-${String(now.getDate()).padStart(2, '0')}`
      setDateInput(screen.getByLabelText('Settlement Date'), today)
      await user.click(screen.getByRole('button', { name: /start reconciliation/i }))

      await waitFor(() => {
        expect(screen.getByRole('alert')).toBeInTheDocument()
      })
    })

    it('submits with account ID when scope is SELECTED_ACCOUNTS', async () => {
      const user = userEvent.setup()
      const onSuccess = vi.fn()

      server.use(
        http.post('*/InitiateAccountReconciliation', () => {
          return HttpResponse.json({ run: { runId: 'run-account-specific' } })
        }),
      )

      renderDialog({ open: true, onSuccess })

      const now = new Date()
      const today = `${now.getFullYear()}-${String(now.getMonth() + 1).padStart(2, '0')}-${String(now.getDate()).padStart(2, '0')}`
      setDateInput(screen.getByLabelText('Settlement Date'), today)
      await user.selectOptions(screen.getByRole('combobox', { name: /scope/i }), 'SELECTED_ACCOUNTS')
      await user.type(screen.getByLabelText('Account ID'), 'acct-001')
      await user.click(screen.getByRole('button', { name: /start reconciliation/i }))

      await waitFor(() => {
        expect(onSuccess).toHaveBeenCalledWith('run-account-specific')
      })
    })
  })

  describe('form reset on close', () => {
    it('resets form fields when dialog is closed and reopened', () => {
      const { rerender } = renderWithProviders(
        <MemoryRouter>
          <Routes>
            <Route
              path="/"
              element={
                <InitiateReconciliationDialog
                  open={true}
                  onOpenChange={vi.fn()}
                  onSuccess={vi.fn()}
                />
              }
            />
          </Routes>
        </MemoryRouter>,
      )

      // Close dialog
      rerender(
        <MemoryRouter>
          <Routes>
            <Route
              path="/"
              element={
                <InitiateReconciliationDialog
                  open={false}
                  onOpenChange={vi.fn()}
                  onSuccess={vi.fn()}
                />
              }
            />
          </Routes>
        </MemoryRouter>,
      )

      // Reopen dialog
      rerender(
        <MemoryRouter>
          <Routes>
            <Route
              path="/"
              element={
                <InitiateReconciliationDialog
                  open={true}
                  onOpenChange={vi.fn()}
                  onSuccess={vi.fn()}
                />
              }
            />
          </Routes>
        </MemoryRouter>,
      )

      expect((screen.getByLabelText('Settlement Date') as HTMLInputElement).value).toBe('')
      expect((screen.getByRole('combobox', { name: /scope/i }) as HTMLSelectElement).value).toBe(
        'ALL_ACCOUNTS',
      )
    })
  })

  describe('cancel button', () => {
    it('calls onOpenChange(false) when cancel is clicked', async () => {
      const user = userEvent.setup()
      const onOpenChange = vi.fn()
      renderDialog({ open: true, onOpenChange })

      await user.click(screen.getByRole('button', { name: /cancel/i }))

      expect(onOpenChange).toHaveBeenCalledWith(false)
    })
  })
})
