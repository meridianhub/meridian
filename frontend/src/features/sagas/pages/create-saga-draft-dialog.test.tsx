import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { BrowserRouter } from 'react-router-dom'
import { TooltipProvider } from '@/components/ui/tooltip'

const mockCreateSagaDraft = vi.hoisted(() =>
  vi.fn().mockResolvedValue({
    saga: {
      id: 'aaaaaaaa-0000-0000-0000-000000000001',
      name: 'savings.withdraw',
      version: 1,
      status: 1, // DRAFT
    },
  }),
)

vi.mock('@/api/context', () => ({
  useApiClients: vi.fn(() => ({
    sagaRegistry: {
      createSagaDraft: mockCreateSagaDraft,
    },
  })),
}))

import { CreateSagaDraftDialog } from './create-saga-draft-dialog'

function makeQueryClient() {
  return new QueryClient({
    defaultOptions: {
      queries: { retry: false, staleTime: Infinity },
      mutations: { retry: false },
    },
  })
}

function Wrapper({ children, queryClient }: { children: React.ReactNode; queryClient?: QueryClient }) {
  const qc = queryClient ?? makeQueryClient()
  return (
    <QueryClientProvider client={qc}>
      <TooltipProvider>
        <BrowserRouter>{children}</BrowserRouter>
      </TooltipProvider>
    </QueryClientProvider>
  )
}

function renderDialog(props: { open?: boolean; onOpenChange?: (open: boolean) => void } = {}) {
  const onOpenChange = props.onOpenChange ?? vi.fn()
  render(
    <Wrapper>
      <CreateSagaDraftDialog open={props.open ?? true} onOpenChange={onOpenChange} />
    </Wrapper>,
  )
  return { onOpenChange }
}

const STARTER_SCRIPT_FRAGMENT = 'def execute(ctx):'

describe('CreateSagaDraftDialog - rendering', () => {
  beforeEach(() => {
    vi.clearAllMocks()
  })

  it('renders dialog when open', () => {
    renderDialog()
    expect(screen.getByRole('dialog')).toBeInTheDocument()
    expect(screen.getByRole('heading', { name: /create saga draft/i })).toBeInTheDocument()
  })

  it('does not render dialog content when closed', () => {
    renderDialog({ open: false })
    expect(screen.queryByRole('dialog')).not.toBeInTheDocument()
  })

  it('renders all form fields', () => {
    renderDialog()
    expect(screen.getByLabelText(/^name$/i)).toBeInTheDocument()
    expect(screen.getByLabelText(/display name/i)).toBeInTheDocument()
    expect(screen.getByLabelText(/description/i)).toBeInTheDocument()
    expect(screen.getByLabelText(/^script$/i)).toBeInTheDocument()
    expect(screen.getByLabelText(/preconditions cel/i)).toBeInTheDocument()
  })

  it('renders Cancel and Create buttons', () => {
    renderDialog()
    expect(screen.getByRole('button', { name: /cancel/i })).toBeInTheDocument()
    expect(screen.getByRole('button', { name: /create saga draft/i })).toBeInTheDocument()
  })

  it('pre-fills script with starter template', () => {
    renderDialog()
    const scriptArea = screen.getByLabelText(/^script$/i) as HTMLTextAreaElement
    expect(scriptArea.value).toContain(STARTER_SCRIPT_FRAGMENT)
  })
})

describe('CreateSagaDraftDialog - name validation', () => {
  beforeEach(() => {
    vi.clearAllMocks()
  })

  it('shows error when name is empty', async () => {
    const user = userEvent.setup()
    renderDialog()
    await user.click(screen.getByRole('button', { name: /create saga draft/i }))
    expect(await screen.findByText(/name is required/i)).toBeInTheDocument()
  })

  it('shows error for name starting with digit', async () => {
    const user = userEvent.setup()
    renderDialog()
    await user.type(screen.getByLabelText(/^name$/i), '1invalid')
    await user.click(screen.getByRole('button', { name: /create saga draft/i }))
    expect(await screen.findByText(/must start with a lowercase letter/i)).toBeInTheDocument()
  })

  it('shows error for name with uppercase letters', async () => {
    const user = userEvent.setup()
    renderDialog()
    await user.type(screen.getByLabelText(/^name$/i), 'InvalidName')
    await user.click(screen.getByRole('button', { name: /create saga draft/i }))
    expect(await screen.findByText(/must start with a lowercase letter/i)).toBeInTheDocument()
  })

  it('accepts valid name with dot separator', async () => {
    const user = userEvent.setup()
    renderDialog()
    await user.type(screen.getByLabelText(/^name$/i), 'savings.withdraw')
    await user.click(screen.getByRole('button', { name: /create saga draft/i }))
    await waitFor(() => {
      expect(screen.queryByText(/must start with a lowercase letter/i)).not.toBeInTheDocument()
      expect(screen.queryByText(/name is required/i)).not.toBeInTheDocument()
    })
  })

  it('accepts valid name with underscores', async () => {
    const user = userEvent.setup()
    renderDialog()
    await user.type(screen.getByLabelText(/^name$/i), 'payment_order.create')
    await user.click(screen.getByRole('button', { name: /create saga draft/i }))
    await waitFor(() => {
      expect(screen.queryByText(/must start with a lowercase letter/i)).not.toBeInTheDocument()
    })
  })
})

describe('CreateSagaDraftDialog - script validation', () => {
  beforeEach(() => {
    vi.clearAllMocks()
  })

  it('shows error when script is cleared', async () => {
    const user = userEvent.setup()
    renderDialog()
    const scriptArea = screen.getByLabelText(/^script$/i)
    await user.clear(scriptArea)
    await user.type(screen.getByLabelText(/^name$/i), 'savings.withdraw')
    await user.click(screen.getByRole('button', { name: /create saga draft/i }))
    expect(await screen.findByText(/script is required/i)).toBeInTheDocument()
  })
})

describe('CreateSagaDraftDialog - successful submission', () => {
  beforeEach(() => {
    vi.clearAllMocks()
  })

  it('calls createSagaDraft with correct data', async () => {
    const user = userEvent.setup()
    renderDialog()

    await user.type(screen.getByLabelText(/^name$/i), 'savings.withdraw')
    await user.type(screen.getByLabelText(/display name/i), 'Savings Withdrawal')
    await user.click(screen.getByRole('button', { name: /create saga draft/i }))

    await waitFor(() => {
      expect(mockCreateSagaDraft).toHaveBeenCalledOnce()
      expect(mockCreateSagaDraft).toHaveBeenCalledWith(
        expect.objectContaining({
          name: 'savings.withdraw',
          displayName: 'Savings Withdrawal',
          version: 1,
        }),
      )
    })
  })

  it('disables submit button while pending', async () => {
    const user = userEvent.setup()
    mockCreateSagaDraft.mockReturnValueOnce(new Promise(() => {}))
    renderDialog()

    await user.type(screen.getByLabelText(/^name$/i), 'savings.withdraw')
    await user.click(screen.getByRole('button', { name: /create saga draft/i }))

    await waitFor(() => {
      expect(screen.getByRole('button', { name: /creating/i })).toBeDisabled()
    })
  })

  it('closes dialog on successful submission', async () => {
    const user = userEvent.setup()
    const { onOpenChange } = renderDialog()

    await user.type(screen.getByLabelText(/^name$/i), 'savings.withdraw')
    await user.click(screen.getByRole('button', { name: /create saga draft/i }))

    await waitFor(() => {
      expect(onOpenChange).toHaveBeenCalledWith(false)
    })
  })
})

describe('CreateSagaDraftDialog - error handling', () => {
  beforeEach(() => {
    vi.clearAllMocks()
  })

  it('shows general error banner on server error', async () => {
    const user = userEvent.setup()
    const { Code, ConnectError } = await import('@connectrpc/connect')
    const err = new ConnectError('Service unavailable', Code.Unavailable)
    mockCreateSagaDraft.mockRejectedValueOnce(err)

    renderDialog()
    await user.type(screen.getByLabelText(/^name$/i), 'savings.withdraw')
    await user.click(screen.getByRole('button', { name: /create saga draft/i }))

    await waitFor(() => {
      expect(screen.getByRole('alert')).toBeInTheDocument()
    })
  })
})

describe('CreateSagaDraftDialog - close and reset', () => {
  it('closes dialog on Cancel button click', async () => {
    const user = userEvent.setup()
    const { onOpenChange } = renderDialog()
    await user.click(screen.getByRole('button', { name: /cancel/i }))
    expect(onOpenChange).toHaveBeenCalledWith(false)
  })

  it('resets form when dialog is closed and reopened', async () => {
    const user = userEvent.setup()
    const onOpenChange = vi.fn()
    const qc = makeQueryClient()
    const { rerender } = render(
      <Wrapper queryClient={qc}>
        <CreateSagaDraftDialog open={true} onOpenChange={onOpenChange} />
      </Wrapper>,
    )

    await user.type(screen.getByLabelText(/^name$/i), 'savings.withdraw')
    expect(screen.getByLabelText(/^name$/i)).toHaveValue('savings.withdraw')

    rerender(
      <Wrapper queryClient={qc}>
        <CreateSagaDraftDialog open={false} onOpenChange={onOpenChange} />
      </Wrapper>,
    )
    rerender(
      <Wrapper queryClient={qc}>
        <CreateSagaDraftDialog open={true} onOpenChange={onOpenChange} />
      </Wrapper>,
    )

    expect(screen.getByLabelText(/^name$/i)).toHaveValue('')
    // Script should be reset to starter template
    const scriptArea = screen.getByLabelText(/^script$/i) as HTMLTextAreaElement
    expect(scriptArea.value).toContain(STARTER_SCRIPT_FRAGMENT)
  })
})
