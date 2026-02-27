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
    id: 'pm-001',
    provider: 1,
    providerCustomerId: 'cus_acme',
    providerMethodId: 'pm_visa_1234',
    methodType: 1,
    isDefault: true,
  },
  {
    id: 'pm-002',
    provider: 1,
    providerCustomerId: 'cus_doe',
    providerMethodId: 'pm_mc_5678',
    methodType: 2,
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
          listPaymentMethods: vi.fn(() => new Promise(() => {})),
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
          listPaymentMethods: vi.fn().mockResolvedValue({ paymentMethods: [] }),
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
          listPaymentMethods: vi.fn().mockResolvedValue({ paymentMethods: mockPaymentMethods }),
          addPaymentMethod: vi.fn(),
          removePaymentMethod: vi.fn(),
          setDefaultPaymentMethod: vi.fn(),
        },
      } as ReturnType<typeof useClients>)
    })

    it('renders all payment methods', async () => {
      renderTab()
      await waitFor(() => {
        expect(screen.getByText('pm_visa_1234')).toBeInTheDocument()
        expect(screen.getByText('pm_mc_5678')).toBeInTheDocument()
      })
    })

    it('shows Default badge on default payment method', async () => {
      renderTab()
      await waitFor(() => {
        expect(screen.getByText('Default')).toBeInTheDocument()
      })
    })

    it('shows provider customer ID', async () => {
      renderTab()
      await waitFor(() => {
        expect(screen.getByText(/cus_acme/)).toBeInTheDocument()
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
        expect(setDefaultButtons).toHaveLength(1)
      })
    })

    it('does not render Set Default for already-default method', async () => {
      renderTab()
      await waitFor(() => {
        expect(screen.getByText('pm_visa_1234')).toBeInTheDocument()
      })
      const setDefaultButtons = screen.getAllByRole('button', { name: /set default/i })
      expect(setDefaultButtons).toHaveLength(1)
    })
  })

  describe('add dialog', () => {
    beforeEach(() => {
      vi.mocked(useClients).mockReturnValue({
        party: {
          listPaymentMethods: vi.fn().mockResolvedValue({ paymentMethods: [] }),
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

      expect(screen.getByPlaceholderText(/cus_/i)).toBeInTheDocument()
      expect(screen.getByPlaceholderText(/pm_/i)).toBeInTheDocument()
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
          listPaymentMethods: vi.fn().mockResolvedValue({ paymentMethods: [] }),
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

      await userEvent.type(screen.getByPlaceholderText(/cus_/i), 'cus_new')
      await userEvent.type(screen.getByPlaceholderText(/pm_/i), 'pm_new')
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
          listPaymentMethods: vi.fn().mockResolvedValue({ paymentMethods: mockPaymentMethods }),
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
          listPaymentMethods: vi.fn().mockResolvedValue({ paymentMethods: mockPaymentMethods }),
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
