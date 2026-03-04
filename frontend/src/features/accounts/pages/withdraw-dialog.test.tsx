import { describe, it, expect, vi, beforeEach } from 'vitest'
import { screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { MemoryRouter } from 'react-router-dom'
import { http, HttpResponse } from 'msw'
import { server } from '@/test/msw-handlers'
import { renderWithProviders } from '@/test/test-utils'
import { createTenantUserToken } from '@/test/jwt-helpers'
import { WithdrawDialog } from './withdraw-dialog'

const tenantToken = createTenantUserToken('tenant-test')

function renderWithdrawDialog(props?: {
  open?: boolean
  onOpenChange?: (open: boolean) => void
  accountId?: string
  currency?: string
}) {
  const {
    open = true,
    onOpenChange = vi.fn(),
    accountId = 'acct-001',
    currency = 'GBP',
  } = props ?? {}
  return renderWithProviders(
    <MemoryRouter>
      <WithdrawDialog
        open={open}
        onOpenChange={onOpenChange}
        accountId={accountId}
        currency={currency}
      />
    </MemoryRouter>,
    { initialToken: tenantToken },
  )
}

describe('WithdrawDialog - rendering', () => {
  it('renders dialog when open', () => {
    renderWithdrawDialog()
    expect(screen.getByRole('dialog')).toBeInTheDocument()
  })

  it('renders dialog title', () => {
    renderWithdrawDialog()
    expect(screen.getAllByText(/withdraw/i).length).toBeGreaterThan(0)
  })

  it('renders amount input on step 1', () => {
    renderWithdrawDialog()
    expect(screen.getByLabelText(/amount/i)).toBeInTheDocument()
  })

  it('renders initiate button on step 1', () => {
    renderWithdrawDialog()
    expect(screen.getByRole('button', { name: /initiate/i })).toBeInTheDocument()
  })

  it('does not render when closed', () => {
    renderWithdrawDialog({ open: false })
    expect(screen.queryByRole('dialog')).not.toBeInTheDocument()
  })
})

describe('WithdrawDialog - step 1 validation', () => {
  it('shows error when amount is empty', async () => {
    renderWithdrawDialog()
    await userEvent.click(screen.getByRole('button', { name: /initiate/i }))
    await waitFor(() => {
      expect(screen.getByText(/amount is required/i)).toBeInTheDocument()
    })
  })

  it('shows error when amount is zero', async () => {
    renderWithdrawDialog()
    await userEvent.type(screen.getByLabelText(/amount/i), '0')
    await userEvent.click(screen.getByRole('button', { name: /initiate/i }))
    await waitFor(() => {
      expect(screen.getByText(/amount must be greater than zero/i)).toBeInTheDocument()
    })
  })

  it('shows error when amount is negative', async () => {
    renderWithdrawDialog()
    await userEvent.type(screen.getByLabelText(/amount/i), '-5')
    await userEvent.click(screen.getByRole('button', { name: /initiate/i }))
    await waitFor(() => {
      expect(screen.getByText(/amount must be greater than zero/i)).toBeInTheDocument()
    })
  })
})

describe('WithdrawDialog - two-step flow', () => {
  beforeEach(() => {
    server.use(
      http.post(
        '*/meridian.current_account.v1.CurrentAccountService/InitiateWithdrawal',
        () => HttpResponse.json({ withdrawalId: 'wdl-001' }),
      ),
      http.post(
        '*/meridian.current_account.v1.CurrentAccountService/ExecuteWithdrawal',
        () => HttpResponse.json({}),
      ),
    )
  })

  it('moves to step 2 after successful initiate', async () => {
    renderWithdrawDialog()
    await userEvent.type(screen.getByLabelText(/amount/i), '50.00')
    await userEvent.click(screen.getByRole('button', { name: /initiate/i }))

    await waitFor(() => {
      expect(screen.getByRole('button', { name: /confirm/i })).toBeInTheDocument()
    })
  })

  it('shows confirmation details on step 2', async () => {
    renderWithdrawDialog()
    await userEvent.type(screen.getByLabelText(/amount/i), '50.00')
    await userEvent.click(screen.getByRole('button', { name: /initiate/i }))

    await waitFor(() => {
      expect(screen.getAllByText(/50/i).length).toBeGreaterThan(0)
    })
  })

  it('calls execute mutation with withdrawalId on confirm', async () => {
    let capturedBody: unknown
    server.use(
      http.post(
        '*/meridian.current_account.v1.CurrentAccountService/InitiateWithdrawal',
        () => HttpResponse.json({ withdrawalId: 'wdl-123' }),
      ),
      http.post(
        '*/meridian.current_account.v1.CurrentAccountService/ExecuteWithdrawal',
        async ({ request }) => {
          capturedBody = await request.json()
          return HttpResponse.json({})
        },
      ),
    )

    renderWithdrawDialog()
    await userEvent.type(screen.getByLabelText(/amount/i), '50.00')
    await userEvent.click(screen.getByRole('button', { name: /initiate/i }))

    await waitFor(() => {
      expect(screen.getByRole('button', { name: /confirm/i })).toBeInTheDocument()
    })

    await userEvent.click(screen.getByRole('button', { name: /confirm/i }))

    await waitFor(() => {
      expect(capturedBody).toMatchObject({ withdrawalId: 'wdl-123' })
    })
  })

  it('closes dialog after successful execute', async () => {
    const onOpenChange = vi.fn()
    renderWithdrawDialog({ onOpenChange })

    await userEvent.type(screen.getByLabelText(/amount/i), '50.00')
    await userEvent.click(screen.getByRole('button', { name: /initiate/i }))

    await waitFor(() => {
      expect(screen.getByRole('button', { name: /confirm/i })).toBeInTheDocument()
    })

    await userEvent.click(screen.getByRole('button', { name: /confirm/i }))

    await waitFor(() => {
      expect(onOpenChange).toHaveBeenCalledWith(false)
    })
  })

  it('shows error when initiate fails', async () => {
    server.use(
      http.post(
        '*/meridian.current_account.v1.CurrentAccountService/InitiateWithdrawal',
        () => HttpResponse.json({ message: 'Insufficient balance' }, { status: 400 }),
      ),
    )

    renderWithdrawDialog()
    await userEvent.type(screen.getByLabelText(/amount/i), '50.00')
    await userEvent.click(screen.getByRole('button', { name: /initiate/i }))

    await waitFor(() => {
      expect(screen.getByRole('alert')).toBeInTheDocument()
    })
  })
})

describe('WithdrawDialog - cancel', () => {
  it('calls onOpenChange(false) when cancel clicked', async () => {
    const onOpenChange = vi.fn()
    renderWithdrawDialog({ onOpenChange })

    await userEvent.click(screen.getByRole('button', { name: /cancel/i }))
    expect(onOpenChange).toHaveBeenCalledWith(false)
  })
})
