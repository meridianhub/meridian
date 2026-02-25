import { describe, it, expect, vi, afterEach } from 'vitest'
import { screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { http, HttpResponse } from 'msw'
import { MemoryRouter, Route, Routes } from 'react-router-dom'
import { renderWithProviders } from '@/test/test-utils'
import { server } from '@/test/msw-handlers'
import { CreateDisputeDialog } from './create-dispute-dialog'

const defaultLineItem = {
  varianceId: 'var-001',
  amount: '100.00',
  expectedAmount: '150.00',
  timestamp: '2026-01-01T00:00:00Z',
}

function renderDialog(props: {
  open?: boolean
  onOpenChange?: (open: boolean) => void
  runId?: string
  lineItem?: typeof defaultLineItem
}) {
  const onOpenChange = props.onOpenChange ?? vi.fn()
  return renderWithProviders(
    <MemoryRouter initialEntries={['/reconciliation/run-123']}>
      <Routes>
        <Route
          path="/reconciliation/:runId"
          element={
            <CreateDisputeDialog
              open={props.open ?? true}
              onOpenChange={onOpenChange}
              runId={props.runId ?? 'run-123'}
              lineItem={props.lineItem ?? defaultLineItem}
            />
          }
        />
      </Routes>
    </MemoryRouter>,
  )
}

describe('CreateDisputeDialog', () => {
  afterEach(() => {
    vi.restoreAllMocks()
  })

  describe('rendering', () => {
    it('renders dialog when open', () => {
      renderDialog({ open: true })
      expect(screen.getByRole('dialog')).toBeInTheDocument()
      expect(screen.getByRole('heading', { name: 'Raise Dispute' })).toBeInTheDocument()
    })

    it('does not render dialog when closed', () => {
      renderDialog({ open: false })
      expect(screen.queryByRole('dialog')).not.toBeInTheDocument()
    })

    it('renders reason select with options', () => {
      renderDialog({ open: true })
      const select = screen.getByRole('combobox', { name: /reason/i })
      expect(select).toBeInTheDocument()
      expect(screen.getByText('Amount Mismatch')).toBeInTheDocument()
      expect(screen.getByText('Missing Entry')).toBeInTheDocument()
      expect(screen.getByText('Duplicate')).toBeInTheDocument()
      expect(screen.getByText('Timing')).toBeInTheDocument()
      expect(screen.getByText('Other')).toBeInTheDocument()
    })

    it('renders description textarea', () => {
      renderDialog({ open: true })
      expect(screen.getByLabelText('Description')).toBeInTheDocument()
    })

    it('renders Raise Dispute and Cancel buttons', () => {
      renderDialog({ open: true })
      expect(screen.getByRole('button', { name: /raise dispute/i })).toBeInTheDocument()
      expect(screen.getByRole('button', { name: /cancel/i })).toBeInTheDocument()
    })
  })

  describe('pre-filled line item context', () => {
    it('displays varianceId in the disputed item section', () => {
      renderDialog({ open: true })
      expect(screen.getByText('var-001')).toBeInTheDocument()
    })

    it('displays amount in the disputed item section', () => {
      renderDialog({ open: true })
      expect(screen.getByText('100.00')).toBeInTheDocument()
    })

    it('displays expectedAmount when provided', () => {
      renderDialog({ open: true })
      expect(screen.getByText('150.00')).toBeInTheDocument()
    })

    it('displays timestamp when provided', () => {
      renderDialog({ open: true })
      expect(screen.getByText('2026-01-01T00:00:00Z')).toBeInTheDocument()
    })

    it('does not display expectedAmount section when not provided', () => {
      renderDialog({
        open: true,
        lineItem: { varianceId: 'var-002', amount: '50.00' },
      })
      expect(screen.queryByText('Expected:')).not.toBeInTheDocument()
    })
  })

  describe('conditional expected amount field', () => {
    it('does not show expected amount input by default', () => {
      renderDialog({ open: true })
      expect(screen.queryByLabelText('Expected Amount')).not.toBeInTheDocument()
    })

    it('shows expected amount input when reason is AMOUNT_MISMATCH', async () => {
      const user = userEvent.setup()
      renderDialog({ open: true })

      await user.selectOptions(screen.getByRole('combobox', { name: /reason/i }), 'DISPUTE_REASON_AMOUNT_MISMATCH')

      expect(screen.getByLabelText('Expected Amount')).toBeInTheDocument()
    })

    it('hides expected amount input when reason changes away from AMOUNT_MISMATCH', async () => {
      const user = userEvent.setup()
      renderDialog({ open: true })

      await user.selectOptions(screen.getByRole('combobox', { name: /reason/i }), 'DISPUTE_REASON_AMOUNT_MISMATCH')
      expect(screen.getByLabelText('Expected Amount')).toBeInTheDocument()

      await user.selectOptions(screen.getByRole('combobox', { name: /reason/i }), 'DISPUTE_REASON_OTHER')
      expect(screen.queryByLabelText('Expected Amount')).not.toBeInTheDocument()
    })

    it('shows error when expected amount is empty for AMOUNT_MISMATCH reason', async () => {
      const user = userEvent.setup()
      renderDialog({ open: true })

      await user.selectOptions(screen.getByRole('combobox', { name: /reason/i }), 'DISPUTE_REASON_AMOUNT_MISMATCH')
      await user.type(screen.getByLabelText('Description'), 'Amount is wrong')
      await user.click(screen.getByRole('button', { name: /raise dispute/i }))

      expect(
        await screen.findByText('Expected amount is required for amount mismatch disputes'),
      ).toBeInTheDocument()
    })
  })

  describe('description validation', () => {
    it('shows error when description is empty', async () => {
      const user = userEvent.setup()
      renderDialog({ open: true })

      await user.selectOptions(screen.getByRole('combobox', { name: /reason/i }), 'DISPUTE_REASON_OTHER')
      await user.click(screen.getByRole('button', { name: /raise dispute/i }))

      expect(await screen.findByText('Description is required')).toBeInTheDocument()
    })

    it('shows error when reason is not selected', async () => {
      const user = userEvent.setup()
      renderDialog({ open: true })

      await user.type(screen.getByLabelText('Description'), 'Some description')
      await user.click(screen.getByRole('button', { name: /raise dispute/i }))

      expect(await screen.findByText('Reason is required')).toBeInTheDocument()
    })
  })

  describe('successful submission', () => {
    it('calls API and closes dialog on success', async () => {
      const user = userEvent.setup()
      const onOpenChange = vi.fn()

      server.use(
        http.post('*/reconciliation/runs/run-123/disputes', () => {
          return HttpResponse.json({ disputeId: 'dispute-001' })
        }),
      )

      renderDialog({ open: true, onOpenChange })

      await user.selectOptions(screen.getByRole('combobox', { name: /reason/i }), 'DISPUTE_REASON_OTHER')
      await user.type(screen.getByLabelText('Description'), 'Test dispute description')
      await user.click(screen.getByRole('button', { name: /raise dispute/i }))

      await waitFor(() => {
        expect(onOpenChange).toHaveBeenCalledWith(false)
      })
    })

    it('submits with expectedAmount when reason is AMOUNT_MISMATCH', async () => {
      const user = userEvent.setup()
      const onOpenChange = vi.fn()
      let capturedBody: unknown

      server.use(
        http.post('*/reconciliation/runs/run-123/disputes', async ({ request }) => {
          capturedBody = await request.json()
          return HttpResponse.json({ disputeId: 'dispute-002' })
        }),
      )

      renderDialog({ open: true, onOpenChange })

      await user.selectOptions(screen.getByRole('combobox', { name: /reason/i }), 'DISPUTE_REASON_AMOUNT_MISMATCH')
      await user.type(screen.getByLabelText('Expected Amount'), '200.00')
      await user.type(screen.getByLabelText('Description'), 'Amount does not match')
      await user.click(screen.getByRole('button', { name: /raise dispute/i }))

      await waitFor(() => {
        expect(onOpenChange).toHaveBeenCalledWith(false)
      })

      expect(capturedBody).toMatchObject({
        reason: 'DISPUTE_REASON_AMOUNT_MISMATCH',
        expectedAmount: '200',
        description: 'Amount does not match',
        varianceId: 'var-001',
      })
    })

    it('completes dispute submission without error', async () => {
      const user = userEvent.setup()

      server.use(
        http.post('*/reconciliation/runs/run-123/disputes', () => {
          return HttpResponse.json({ disputeId: 'dispute-003' })
        }),
      )

      const onOpenChange = vi.fn()
      renderDialog({ open: true, onOpenChange })

      await user.selectOptions(screen.getByRole('combobox', { name: /reason/i }), 'DISPUTE_REASON_OTHER')
      await user.type(screen.getByLabelText('Description'), 'Test')
      await user.click(screen.getByRole('button', { name: /raise dispute/i }))

      await waitFor(() => {
        expect(onOpenChange).toHaveBeenCalledWith(false)
      })
    })
  })

  describe('form reset on close', () => {
    it('resets form fields when dialog is closed and reopened', async () => {
      const user = userEvent.setup()
      const { rerender } = renderWithProviders(
        <MemoryRouter>
          <Routes>
            <Route
              path="/"
              element={
                <CreateDisputeDialog
                  open={true}
                  onOpenChange={vi.fn()}
                  runId="run-123"
                  lineItem={defaultLineItem}
                />
              }
            />
          </Routes>
        </MemoryRouter>,
      )

      // Populate fields before closing
      await user.selectOptions(screen.getByRole('combobox', { name: /reason/i }), 'DISPUTE_REASON_OTHER')
      await user.type(screen.getByLabelText('Description'), 'Some content')

      expect((screen.getByRole('combobox', { name: /reason/i }) as HTMLSelectElement).value).toBe('DISPUTE_REASON_OTHER')
      expect((screen.getByLabelText('Description') as HTMLTextAreaElement).value).toBe('Some content')

      // Close dialog
      rerender(
        <MemoryRouter>
          <Routes>
            <Route
              path="/"
              element={
                <CreateDisputeDialog
                  open={false}
                  onOpenChange={vi.fn()}
                  runId="run-123"
                  lineItem={defaultLineItem}
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
                <CreateDisputeDialog
                  open={true}
                  onOpenChange={vi.fn()}
                  runId="run-123"
                  lineItem={defaultLineItem}
                />
              }
            />
          </Routes>
        </MemoryRouter>,
      )

      // Fields should be reset to empty
      expect((screen.getByRole('combobox', { name: /reason/i }) as HTMLSelectElement).value).toBe('')
      expect((screen.getByLabelText('Description') as HTMLTextAreaElement).value).toBe('')
    })
  })

  describe('API error handling', () => {
    it('shows error alert when API returns an error', async () => {
      const user = userEvent.setup()

      server.use(
        http.post('*/reconciliation/runs/run-123/disputes', () => {
          return HttpResponse.json({ message: 'Internal server error' }, { status: 500 })
        }),
      )

      renderDialog({ open: true })

      await user.selectOptions(screen.getByRole('combobox', { name: /reason/i }), 'DISPUTE_REASON_OTHER')
      await user.type(screen.getByLabelText('Description'), 'Test description')
      await user.click(screen.getByRole('button', { name: /raise dispute/i }))

      await waitFor(() => {
        expect(screen.getByRole('alert')).toBeInTheDocument()
      })

      expect(screen.getByRole('alert')).toHaveTextContent('Internal server error')
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
