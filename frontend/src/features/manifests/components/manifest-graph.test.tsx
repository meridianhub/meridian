import { describe, it, expect, vi } from 'vitest'
import { useState } from 'react'
import { render, screen, fireEvent, waitFor } from '@testing-library/react'
import { MemoryRouter } from 'react-router-dom'
import { ManifestGraph } from './manifest-graph'
import type { Manifest } from '@/api/gen/meridian/control_plane/v1/manifest_pb'

vi.mock('@/api/transport', () => ({
  createTenantTransport: vi.fn(() => ({ __type: 'mock-transport' })),
}))

vi.mock('@/api/clients', () => ({
  createServiceClients: vi.fn(() => ({})),
}))

vi.mock('@/features/economy/components/apply-resource-modal', () => ({
  ApplyResourceModal: () => null,
}))

vi.mock('@/components/ui/tooltip', () => ({
  TooltipProvider: ({ children }: { children: React.ReactNode }) => <>{children}</>,
  Tooltip: ({ children }: { children: React.ReactNode }) => <>{children}</>,
  TooltipTrigger: ({ children }: { children: React.ReactNode }) => <>{children}</>,
  TooltipContent: ({ children }: { children: React.ReactNode }) => (
    <div data-testid="tooltip-content">{children}</div>
  ),
}))

vi.mock('@xyflow/react', () => {
  const Position = { Top: 'top', Bottom: 'bottom', Left: 'left', Right: 'right' }
  const BackgroundVariant = { Dots: 'dots' }

  function Handle() { return null }

  function ReactFlow({ nodes, edges, onNodeClick, onNodeDoubleClick, onPaneClick, children }: {
    nodes: { id: string; type?: string; data: Record<string, unknown> }[]
    edges: { id: string; source: string; target: string; data?: Record<string, unknown> }[]
    onNodeClick?: (event: unknown, node: unknown) => void
    onNodeDoubleClick?: (event: unknown, node: unknown) => void
    onPaneClick?: () => void
    children?: React.ReactNode
    [key: string]: unknown
  }) {
    return (
      <div data-testid="react-flow" data-node-count={nodes.length} data-edge-count={edges.length} onClick={(e) => {
        if ((e.target as HTMLElement).getAttribute('data-testid') === 'react-flow') onPaneClick?.()
      }}>
        {nodes.map((n) => {
          const mn = (n.data as { manifestNode?: { type: string; label: string; data: Record<string, unknown> } }).manifestNode
          return (
            <div
              key={n.id}
              data-testid={`node-${n.id}`}
              data-node-type={n.type}
              data-dimmed={String((n.data as { dimmed?: boolean }).dimmed ?? false)}
              data-highlighted={String((n.data as { highlighted?: boolean }).highlighted ?? false)}
              onClick={(e) => { e.stopPropagation(); onNodeClick?.({}, n) }}
              onDoubleClick={() => onNodeDoubleClick?.({}, n)}
            >
              {mn?.label ?? n.id}
            </div>
          )
        })}
        {edges.map((e) => (
          <div key={e.id} data-testid={`edge-${e.id}`} data-relationship={e.data?.relationship}>
            {e.source} -&gt; {e.target}
          </div>
        ))}
        {children}
      </div>
    )
  }

  function Controls() { return <div data-testid="controls" /> }
  function Background() { return null }
  function MiniMap() { return <div data-testid="minimap" /> }

  return {
    ReactFlow,
    Controls,
    Background,
    MiniMap,
    Handle,
    Position,
    BackgroundVariant,
    useNodesState: (init: unknown[]) => {
      const [s, setS] = useState(init)
      return [s, setS, vi.fn()]
    },
    useEdgesState: (init: unknown[]) => {
      const [s, setS] = useState(init)
      return [s, setS, vi.fn()]
    },
  }
})

vi.mock('elkjs/lib/elk.bundled.js', () => ({
  default: class MockELK {
    async layout(graph: { children: { id: string }[] }) {
      return {
        children: graph.children.map((child, i) => ({
          id: child.id,
          x: i * 100,
          y: i * 100,
        })),
      }
    }
  },
}))

const mockNavigate = vi.fn()
vi.mock('react-router-dom', async () => {
  const actual = await vi.importActual('react-router-dom')
  return {
    ...actual,
    useNavigate: () => mockNavigate,
  }
})

function createMockManifest(overrides: Partial<Record<string, unknown>> = {}): Manifest {
  return {
    version: '1.0',
    metadata: { name: 'Test Manifest', industry: 'energy', description: '' },
    instruments: [],
    accountTypes: [],
    valuationRules: [],
    sagas: [],
    seedData: undefined,
    paymentRails: [],
    partyTypes: [],
    mappings: [],
    operationalGateway: undefined,
    ...overrides,
  } as unknown as Manifest
}

const energyManifest = createMockManifest({
  instruments: [
    { code: 'KWH', name: 'Kilowatt Hour', type: 2, dimensions: { unit: 'kWh', precision: 4 } },
    { code: 'GBP', name: 'British Pound', type: 1, dimensions: { unit: 'GBP', precision: 2 } },
  ],
  accountTypes: [
    {
      code: 'ENERGY_HOLDING',
      name: 'Energy Holding',
      normalBalance: 1,
      allowedInstruments: ['KWH'],
    },
  ],
  valuationRules: [
    { fromInstrument: 'KWH', toInstrument: 'GBP', method: 1, source: 'nordpool_spot' },
  ],
  sagas: [
    {
      name: 'usage_to_value',
      trigger: 'event:position-keeping.transaction-captured.v1',
      filter: 'event.instrument_code == "KWH"',
      script: 'def main(): pass',
    },
    {
      name: 'daily_reconciliation',
      trigger: 'scheduled:daily_reconciliation',
      script: 'def main(): pass',
    },
  ],
})

function renderGraph(manifest: Manifest) {
  return render(
    <MemoryRouter>
      <ManifestGraph manifest={manifest} className="h-96" />
    </MemoryRouter>,
  )
}

describe('ManifestGraph', () => {
  beforeEach(() => {
    mockNavigate.mockClear()
    localStorage.clear()
  })

  describe('rendering', () => {
    it('renders ReactFlow container with nodes', async () => {
      renderGraph(energyManifest)
      const flow = await screen.findByTestId('react-flow')
      expect(flow).toBeInTheDocument()
      expect(flow).toHaveAttribute('data-node-count', '7')
    })

    it('renders controls and minimap', async () => {
      renderGraph(energyManifest)
      expect(await screen.findByTestId('controls')).toBeInTheDocument()
      expect(await screen.findByTestId('minimap')).toBeInTheDocument()
    })

    it('renders instrument nodes with code and name', async () => {
      renderGraph(energyManifest)
      expect(await screen.findByText('Kilowatt Hour')).toBeInTheDocument()
      expect(screen.getByText('British Pound')).toBeInTheDocument()
    })

    it('renders saga nodes', async () => {
      renderGraph(energyManifest)
      expect(await screen.findByText('usage_to_value')).toBeInTheDocument()
      expect(screen.getByText('daily_reconciliation')).toBeInTheDocument()
    })

    it('renders correct node types', async () => {
      renderGraph(energyManifest)
      const instrumentNode = await screen.findByTestId('node-instrument:KWH')
      expect(instrumentNode).toHaveAttribute('data-node-type', 'instrument')

      const sagaNode = screen.getByTestId('node-saga:usage_to_value')
      expect(sagaNode).toHaveAttribute('data-node-type', 'saga')
    })
  })

  describe('empty state', () => {
    it('shows empty state for empty manifest', () => {
      const emptyManifest = createMockManifest()
      renderGraph(emptyManifest)
      expect(screen.getByTestId('manifest-graph-empty')).toBeInTheDocument()
      expect(screen.getByText('No elements in manifest to visualize.')).toBeInTheDocument()
    })
  })

  describe('filter sidebar', () => {
    it('renders type filter checkboxes', async () => {
      renderGraph(energyManifest)
      expect(await screen.findByLabelText('Show Instruments')).toBeInTheDocument()
      expect(screen.getByLabelText('Show Account Types')).toBeInTheDocument()
      expect(screen.getByLabelText('Show Valuation Rules')).toBeInTheDocument()
      expect(screen.getByLabelText('Show Sagas')).toBeInTheDocument()
      expect(screen.getByLabelText('Show Payment Rails')).toBeInTheDocument()
      expect(screen.getByLabelText('Show Party Types')).toBeInTheDocument()
      expect(screen.getByLabelText('Show Mappings')).toBeInTheDocument()
      expect(screen.getByLabelText('Show Operational Gateway')).toBeInTheDocument()
      expect(screen.getByLabelText('Show Provider Connections')).toBeInTheDocument()
      expect(screen.getByLabelText('Show Instruction Routes')).toBeInTheDocument()
      expect(screen.getByLabelText('Show Market Data')).toBeInTheDocument()
      expect(screen.getByLabelText('Show Organizations')).toBeInTheDocument()
      expect(screen.getByLabelText('Show Internal Accounts')).toBeInTheDocument()
    })

    it('shows node counts per type', async () => {
      renderGraph(energyManifest)
      // 2 instruments, 1 account type, 1 valuation rule, 2 sagas, 1 event channel
      const counts = await screen.findAllByText('(2)')
      expect(counts).toHaveLength(2) // instruments and sagas both have 2
      const ones = screen.getAllByText('(1)')
      expect(ones).toHaveLength(3) // account type, valuation rule, and event channel each have 1
    })

    it('shows total visible count', async () => {
      renderGraph(energyManifest)
      expect(await screen.findByText('7 nodes visible')).toBeInTheDocument()
    })

    it('unchecking a type filters nodes', async () => {
      renderGraph(energyManifest)
      const sagaCheckbox = await screen.findByLabelText('Show Sagas')
      fireEvent.click(sagaCheckbox)
      // After unchecking sagas, saga nodes should be removed from the flow
      const flow = await screen.findByTestId('react-flow')
      // The node count should decrease (7 - 2 sagas = 5)
      expect(flow).toHaveAttribute('data-node-count', '5')
    })

    it('renders Show All and None buttons', async () => {
      renderGraph(energyManifest)
      expect(await screen.findByLabelText('Show all types')).toBeInTheDocument()
      expect(screen.getByLabelText('Hide all types')).toBeInTheDocument()
    })

    it('None button hides all nodes', async () => {
      renderGraph(energyManifest)
      const noneButton = await screen.findByLabelText('Hide all types')
      fireEvent.click(noneButton)
      const flow = await screen.findByTestId('react-flow')
      await waitFor(() => expect(flow).toHaveAttribute('data-node-count', '0'))
    })

    it('All button restores all nodes after hiding', async () => {
      renderGraph(energyManifest)
      const noneButton = await screen.findByLabelText('Hide all types')
      fireEvent.click(noneButton)
      const allButton = screen.getByLabelText('Show all types')
      fireEvent.click(allButton)
      const flow = await screen.findByTestId('react-flow')
      await waitFor(() => expect(flow).toHaveAttribute('data-node-count', '7'))
    })

    it('persists visible types to localStorage when toggling', async () => {
      renderGraph(energyManifest)
      const sagaCheckbox = await screen.findByLabelText('Show Sagas')
      fireEvent.click(sagaCheckbox)
      const stored = localStorage.getItem('meridian:graph-visible-types')
      expect(stored).not.toBeNull()
      const parsed: unknown = JSON.parse(stored!)
      expect(Array.isArray(parsed)).toBe(true)
      expect((parsed as string[]).includes('saga')).toBe(false)
    })

    it('restores visible types from localStorage on mount', async () => {
      // Pre-populate localStorage with sagas hidden
      const types = ['instrument', 'account_type', 'valuation_rule']
      localStorage.setItem('meridian:graph-visible-types', JSON.stringify(types))
      renderGraph(energyManifest)
      const flow = await screen.findByTestId('react-flow')
      // Only instrument, account_type, valuation_rule nodes visible (2 + 1 + 1 = 4)
      expect(flow).toHaveAttribute('data-node-count', '4')
    })

    it('restores empty (hide all) state from localStorage on mount', async () => {
      localStorage.setItem('meridian:graph-visible-types', JSON.stringify([]))
      renderGraph(energyManifest)
      const flow = await screen.findByTestId('react-flow')
      await waitFor(() => expect(flow).toHaveAttribute('data-node-count', '0'))
    })
  })

  describe('legend', () => {
    it('renders edge type legend', async () => {
      renderGraph(energyManifest)
      expect(await screen.findByText('Allowed by')).toBeInTheDocument()
      expect(screen.getByText('Converts from')).toBeInTheDocument()
      expect(screen.getByText('Converts to')).toBeInTheDocument()
    })
  })

  describe('event channel nodes', () => {
    it('renders event_channel nodes for event-triggered sagas', async () => {
      renderGraph(energyManifest)
      const node = await screen.findByTestId('node-event_channel:position-keeping.transaction-captured.v1')
      expect(node).toBeInTheDocument()
      expect(node).toHaveAttribute('data-node-type', 'event_channel')
    })

    it('displays channel name as node label', async () => {
      renderGraph(energyManifest)
      await screen.findByTestId('node-event_channel:position-keeping.transaction-captured.v1')
      expect(screen.getByText('position-keeping.transaction-captured.v1')).toBeInTheDocument()
    })

    it('renders event channel filter checkbox', async () => {
      renderGraph(energyManifest)
      expect(await screen.findByLabelText('Show Event Channels')).toBeInTheDocument()
    })

    it('hides event channel nodes when filter unchecked', async () => {
      renderGraph(energyManifest)
      const checkbox = await screen.findByLabelText('Show Event Channels')
      fireEvent.click(checkbox)
      const flow = await screen.findByTestId('react-flow')
      // 7 total - 1 event channel = 6
      expect(flow).toHaveAttribute('data-node-count', '6')
    })
  })

  describe('double-click navigation', () => {
    it('navigates to instruments page on instrument double-click', async () => {
      renderGraph(energyManifest)
      const node = await screen.findByTestId('node-instrument:KWH')
      fireEvent.doubleClick(node)
      expect(mockNavigate).toHaveBeenCalledWith('/reference-data/instruments')
    })

    it('navigates to account types page on account type double-click', async () => {
      renderGraph(energyManifest)
      const node = await screen.findByTestId('node-account_type:ENERGY_HOLDING')
      fireEvent.doubleClick(node)
      expect(mockNavigate).toHaveBeenCalledWith('/reference-data/account-types')
    })

    it('navigates to saga detail page on saga double-click', async () => {
      renderGraph(energyManifest)
      const node = await screen.findByTestId('node-saga:usage_to_value')
      fireEvent.doubleClick(node)
      expect(mockNavigate).toHaveBeenCalledWith('/starlark-config/usage_to_value')
    })
  })

  describe('node selection', () => {
    it('shows toolbar when an instrument node is clicked', async () => {
      renderGraph(energyManifest)
      const node = await screen.findByTestId('node-instrument:KWH')
      fireEvent.click(node)
      const toolbar = await screen.findByTestId('node-toolbar')
      expect(toolbar).toBeInTheDocument()
      expect(toolbar.textContent).toContain('Kilowatt Hour')
    })

    it('shows "Show Event Chain" button for instrument nodes', async () => {
      renderGraph(energyManifest)
      const node = await screen.findByTestId('node-instrument:KWH')
      fireEvent.click(node)
      expect(await screen.findByTestId('show-event-chain-button')).toBeInTheDocument()
    })

    it('shows "Show Event Chain" button for account_type nodes', async () => {
      renderGraph(energyManifest)
      const node = await screen.findByTestId('node-account_type:ENERGY_HOLDING')
      fireEvent.click(node)
      expect(await screen.findByTestId('show-event-chain-button')).toBeInTheDocument()
    })

    it('does not show "Show Event Chain" button for saga nodes', async () => {
      renderGraph(energyManifest)
      const node = await screen.findByTestId('node-saga:usage_to_value')
      fireEvent.click(node)
      expect(await screen.findByTestId('node-toolbar')).toBeInTheDocument()
      expect(screen.queryByTestId('show-event-chain-button')).not.toBeInTheDocument()
    })

    it('deselects node when clicking the same node again', async () => {
      renderGraph(energyManifest)
      const node = await screen.findByTestId('node-instrument:KWH')
      fireEvent.click(node)
      expect(await screen.findByTestId('node-toolbar')).toBeInTheDocument()
      fireEvent.click(node)
      expect(screen.queryByTestId('node-toolbar')).not.toBeInTheDocument()
    })

    it('deselects node when clicking the pane', async () => {
      renderGraph(energyManifest)
      const node = await screen.findByTestId('node-instrument:KWH')
      fireEvent.click(node)
      expect(await screen.findByTestId('node-toolbar')).toBeInTheDocument()
      const pane = screen.getByTestId('react-flow')
      fireEvent.click(pane)
      expect(screen.queryByTestId('node-toolbar')).not.toBeInTheDocument()
    })
  })

  describe('event chain panel', () => {
    it('opens event chain panel when "Show Event Chain" is clicked', async () => {
      renderGraph(energyManifest)
      const node = await screen.findByTestId('node-instrument:KWH')
      fireEvent.click(node)
      const button = await screen.findByTestId('show-event-chain-button')
      fireEvent.click(button)
      expect(await screen.findByTestId('event-chain-side-panel')).toBeInTheDocument()
      expect(screen.getByTestId('event-chain-panel')).toBeInTheDocument()
    })

    it('closes event chain panel when close button is clicked', async () => {
      renderGraph(energyManifest)
      const node = await screen.findByTestId('node-instrument:KWH')
      fireEvent.click(node)
      const button = await screen.findByTestId('show-event-chain-button')
      fireEvent.click(button)
      expect(await screen.findByTestId('event-chain-side-panel')).toBeInTheDocument()
      const closeButton = screen.getByTestId('close-event-chain-panel')
      fireEvent.click(closeButton)
      expect(screen.queryByTestId('event-chain-side-panel')).not.toBeInTheDocument()
    })
  })
})
