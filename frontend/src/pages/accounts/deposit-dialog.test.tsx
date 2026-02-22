import { describe, it, expect, vi, beforeEach } from 'vitest'
import { screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { MemoryRouter } from 'react-router-dom'
import { http, HttpResponse } from 'msw'
import { server } from '@/test/msw-handlers'
import { renderWithProviders } from '@/test/test-utils'
import { createTenantUserToken } from '@/test/jwt-helpers'
import { DepositDialog } from './deposit-dialog'

const tenantToken = createTenantUserToken('tenant-test')

function renderDepositDialog(props: {
  open?: boolean
  onOpenChange?: (open: boolean) => void
  accountId?: string
  currency?: string
}) {
  const { open = true, onOpenChange = vi.fn(), accountId = 'acct-001', currency = 'GBP' } = props
  return renderWithProviders(
    <MemoryRouter>
      <DepositDialog
        open={open}
        onOpenChange={onOpenChange}
        accountId={accountId}
        currency={currency}
      />
    </MemoryRouter>,
    { initialToken: tenantToken },
  )
}

describe('DepositDialog - rendering', () => {
  it('renders dialog title when open', () => {
    renderDepositDialog({})
    expect(screen.getByRole('dialog')).toBeInTheDocument()
    expect(screen.getAllByText(/deposit funds/i).length).toBeGreaterThan(0)
  })

  it('renders amount input', () => {
    renderDepositDialog({})
    expect(screen.getByLabelText(/amount/i)).toBeInTheDocument()
  })

  it('renders currency in dialog', () => {
    renderDepositDialog({ currency: 'GBP' })
    expect(screen.getAllByText(/GBP/).length).toBeGreaterThan(0)
  })

  it('renders deposit and cancel buttons', () => {
    renderDepositDialog({})
    expect(screen.getByRole('button', { name: /deposit/i })).toBeInTheDocument()
    expect(screen.getByRole('button', { name: /cancel/i })).toBeInTheDocument()
  })

  it('does not render when closed', () => {
    renderDepositDialog({ open: false })
    expect(screen.queryByRole('dialog')).not.toBeInTheDocument()
  })
})

describe('DepositDialog - validation', () => {
  it('shows error when amount is empty', async () => {
    renderDepositDialog({})
    await userEvent.click(screen.getByRole('button', { name: /deposit/i }))
    await waitFor(() => {
      expect(screen.getByText(/amount is required/i)).toBeInTheDocument()
    })
  })

  it('shows error when amount is zero', async () => {
    renderDepositDialog({})
    await userEvent.type(screen.getByLabelText(/amount/i), '0')
    await userEvent.click(screen.getByRole('button', { name: /deposit/i }))
    await waitFor(() => {
      expect(screen.getByText(/amount must be greater than zero/i)).toBeInTheDocument()
    })
  })

  it('shows error when amount is negative', async () => {
    renderDepositDialog({})
    await userEvent.type(screen.getByLabelText(/amount/i), '-10')
    await userEvent.click(screen.getByRole('button', { name: /deposit/i }))
    await waitFor(() => {
      expect(screen.getByText(/amount must be greater than zero/i)).toBeInTheDocument()
    })
  })

  it('shows error when amount is not a valid number', async () => {
    renderDepositDialog({})
    await userEvent.type(screen.getByLabelText(/amount/i), 'abc')
    await userEvent.click(screen.getByRole('button', { name: /deposit/i }))
    await waitFor(() => {
      expect(screen.getByText(/invalid amount/i)).toBeInTheDocument()
    })
  })
})

describe('DepositDialog - submission', () => {
  beforeEach(() => {
    server.use(
      http.post('*/meridian.current_account.v1.CurrentAccountService/DepositFunds', () =>
        HttpResponse.json({}),
      ),
    )
  })

  it('calls mutation with correct BigInt conversion on submit', async () => {
    let capturedBody: unknown
    server.use(
      http.post('*/meridian.current_account.v1.CurrentAccountService/DepositFunds', async ({ request }) => {
        capturedBody = await request.json()
        return HttpResponse.json({})
      }),
    )

    renderDepositDialog({})
    await userEvent.type(screen.getByLabelText(/amount/i), '100.50')
    await userEvent.click(screen.getByRole('button', { name: /deposit/i }))

    await waitFor(() => {
      expect(capturedBody).toMatchObject({
        accountId: 'acct-001',
        amount: { amount: '10050' },
      })
    })
  })

  it('closes dialog on success', async () => {
    const onOpenChange = vi.fn()
    renderDepositDialog({ onOpenChange })

    await userEvent.type(screen.getByLabelText(/amount/i), '100.00')
    await userEvent.click(screen.getByRole('button', { name: /deposit/i }))

    await waitFor(() => {
      expect(onOpenChange).toHaveBeenCalledWith(false)
    })
  })

  it('shows error message on failure', async () => {
    server.use(
      http.post('*/meridian.current_account.v1.CurrentAccountService/DepositFunds', () =>
        HttpResponse.json({ message: 'Insufficient permissions' }, { status: 400 }),
      ),
    )

    renderDepositDialog({})
    await userEvent.type(screen.getByLabelText(/amount/i), '100.00')
    await userEvent.click(screen.getByRole('button', { name: /deposit/i }))

    await waitFor(() => {
      expect(screen.getByRole('alert')).toBeInTheDocument()
    })
  })

  it('disables submit button during submission', async () => {
    server.use(
      http.post('*/meridian.current_account.v1.CurrentAccountService/DepositFunds', async () => {
        await new Promise((resolve) => setTimeout(resolve, 100))
        return HttpResponse.json({})
      }),
    )

    renderDepositDialog({})
    await userEvent.type(screen.getByLabelText(/amount/i), '100.00')
    await userEvent.click(screen.getByRole('button', { name: /deposit/i }))

    expect(screen.getByRole('button', { name: /depositing|deposit/i })).toBeDisabled()
  })
})

describe('DepositDialog - cancel', () => {
  it('calls onOpenChange(false) when cancel clicked', async () => {
    const onOpenChange = vi.fn()
    renderDepositDialog({ onOpenChange })

    await userEvent.click(screen.getByRole('button', { name: /cancel/i }))
    expect(onOpenChange).toHaveBeenCalledWith(false)
  })
})
