import { describe, it, expect, vi } from 'vitest'
import { render, screen } from '@testing-library/react'
import { ExecutionContextTab } from './execution-context-tab'
import type { ManifestGraph } from '@/features/manifests/lib/manifest-graph-model'

vi.mock('@/features/manifests/hooks/use-manifest-graph', () => ({
  useManifestGraph: vi.fn(),
}))

vi.mock('@/features/manifests/hooks/use-event-chain', () => ({
  useEventChain: vi.fn(() => null),
}))

vi.mock('@/features/manifests/components/event-chain-panel', () => ({
  EventChainPanel: ({ startNodeLabel }: { startNodeLabel: string }) => (
    <div data-testid="event-chain-panel">Event chain for {startNodeLabel}</div>
  ),
}))

import { useManifestGraph } from '@/features/manifests/hooks/use-manifest-graph'
import { useEventChain } from '@/features/manifests/hooks/use-event-chain'

const mockedUseManifestGraph = vi.mocked(useManifestGraph)
const mockedUseEventChain = vi.mocked(useEventChain)

function buildTestGraph(overrides?: Partial<ManifestGraph>): ManifestGraph {
  return {
    nodes: [
      { id: 'instrument:GBP', type: 'instrument', label: 'British Pound', data: { code: 'GBP' } },
      { id: 'account_type:CUSTOMER', type: 'account_type', label: 'Customer Account', data: { code: 'CUSTOMER' } },
      { id: 'saga:payment_saga', type: 'saga', label: 'payment_saga', data: { name: 'payment_saga', trigger: 'event:tx' } },
      { id: 'valuation_rule:GBP:USD:0', type: 'valuation_rule', label: 'GBP -> USD', data: { fromInstrument: 'GBP', toInstrument: 'USD' } },
    ],
    edges: [
      { id: 'writes_to:saga:payment_saga:CUSTOMER', source: 'saga:payment_saga', target: 'account_type:CUSTOMER', relationship: 'writes_to' },
      { id: 'allowed_by:CUSTOMER:GBP', source: 'account_type:CUSTOMER', target: 'instrument:GBP', relationship: 'allowed_by' },
      { id: 'converts_from:GBP:USD:0', source: 'valuation_rule:GBP:USD:0', target: 'instrument:GBP', relationship: 'converts_from' },
    ],
    ...overrides,
  }
}

describe('ExecutionContextTab', () => {
  it('shows loading state when graph is loading', () => {
    mockedUseManifestGraph.mockReturnValue({ graph: null, isLoading: true, error: null })

    render(<ExecutionContextTab entityType="instrument" entityCode="GBP" />)

    expect(screen.getByText('Loading execution context...')).toBeInTheDocument()
  })

  it('shows error state when graph fails to load', () => {
    mockedUseManifestGraph.mockReturnValue({ graph: null, isLoading: false, error: new Error('fetch failed') })

    render(<ExecutionContextTab entityType="instrument" entityCode="GBP" />)

    expect(screen.getByText(/Failed to load manifest/)).toBeInTheDocument()
  })

  it('shows empty state when no related sagas found', () => {
    const graph = buildTestGraph({
      edges: [],
    })
    mockedUseManifestGraph.mockReturnValue({ graph, isLoading: false, error: null })

    render(<ExecutionContextTab entityType="instrument" entityCode="GBP" />)

    expect(screen.getByText('No related sagas found.')).toBeInTheDocument()
  })

  it('renders related sagas for an instrument', () => {
    const graph = buildTestGraph()
    mockedUseManifestGraph.mockReturnValue({ graph, isLoading: false, error: null })

    render(<ExecutionContextTab entityType="instrument" entityCode="GBP" />)

    expect(screen.getByText('Related Sagas')).toBeInTheDocument()
    expect(screen.getByText('payment_saga')).toBeInTheDocument()
  })

  it('renders related sagas for an account type via writes_to edges', () => {
    const graph = buildTestGraph()
    mockedUseManifestGraph.mockReturnValue({ graph, isLoading: false, error: null })

    render(<ExecutionContextTab entityType="account_type" entityCode="CUSTOMER" />)

    expect(screen.getByText('Related Sagas')).toBeInTheDocument()
    expect(screen.getByText('payment_saga')).toBeInTheDocument()
  })

  it('renders valuation rules section for instruments', () => {
    const graph = buildTestGraph()
    mockedUseManifestGraph.mockReturnValue({ graph, isLoading: false, error: null })

    render(<ExecutionContextTab entityType="instrument" entityCode="GBP" />)

    expect(screen.getByText('Valuation Rules')).toBeInTheDocument()
    expect(screen.getByText('GBP -> USD')).toBeInTheDocument()
  })

  it('does not render valuation rules section for account types', () => {
    const graph = buildTestGraph()
    mockedUseManifestGraph.mockReturnValue({ graph, isLoading: false, error: null })

    render(<ExecutionContextTab entityType="account_type" entityCode="CUSTOMER" />)

    expect(screen.queryByText('Valuation Rules')).not.toBeInTheDocument()
  })

  it('renders event chain panel when event chain is available', () => {
    const graph = buildTestGraph()
    mockedUseManifestGraph.mockReturnValue({ graph, isLoading: false, error: null })
    mockedUseEventChain.mockReturnValue({
      hops: [],
      terminationReason: 'no_matching_sagas',
      maxDepthUsed: 0,
    })

    render(<ExecutionContextTab entityType="instrument" entityCode="GBP" />)

    expect(screen.getByText('Event Chain')).toBeInTheDocument()
    expect(screen.getByTestId('event-chain-panel')).toBeInTheDocument()
  })

  it('does not render event chain section when chain is null', () => {
    const graph = buildTestGraph()
    mockedUseManifestGraph.mockReturnValue({ graph, isLoading: false, error: null })
    mockedUseEventChain.mockReturnValue(null)

    render(<ExecutionContextTab entityType="instrument" entityCode="GBP" />)

    expect(screen.queryByTestId('event-chain-panel')).not.toBeInTheDocument()
  })
})
