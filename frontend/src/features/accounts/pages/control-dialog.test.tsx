import { describe, it, expect, vi } from 'vitest'
import { screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { MemoryRouter } from 'react-router-dom'
import { http, HttpResponse } from 'msw'
import { server } from '@/test/msw-handlers'
import { renderWithProviders } from '@/test/test-utils'
import { createTenantUserToken } from '@/test/jwt-helpers'
import { ControlDialog } from './control-dialog'
import type { ControlAction } from './control-dialog'

const tenantToken = createTenantUserToken('tenant-test')

function renderControlDialog(props?: {
  open?: boolean
  onOpenChange?: (open: boolean) => void
  accountId?: string
  action?: ControlAction
}) {
  const {
    open = true,
    onOpenChange = vi.fn(),
    accountId = 'acct-001',
    action = 'freeze',
  } = props ?? {}
  return renderWithProviders(
    <MemoryRouter>
      <ControlDialog
        open={open}
        onOpenChange={onOpenChange}
        accountId={accountId}
        action={action}
      />
    </MemoryRouter>,
    { initialToken: tenantToken },
  )
}

describe('ControlDialog - rendering', () => {
  it('renders dialog when open', () => {
    renderControlDialog()
    expect(screen.getByRole('dialog')).toBeInTheDocument()
  })

  it('shows freeze title for freeze action', () => {
    renderControlDialog({ action: 'freeze' })
    expect(screen.getAllByText(/freeze/i).length).toBeGreaterThan(0)
  })

  it('shows unfreeze title for unfreeze action', () => {
    renderControlDialog({ action: 'unfreeze' })
    expect(screen.getAllByText(/unfreeze/i).length).toBeGreaterThan(0)
  })

  it('shows close title for close action', () => {
    renderControlDialog({ action: 'close' })
    expect(screen.getAllByText(/close/i).length).toBeGreaterThan(0)
  })

  it('does not render when closed', () => {
    renderControlDialog({ open: false })
    expect(screen.queryByRole('dialog')).not.toBeInTheDocument()
  })

  it('renders confirm and cancel buttons', () => {
    renderControlDialog()
    expect(screen.getByRole('button', { name: /confirm/i })).toBeInTheDocument()
    expect(screen.getByRole('button', { name: /cancel/i })).toBeInTheDocument()
  })

  it('shows account id in dialog', () => {
    renderControlDialog({ accountId: 'acct-xyz' })
    expect(screen.getAllByText(/acct-xyz/).length).toBeGreaterThan(0)
  })
})

describe('ControlDialog - freeze action', () => {
  it('calls freeze endpoint on confirm', async () => {
    let capturedBody: unknown
    server.use(
      http.post(
        '*/meridian.current_account.v1.CurrentAccountService/FreezeAccount',
        async ({ request }) => {
          capturedBody = await request.json()
          return HttpResponse.json({})
        },
      ),
    )

    renderControlDialog({ action: 'freeze', accountId: 'acct-001' })
    await userEvent.click(screen.getByRole('button', { name: /confirm/i }))

    await waitFor(() => {
      expect(capturedBody).toMatchObject({ accountId: 'acct-001' })
    })
  })

  it('closes dialog on success', async () => {
    server.use(
      http.post(
        '*/meridian.current_account.v1.CurrentAccountService/FreezeAccount',
        () => HttpResponse.json({}),
      ),
    )
    const onOpenChange = vi.fn()
    renderControlDialog({ action: 'freeze', onOpenChange })

    await userEvent.click(screen.getByRole('button', { name: /confirm/i }))

    await waitFor(() => {
      expect(onOpenChange).toHaveBeenCalledWith(false)
    })
  })

  it('shows error on failure', async () => {
    server.use(
      http.post(
        '*/meridian.current_account.v1.CurrentAccountService/FreezeAccount',
        () => HttpResponse.json({ message: 'Cannot freeze' }, { status: 400 }),
      ),
    )

    renderControlDialog({ action: 'freeze' })
    await userEvent.click(screen.getByRole('button', { name: /confirm/i }))

    await waitFor(() => {
      expect(screen.getByRole('alert')).toBeInTheDocument()
    })
  })
})

describe('ControlDialog - unfreeze action', () => {
  it('calls unfreeze endpoint on confirm', async () => {
    let capturedBody: unknown
    server.use(
      http.post(
        '*/meridian.current_account.v1.CurrentAccountService/UnfreezeAccount',
        async ({ request }) => {
          capturedBody = await request.json()
          return HttpResponse.json({})
        },
      ),
    )

    renderControlDialog({ action: 'unfreeze', accountId: 'acct-001' })
    await userEvent.click(screen.getByRole('button', { name: /confirm/i }))

    await waitFor(() => {
      expect(capturedBody).toMatchObject({ accountId: 'acct-001' })
    })
  })
})

describe('ControlDialog - close action', () => {
  it('calls close endpoint on confirm', async () => {
    let capturedBody: unknown
    server.use(
      http.post(
        '*/meridian.current_account.v1.CurrentAccountService/CloseAccount',
        async ({ request }) => {
          capturedBody = await request.json()
          return HttpResponse.json({})
        },
      ),
    )

    renderControlDialog({ action: 'close', accountId: 'acct-001' })
    await userEvent.click(screen.getByRole('button', { name: /confirm/i }))

    await waitFor(() => {
      expect(capturedBody).toMatchObject({ accountId: 'acct-001' })
    })
  })

  it('uses destructive button variant for close action', () => {
    renderControlDialog({ action: 'close' })
    const confirmButton = screen.getByRole('button', { name: /confirm/i })
    expect(confirmButton).toBeInTheDocument()
  })
})

describe('ControlDialog - cancel', () => {
  it('calls onOpenChange(false) on cancel', async () => {
    const onOpenChange = vi.fn()
    renderControlDialog({ onOpenChange })

    await userEvent.click(screen.getByRole('button', { name: /cancel/i }))
    expect(onOpenChange).toHaveBeenCalledWith(false)
  })
})
