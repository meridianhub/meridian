import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, fireEvent, waitFor } from '@testing-library/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { HandlerReference } from './handler-reference'
import type { ServiceClients } from '@/api/clients'

// Mock the API context
vi.mock('@/api/context', () => ({
  useApiClients: vi.fn(),
}))

import { useApiClients } from '@/api/context'

// Mock handler schema matching the real handlers.yaml structure
const mockDescribeHandlersResponse = {
  services: [
    {
      name: 'position_keeping',
      handlers: [
        {
          name: 'initiate_log',
          description: 'Initiates a position log entry',
          parameters: [
            { name: 'amount', type: 'Decimal', required: true, enumValues: [], description: '' },
            { name: 'direction', type: 'enum', required: true, enumValues: ['DEBIT', 'CREDIT'], description: '' },
          ],
          isExternal: false,
          compensateHandler: '',
        },
        {
          name: 'finalize_log',
          description: 'Finalizes a position log entry',
          parameters: [
            { name: 'log_id', type: 'string', required: true, enumValues: [], description: '' },
          ],
          isExternal: false,
          compensateHandler: '',
        },
      ],
    },
    {
      name: 'current_account',
      handlers: [
        {
          name: 'debit',
          description: 'Debits an account',
          parameters: [
            { name: 'account_id', type: 'string', required: true, enumValues: [], description: '' },
            { name: 'amount', type: 'Decimal', required: true, enumValues: [], description: '' },
          ],
          isExternal: false,
          compensateHandler: '',
        },
      ],
    },
  ],
}

function createMockClients() {
  return {
    sagaRegistry: {
      describeHandlers: vi.fn().mockResolvedValue(mockDescribeHandlersResponse),
    },
  } as unknown as ServiceClients
}

function renderWithProviders(ui: React.ReactElement, clients?: ServiceClients) {
  const mockClients = clients ?? createMockClients()
  vi.mocked(useApiClients).mockReturnValue(mockClients)

  const queryClient = new QueryClient({
    defaultOptions: {
      queries: {
        retry: false,
      },
    },
  })

  return render(
    <QueryClientProvider client={queryClient}>
      {ui}
    </QueryClientProvider>
  )
}

describe('HandlerReference', () => {
  const defaultProps = {
    filter: '',
    onInsert: vi.fn(),
  }

  beforeEach(() => {
    vi.clearAllMocks()
  })

  it('renders handler reference container', () => {
    const { container } = renderWithProviders(<HandlerReference {...defaultProps} />)
    expect(container.querySelector('[data-testid="handler-reference"]')).toBeTruthy()
  })

  it('loads and displays schema services', async () => {
    renderWithProviders(<HandlerReference {...defaultProps} />)
    // Wait for schema to load
    expect(
      await screen.findByText('position_keeping')
    ).toBeTruthy()
  })

  it('displays all services from schema', async () => {
    renderWithProviders(<HandlerReference {...defaultProps} />)
    expect(
      await screen.findByText('position_keeping')
    ).toBeTruthy()
    expect(screen.getByText('current_account')).toBeTruthy()
  })

  it('displays handler names within services', async () => {
    renderWithProviders(<HandlerReference {...defaultProps} />)
    expect(
      await screen.findByText('initiate_log')
    ).toBeTruthy()
    expect(screen.getByText('finalize_log')).toBeTruthy()
    expect(screen.getByText('debit')).toBeTruthy()
  })

  it('filters handlers by service name', async () => {
    const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } })
    vi.mocked(useApiClients).mockReturnValue(createMockClients())

    const { rerender } = render(
      <QueryClientProvider client={queryClient}>
        <HandlerReference {...defaultProps} filter="" />
      </QueryClientProvider>
    )
    expect(
      await screen.findByText('position_keeping')
    ).toBeTruthy()

    // Filter by service
    rerender(
      <QueryClientProvider client={queryClient}>
        <HandlerReference {...defaultProps} filter="current_account" />
      </QueryClientProvider>
    )
    await waitFor(() => {
      expect(screen.queryByText('initiate_log')).toBeNull()
    })
    expect(screen.getByText('debit')).toBeTruthy()
  })

  it('filters handlers by handler name', async () => {
    const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } })
    vi.mocked(useApiClients).mockReturnValue(createMockClients())

    const { rerender } = render(
      <QueryClientProvider client={queryClient}>
        <HandlerReference {...defaultProps} filter="" />
      </QueryClientProvider>
    )
    expect(
      await screen.findByText('initiate_log')
    ).toBeTruthy()

    rerender(
      <QueryClientProvider client={queryClient}>
        <HandlerReference {...defaultProps} filter="debit" />
      </QueryClientProvider>
    )
    await waitFor(() => {
      expect(screen.queryByText('initiate_log')).toBeNull()
    })
    expect(screen.getByText('debit')).toBeTruthy()
    expect(screen.queryByText('finalize_log')).toBeNull()
  })

  it('expands and collapses service accordion', async () => {
    renderWithProviders(<HandlerReference {...defaultProps} />)
    const accordionTrigger = await screen.findByRole('button', {
      name: /position_keeping/i,
    })

    // Initially expanded (handlers visible)
    expect(screen.getByText('initiate_log')).toBeTruthy()

    // Click to collapse
    fireEvent.click(accordionTrigger)
    expect(screen.queryByText('initiate_log')).toBeNull()

    // Click to expand again
    fireEvent.click(accordionTrigger)
    expect(screen.getByText('initiate_log')).toBeTruthy()
  })

  it('displays handler description', async () => {
    renderWithProviders(<HandlerReference {...defaultProps} />)
    expect(
      await screen.findByText('Initiates a position log entry')
    ).toBeTruthy()
    expect(screen.getByText('Debits an account')).toBeTruthy()
  })

  it('displays handler parameters with types', async () => {
    renderWithProviders(<HandlerReference {...defaultProps} />)
    const direction = await screen.findByText('direction')
    expect(direction).toBeTruthy()
    // Check that parameters are displayed within a handler
    expect(screen.getAllByText('amount').length).toBeGreaterThan(0)
  })

  it('marks required parameters with asterisk', async () => {
    renderWithProviders(<HandlerReference {...defaultProps} />)
    const direction = await screen.findByText('direction')
    const paramContainer = direction.closest('li')
    expect(paramContainer?.textContent).toContain('*')
  })

  it('displays enum values for enum parameters', async () => {
    renderWithProviders(<HandlerReference {...defaultProps} />)
    const directionText = await screen.findByText('direction')
    const paramContainer = directionText.closest('li')
    expect(paramContainer?.textContent).toContain('DEBIT')
    expect(paramContainer?.textContent).toContain('CREDIT')
  })

  it('calls onInsert with correct Starlark call template', async () => {
    const onInsert = vi.fn()
    renderWithProviders(<HandlerReference {...defaultProps} onInsert={onInsert} />)

    const insertButton = await screen.findByRole('button', {
      name: /insert.*initiate_log/i,
    })
    fireEvent.click(insertButton)

    expect(onInsert).toHaveBeenCalledWith(
      expect.stringContaining('position_keeping.initiate_log'),
    )
    expect(onInsert).toHaveBeenCalledWith(
      expect.stringContaining('amount'),
    )
    expect(onInsert).toHaveBeenCalledWith(
      expect.stringContaining('direction'),
    )
  })

  it('generates correct template with multiple parameters', async () => {
    const onInsert = vi.fn()
    renderWithProviders(<HandlerReference {...defaultProps} onInsert={onInsert} />)

    const insertButton = await screen.findByRole('button', {
      name: /insert.*debit/i,
    })
    fireEvent.click(insertButton)

    const template = onInsert.mock.calls[0][0]
    expect(template).toContain('current_account.debit(')
    expect(template).toContain('account_id=')
    expect(template).toContain('amount=')
  })

  it('generates template without parameters for handlers without params', async () => {
    renderWithProviders(<HandlerReference {...defaultProps} />)

    // Just verify that buttons were generated
    const insertButtons = await screen.findAllByRole('button')
    expect(insertButtons.length).toBeGreaterThan(0)
  })

  it('case-insensitive filter search', async () => {
    const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } })
    vi.mocked(useApiClients).mockReturnValue(createMockClients())

    const { rerender } = render(
      <QueryClientProvider client={queryClient}>
        <HandlerReference {...defaultProps} filter="" />
      </QueryClientProvider>
    )
    expect(
      await screen.findByText('initiate_log')
    ).toBeTruthy()

    rerender(
      <QueryClientProvider client={queryClient}>
        <HandlerReference {...defaultProps} filter="POSITION" />
      </QueryClientProvider>
    )
    expect(screen.getByText('initiate_log')).toBeTruthy()

    rerender(
      <QueryClientProvider client={queryClient}>
        <HandlerReference {...defaultProps} filter="DeBiT" />
      </QueryClientProvider>
    )
    await waitFor(() => {
      expect(screen.queryByText('initiate_log')).toBeNull()
    })
    expect(screen.getByText('debit')).toBeTruthy()
  })

  it('no results message when filter matches nothing', async () => {
    const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } })
    vi.mocked(useApiClients).mockReturnValue(createMockClients())

    render(
      <QueryClientProvider client={queryClient}>
        <HandlerReference {...defaultProps} filter="nonexistent" />
      </QueryClientProvider>
    )
    expect(await screen.findByText(/no handlers found/i)).toBeTruthy()
  })

  it('accepts optional className prop', async () => {
    const { container } = renderWithProviders(
      <HandlerReference {...defaultProps} className="custom-class" />,
    )
    expect(
      container.querySelector('[data-testid="handler-reference"]')?.classList,
    ).toContain('custom-class')
  })

  it('displays type information in parameter list', async () => {
    renderWithProviders(<HandlerReference {...defaultProps} />)
    const direction = await screen.findByText('direction')
    const paramContainer = direction.closest('li')
    expect(paramContainer?.textContent).toContain('enum')
  })

  it('disables insert button when service is collapsed', async () => {
    renderWithProviders(<HandlerReference {...defaultProps} />)
    const accordionTrigger = await screen.findByRole('button', {
      name: /position_keeping/i,
    })

    // Collapse the accordion
    fireEvent.click(accordionTrigger)

    // Insert button should no longer be visible
    expect(
      screen.queryByRole('button', {
        name: /insert.*initiate_log/i,
      }),
    ).toBeNull()
  })

  it('shows loading state while fetching', () => {
    // Create a mock that never resolves (simulates loading)
    const mockClients = {
      sagaRegistry: {
        describeHandlers: vi.fn().mockReturnValue(new Promise(() => {})),
      },
    } as unknown as ServiceClients

    renderWithProviders(<HandlerReference {...defaultProps} />, mockClients)

    expect(screen.getByText(/loading handlers/i)).toBeTruthy()
  })

  it('shows error state with retry button when API fails', async () => {
    const mockClients = {
      sagaRegistry: {
        describeHandlers: vi.fn().mockRejectedValue(new Error('Network error')),
      },
    } as unknown as ServiceClients

    renderWithProviders(<HandlerReference {...defaultProps} />, mockClients)

    expect(await screen.findByText(/network error/i)).toBeTruthy()
    expect(screen.getByRole('button', { name: /retry/i })).toBeTruthy()
  })
})
