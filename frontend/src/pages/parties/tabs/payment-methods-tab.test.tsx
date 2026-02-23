import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'

vi.mock('@/api/context', () => ({
  useClients: vi.fn(),
}))

import { useClients } from '@/api/context'
import { PaymentMethodsTab } from './payment-methods-tab'

function makeQueryClient() {
  return new QueryClient({
    defaultOptions: {
      queries: { retry: false, gcTime: 0, staleTime: 0 },
      mutations: { retry: false },
    },
  })
}

function renderTab(partyId = 'party-001') {
  return render(
    <QueryClientProvider client={makeQueryClient()}>
      <PaymentMethodsTab partyId={partyId} />
    </QueryClientProvider>,
  )
}

const mockPaymentMethods = [
  {
    paymentMethodId: 'pm-001',
    type: 'BANK_ACCOUNT',
    accountNumber: '12345678',
    routingNumber: '021000021',
    accountHolderName: 'Acme Corp',
    isDefault: true,
  },
  {
    paymentMethodId: 'pm-002',
    type: 'CARD',
    accountNumber: '4111111111111234',
    accountHolderName: 'John Doe',
    isDefault: false,
  },
]

describe('PaymentMethodsTab', () => {
  beforeEach(() => {
    vi.clearAllMocks()
  })

  describe('loading state', () => {
    it('renders skeletons while loading', () => {
      vi.mocked(useClients).mockReturnValue({
        party: {
          getPaymentMethods: vi.fn(() => new Promise(() => {})),
          addPaymentMethod: vi.fn(),
          removePaymentMethod: vi.fn(),
          setDefaultPaymentMethod: vi.fn(),
        },
      } as ReturnType<typeof useClients>)

      const { container } = renderTab()

      const skeletons = container.querySelectorAll('.animate-pulse')
      expect(skeletons.length).toBeGreaterThan(0)
    })
  })

  describe('empty state', () => {
    beforeEach(() => {
      vi.mocked(useClients).mockReturnValue({
        party: {
          getPaymentMethods: vi.fn().mockResolvedValue({ paymentMethods: [] }),
          addPaymentMethod: vi.fn(),
          removePaymentMethod: vi.fn(),
          setDefaultPaymentMethod: vi.fn(),
        },
      } as ReturnType<typeof useClients>)
    })

    it('renders empty state when no payment methods exist', async () => {
      renderTab()
      await waitFor(() => {
        expect(screen.getByText(/no payment methods/i)).toBeInTheDocument()
      })
    })

    it('renders Add Payment Method button even when empty', async () => {
      renderTab()
      await waitFor(() => {
        expect(screen.getByRole('button', { name: /add payment method/i })).toBeInTheDocument()
      })
    })
  })

  describe('list rendering', () => {
    beforeEach(() => {
      vi.mocked(useClients).mockReturnValue({
        party: {
          getPaymentMethods: vi.fn().mockResolvedValue({ paymentMethods: mockPaymentMethods }),
          addPaymentMethod: vi.fn(),
          removePaymentMethod: vi.fn(),
          setDefaultPaymentMethod: vi.fn(),
        },
      } as ReturnType<typeof useClients>)
    })

    it('renders all payment methods', async () => {
      renderTab()
      await waitFor(() => {
        expect(screen.getByText('Acme Corp')).toBeInTheDocument()
        expect(screen.getByText('John Doe')).toBeInTheDocument()
      })
    })

    it('shows Default badge on default payment method', async () => {
      renderTab()
      await waitFor(() => {
        expect(screen.getByText('Default')).toBeInTheDocument()
      })
    })

    it('shows masked account number', async () => {
      renderTab()
      await waitFor(() => {
        expect(screen.getByText(/••••5678/)).toBeInTheDocument()
      })
    })

    it('shows payment method type', async () => {
      renderTab()
      await waitFor(() => {
        expect(screen.getByText(/BANK_ACCOUNT/)).toBeInTheDocument()
      })
    })

    it('shows routing number when present', async () => {
      renderTab()
      await waitFor(() => {
        expect(screen.getByText(/021000021/)).toBeInTheDocument()
      })
    })

    it('renders Remove button for each payment method', async () => {
      renderTab()
      await waitFor(() => {
        const removeButtons = screen.getAllByRole('button', { name: /remove/i })
        expect(removeButtons).toHaveLength(2)
      })
    })

    it('renders Set Default button for non-default methods', async () => {
      renderTab()
      await waitFor(() => {
        const setDefaultButtons = screen.getAllByRole('button', { name: /set default/i })
        // Only the non-default method gets "Set Default"
        expect(setDefaultButtons).toHaveLength(1)
      })
    })

    it('does not render Set Default for already-default method', async () => {
      renderTab()
      await waitFor(() => {
        expect(screen.getByText('Acme Corp')).toBeInTheDocument()
      })
      // The default method (Acme Corp, pm-001) should NOT have Set Default
      const setDefaultButtons = screen.getAllByRole('button', { name: /set default/i })
      expect(setDefaultButtons).toHaveLength(1)
    })
  })

  describe('add dialog', () => {
    beforeEach(() => {
      vi.mocked(useClients).mockReturnValue({
        party: {
          getPaymentMethods: vi.fn().mockResolvedValue({ paymentMethods: [] }),
          addPaymentMethod: vi.fn().mockResolvedValue({}),
          removePaymentMethod: vi.fn(),
          setDefaultPaymentMethod: vi.fn(),
        },
      } as ReturnType<typeof useClients>)
    })

    it('opens add dialog when Add Payment Method is clicked', async () => {
      renderTab()

      await waitFor(() => {
        expect(screen.getByRole('button', { name: /add payment method/i })).toBeInTheDocument()
      })

      await userEvent.click(screen.getByRole('button', { name: /add payment method/i }))

      await waitFor(() => {
        expect(screen.getByRole('dialog')).toBeInTheDocument()
      })
    })

    it('renders form fields in the dialog', async () => {
      renderTab()

      await waitFor(() => {
        expect(screen.getByRole('button', { name: /add payment method/i })).toBeInTheDocument()
      })

      await userEvent.click(screen.getByRole('button', { name: /add payment method/i }))

      await waitFor(() => {
        expect(screen.getByRole('dialog')).toBeInTheDocument()
      })

      expect(screen.getByPlaceholderText(/account number/i)).toBeInTheDocument()
      expect(screen.getByPlaceholderText(/routing number/i)).toBeInTheDocument()
    })

    it('closes dialog when Cancel is clicked in dialog', async () => {
      renderTab()

      await waitFor(() => {
        expect(screen.getByRole('button', { name: /add payment method/i })).toBeInTheDocument()
      })

      await userEvent.click(screen.getByRole('button', { name: /add payment method/i }))

      await waitFor(() => {
        expect(screen.getByRole('dialog')).toBeInTheDocument()
      })

      await userEvent.click(screen.getByRole('button', { name: /cancel/i }))

      await waitFor(() => {
        expect(screen.queryByRole('dialog')).not.toBeInTheDocument()
      })
    })

    it('calls addPaymentMethod on form submit', async () => {
      const addPaymentMethod = vi.fn().mockResolvedValue({})
      vi.mocked(useClients).mockReturnValue({
        party: {
          getPaymentMethods: vi.fn().mockResolvedValue({ paymentMethods: [] }),
          addPaymentMethod,
          removePaymentMethod: vi.fn(),
          setDefaultPaymentMethod: vi.fn(),
        },
      } as ReturnType<typeof useClients>)

      renderTab()

      await waitFor(() => {
        expect(screen.getByRole('button', { name: /add payment method/i })).toBeInTheDocument()
      })

      await userEvent.click(screen.getByRole('button', { name: /add payment method/i }))

      await waitFor(() => {
        expect(screen.getByRole('dialog')).toBeInTheDocument()
      })

      // Select a type (required field)
      await userEvent.selectOptions(screen.getByRole('combobox'), 'BANK_ACCOUNT')
      await userEvent.type(screen.getByPlaceholderText(/account number/i), '987654321')
      await userEvent.click(screen.getByRole('button', { name: /^add$/i }))

      await waitFor(() => {
        expect(addPaymentMethod).toHaveBeenCalled()
      })
    })
  })

  describe('remove payment method', () => {
    it('calls removePaymentMethod when Remove is clicked', async () => {
      const removePaymentMethod = vi.fn().mockResolvedValue({})
      vi.mocked(useClients).mockReturnValue({
        party: {
          getPaymentMethods: vi.fn().mockResolvedValue({ paymentMethods: mockPaymentMethods }),
          addPaymentMethod: vi.fn(),
          removePaymentMethod,
          setDefaultPaymentMethod: vi.fn(),
        },
      } as ReturnType<typeof useClients>)

      renderTab()

      await waitFor(() => {
        expect(screen.getAllByRole('button', { name: /remove/i })).toHaveLength(2)
      })

      await userEvent.click(screen.getAllByRole('button', { name: /remove/i })[0])

      await waitFor(() => {
        expect(removePaymentMethod).toHaveBeenCalledWith(
          expect.objectContaining({ partyId: 'party-001' }),
        )
      })
    })
  })

  describe('set default payment method', () => {
    it('calls setDefaultPaymentMethod when Set Default is clicked', async () => {
      const setDefaultPaymentMethod = vi.fn().mockResolvedValue({})
      vi.mocked(useClients).mockReturnValue({
        party: {
          getPaymentMethods: vi.fn().mockResolvedValue({ paymentMethods: mockPaymentMethods }),
          addPaymentMethod: vi.fn(),
          removePaymentMethod: vi.fn(),
          setDefaultPaymentMethod,
        },
      } as ReturnType<typeof useClients>)

      renderTab()

      await waitFor(() => {
        expect(screen.getByRole('button', { name: /set default/i })).toBeInTheDocument()
      })

      await userEvent.click(screen.getByRole('button', { name: /set default/i }))

      await waitFor(() => {
        expect(setDefaultPaymentMethod).toHaveBeenCalledWith(
          expect.objectContaining({ partyId: 'party-001', paymentMethodId: 'pm-002' }),
        )
      })
    })
  })
})
