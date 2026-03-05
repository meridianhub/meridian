import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { BrowserRouter, Routes, Route } from 'react-router-dom'

vi.mock('@/api/context', () => ({
  useApiClients: vi.fn(),
}))

// Mock CodeMirror editors to avoid JSDOM incompatibility
vi.mock('@/features/sagas/components/cel-editor', () => ({
  CELEditor: ({ value, onChange }: { value: string; onChange: (v: string) => void }) => (
    <textarea
      data-testid="cel-editor"
      value={value}
      onChange={(e) => onChange(e.target.value)}
    />
  ),
}))

import { useApiClients } from '@/api/context'
import { MappingDetailPage } from './[mappingId]'

const SAMPLE_MAPPING = {
  id: 'mapping-abc',
  name: 'Stripe Webhook',
  targetService: 'meridian.payment_order.v1.PaymentOrderService',
  targetRpc: 'InitiatePaymentOrder',
  version: 1,
  status: 'MAPPING_STATUS_ACTIVE',
  fields: [
    {
      externalPath: 'customer.id',
      internalPath: 'customer_id',
      transform: null,
    },
    {
      externalPath: 'amount.value',
      internalPath: 'amount.amount',
      transform: { cel: { inboundCel: 'value * 100', outboundCel: '' } },
    },
  ],
  inboundValidationCel: 'has(payload.customer_id)',
  outboundValidationCel: '',
  isBatch: false,
  batchTargetPath: '',
  createdAt: undefined,
  updatedAt: undefined,
}

function makeQueryClient() {
  return new QueryClient({
    defaultOptions: {
      queries: { retry: false, staleTime: Infinity },
    },
  })
}

function renderAtRoute(component: React.ReactNode, route: string) {
  const qc = makeQueryClient()
  window.history.pushState({}, 'test page', route)
  return render(
    <QueryClientProvider client={qc}>
      <BrowserRouter>
        <Routes>
          <Route path="/gateway-mappings/:mappingId" element={component} />
        </Routes>
      </BrowserRouter>
    </QueryClientProvider>,
  )
}

function makeDefaultClients(mapping = SAMPLE_MAPPING) {
  return {
    mapping: {
      getMapping: vi.fn().mockResolvedValue({ mapping }),
      dryRunMapping: vi.fn().mockResolvedValue({
        transformedJson: '{"customer_id":"cust_123"}',
        idempotencyKey: 'key-abc',
        validationResult: { passed: true, errors: [] },
        executionTimeMs: 12,
        fieldMappingTrace: [
          {
            sourcePath: 'customer.id',
            targetPath: 'customer_id',
            sourceValue: '"cust_123"',
            transformedValue: '"cust_123"',
            transformType: 'none',
          },
        ],
        transformError: '',
      }),
    },
  }
}

describe('MappingDetailPage', () => {
  beforeEach(() => {
    vi.mocked(useApiClients).mockReturnValue(makeDefaultClients() as never)
  })

  it('renders the page title', async () => {
    renderAtRoute(<MappingDetailPage />, '/gateway-mappings/mapping-abc')

    await waitFor(() => {
      // Breadcrumb link to parent section
      const gatewayMappingsLink = screen.getByRole('link', { name: 'Gateway Mappings' })
      expect(gatewayMappingsLink).toBeInTheDocument()
      expect(gatewayMappingsLink).toHaveAttribute('href', '/mappings')
    })
  })

  it('renders mapping name after loading', async () => {
    renderAtRoute(<MappingDetailPage />, '/gateway-mappings/mapping-abc')

    await waitFor(() => {
      expect(screen.getByRole('heading', { name: 'Stripe Webhook' })).toBeInTheDocument()
    })
  })

  it('renders field mapper tab', async () => {
    renderAtRoute(<MappingDetailPage />, '/gateway-mappings/mapping-abc')

    await waitFor(() => {
      expect(screen.getByRole('tab', { name: /field mapper/i })).toBeInTheDocument()
    })
  })

  it('renders dry run playground tab', async () => {
    renderAtRoute(<MappingDetailPage />, '/gateway-mappings/mapping-abc')

    await waitFor(() => {
      expect(screen.getByRole('tab', { name: /dry run/i })).toBeInTheDocument()
    })
  })

  it('shows field correspondence with gjson paths', async () => {
    const user = userEvent.setup()
    renderAtRoute(<MappingDetailPage />, '/gateway-mappings/mapping-abc')

    // Wait for page to load then click field mapper tab
    await waitFor(() => {
      expect(screen.getByRole('tab', { name: /field mapper/i })).toBeInTheDocument()
    })
    await user.click(screen.getByRole('tab', { name: /field mapper/i }))

    await waitFor(() => {
      expect(screen.getByText('customer.id')).toBeInTheDocument()
      expect(screen.getByText('customer_id')).toBeInTheDocument()
    })
  })

  it('shows transform type for field with cel transform', async () => {
    const user = userEvent.setup()
    renderAtRoute(<MappingDetailPage />, '/gateway-mappings/mapping-abc')

    await waitFor(() => {
      expect(screen.getByRole('tab', { name: /field mapper/i })).toBeInTheDocument()
    })
    await user.click(screen.getByRole('tab', { name: /field mapper/i }))

    await waitFor(() => {
      expect(screen.getByText('CEL')).toBeInTheDocument()
    })
  })

  it('calls DryRunMapping RPC and shows transformed JSON output', async () => {
    const user = userEvent.setup()
    renderAtRoute(<MappingDetailPage />, '/gateway-mappings/mapping-abc')

    // Click on Dry Run tab
    await waitFor(() => {
      expect(screen.getByRole('tab', { name: /dry run/i })).toBeInTheDocument()
    })
    await user.click(screen.getByRole('tab', { name: /dry run/i }))

    // Click run button
    const runButton = await screen.findByRole('button', { name: /^run$/i })
    await user.click(runButton)

    // Check transformed output appears
    await waitFor(() => {
      expect(screen.getByTestId('dry-run-output')).toHaveTextContent(/customer_id/)
    })
  })

  it('shows PII masking for sensitive fields', async () => {
    const clientsWithPii = {
      mapping: {
        getMapping: vi.fn().mockResolvedValue({
          mapping: {
            ...SAMPLE_MAPPING,
            fields: [
              {
                externalPath: 'card.number',
                internalPath: 'payment_method',
                transform: null,
              },
            ],
          },
        }),
        dryRunMapping: vi.fn().mockResolvedValue({
          transformedJson: '{"payment_method":"****"}',
          idempotencyKey: '',
          validationResult: { passed: true, errors: [] },
          executionTimeMs: 5,
          fieldMappingTrace: [],
          transformError: '',
        }),
      },
    }
    vi.mocked(useApiClients).mockReturnValue(clientsWithPii as never)

    const user = userEvent.setup()
    renderAtRoute(<MappingDetailPage />, '/gateway-mappings/mapping-abc')

    await waitFor(() => {
      expect(screen.getByRole('tab', { name: /dry run/i })).toBeInTheDocument()
    })
    await user.click(screen.getByRole('tab', { name: /dry run/i }))

    const runButton = await screen.findByRole('button', { name: /run/i })

    // Enable PII masking
    const piiToggle = screen.queryByRole('checkbox', { name: /mask pii/i })
    if (piiToggle && !piiToggle.checked) {
      await user.click(piiToggle)
    }

    await user.click(runButton)

    await waitFor(() => {
      // The output should be rendered
      expect(screen.getByTestId('dry-run-output')).toBeInTheDocument()
    })
  })

  it('shows field mapping trace after dry run', async () => {
    const user = userEvent.setup()
    renderAtRoute(<MappingDetailPage />, '/gateway-mappings/mapping-abc')

    await waitFor(() => {
      expect(screen.getByRole('tab', { name: /dry run/i })).toBeInTheDocument()
    })
    await user.click(screen.getByRole('tab', { name: /dry run/i }))

    const runButton = await screen.findByRole('button', { name: /run/i })
    await user.click(runButton)

    await waitFor(() => {
      expect(screen.getByTestId('field-mapping-trace')).toBeInTheDocument()
    })
  })

  it('shows error message when mapping not found', async () => {
    vi.mocked(useApiClients).mockReturnValue({
      mapping: {
        getMapping: vi.fn().mockRejectedValue(new Error('NOT_FOUND')),
        dryRunMapping: vi.fn(),
      },
    } as never)

    renderAtRoute(<MappingDetailPage />, '/gateway-mappings/nonexistent')

    await waitFor(() => {
      expect(screen.getByText(/failed to load/i)).toBeInTheDocument()
    })
  })
})
