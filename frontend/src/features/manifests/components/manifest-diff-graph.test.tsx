import { describe, it, expect, vi } from 'vitest'
import { useState } from 'react'
import { render, screen } from '@testing-library/react'
import { ManifestDiffGraph } from './manifest-diff-graph'
import type { ManifestGraph } from '../lib/manifest-graph-model'

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

  function ReactFlow({ nodes, edges, children }: {
    nodes: { id: string; type?: string; data: Record<string, unknown> }[]
    edges: { id: string; source: string; target: string; data?: Record<string, unknown> }[]
    children?: React.ReactNode
    [key: string]: unknown
  }) {
    return (
      <div data-testid="react-flow" data-node-count={nodes.length} data-edge-count={edges.length}>
        {nodes.map((n) => (
          <div
            key={n.id}
            data-testid={`diff-flow-node-${n.id}`}
            data-node-type={n.type}
            data-diff-status={(n.data as { diffStatus?: string }).diffStatus}
          >
            {n.id}
          </div>
        ))}
        {edges.map((e) => (
          <div key={e.id} data-testid={`diff-flow-edge-${e.id}`} data-diff-status={(e.data as { diffStatus?: string })?.diffStatus}>
            {e.source} -&gt; {e.target}
          </div>
        ))}
        {children}
      </div>
    )
  }

  function Controls() { return <div data-testid="controls" /> }
  function Background() { return null }

  return {
    ReactFlow,
    Controls,
    Background,
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

const emptyGraph: ManifestGraph = { nodes: [], edges: [] }

const graphWithInstrument: ManifestGraph = {
  nodes: [
    {
      id: 'instrument:GBP',
      type: 'instrument',
      label: 'British Pound',
      data: { code: 'GBP', dimensions: { unit: 'GBP' } },
    },
  ],
  edges: [],
}

const graphWithTwo: ManifestGraph = {
  nodes: [
    {
      id: 'instrument:GBP',
      type: 'instrument',
      label: 'British Pound',
      data: { code: 'GBP', dimensions: { unit: 'GBP' } },
    },
    {
      id: 'instrument:KWH',
      type: 'instrument',
      label: 'Kilowatt Hour',
      data: { code: 'KWH', dimensions: { unit: 'kWh' } },
    },
  ],
  edges: [],
}

describe('ManifestDiffGraph', () => {
  describe('no-diff state', () => {
    it('shows no-changes message when before and after are identical', () => {
      render(<ManifestDiffGraph before={graphWithInstrument} after={graphWithInstrument} />)
      expect(screen.getByTestId('manifest-diff-no-changes')).toBeInTheDocument()
      expect(screen.getByText('No differences between versions.')).toBeInTheDocument()
    })

    it('shows no-changes for two empty graphs', () => {
      render(<ManifestDiffGraph before={emptyGraph} after={emptyGraph} />)
      expect(screen.getByTestId('manifest-diff-no-changes')).toBeInTheDocument()
    })
  })

  describe('graph rendering with changes', () => {
    it('renders ReactFlow when nodes differ', async () => {
      render(<ManifestDiffGraph before={emptyGraph} after={graphWithInstrument} />)
      expect(await screen.findByTestId('react-flow')).toBeInTheDocument()
    })

    it('renders diff-summary panel', async () => {
      render(<ManifestDiffGraph before={emptyGraph} after={graphWithInstrument} />)
      expect(await screen.findByTestId('diff-summary')).toBeInTheDocument()
    })

    it('shows added count when node is new', async () => {
      render(<ManifestDiffGraph before={emptyGraph} after={graphWithInstrument} />)
      expect(await screen.findByText('+1 added')).toBeInTheDocument()
    })

    it('shows removed count when node is removed', async () => {
      render(<ManifestDiffGraph before={graphWithInstrument} after={emptyGraph} />)
      expect(await screen.findByText('-1 removed')).toBeInTheDocument()
    })

    it('renders node with added diff status', async () => {
      render(<ManifestDiffGraph before={emptyGraph} after={graphWithInstrument} />)
      const node = await screen.findByTestId('diff-flow-node-instrument:GBP')
      expect(node).toHaveAttribute('data-diff-status', 'added')
    })

    it('renders node with removed diff status', async () => {
      render(<ManifestDiffGraph before={graphWithInstrument} after={emptyGraph} />)
      const node = await screen.findByTestId('diff-flow-node-instrument:GBP')
      expect(node).toHaveAttribute('data-diff-status', 'removed')
    })

    it('shows no-changes state (not ReactFlow) when before and after are identical', async () => {
      render(<ManifestDiffGraph before={graphWithInstrument} after={graphWithInstrument} />)
      expect(screen.getByTestId('manifest-diff-no-changes')).toBeInTheDocument()
      expect(screen.queryByTestId('react-flow')).not.toBeInTheDocument()
    })

    it('renders multiple added nodes', async () => {
      render(<ManifestDiffGraph before={emptyGraph} after={graphWithTwo} />)
      const flow = await screen.findByTestId('react-flow')
      expect(flow).toHaveAttribute('data-node-count', '2')
    })

    it('assigns diff_instrument node type', async () => {
      render(<ManifestDiffGraph before={emptyGraph} after={graphWithInstrument} />)
      const node = await screen.findByTestId('diff-flow-node-instrument:GBP')
      expect(node).toHaveAttribute('data-node-type', 'diff_instrument')
    })

    it('accepts className prop', async () => {
      const { container } = render(
        <ManifestDiffGraph before={emptyGraph} after={graphWithInstrument} className="h-96" />,
      )
      await screen.findByTestId('react-flow')
      const wrapper = container.querySelector('[data-testid="manifest-diff-graph"]')
      expect(wrapper).toHaveClass('h-96')
    })
  })

  describe('modified nodes', () => {
    const graphBefore: ManifestGraph = {
      nodes: [
        {
          id: 'instrument:GBP',
          type: 'instrument',
          label: 'British Pound',
          data: { code: 'GBP', dimensions: { unit: 'GBP', precision: 2 } },
        },
      ],
      edges: [],
    }

    const graphAfterModified: ManifestGraph = {
      nodes: [
        {
          id: 'instrument:GBP',
          type: 'instrument',
          label: 'British Pound Sterling',
          data: { code: 'GBP', dimensions: { unit: 'GBP', precision: 4 } },
        },
      ],
      edges: [],
    }

    it('shows modified count when node data changes', async () => {
      render(<ManifestDiffGraph before={graphBefore} after={graphAfterModified} />)
      expect(await screen.findByText('~1 modified')).toBeInTheDocument()
    })

    it('renders node with modified diff status', async () => {
      render(<ManifestDiffGraph before={graphBefore} after={graphAfterModified} />)
      const node = await screen.findByTestId('diff-flow-node-instrument:GBP')
      expect(node).toHaveAttribute('data-diff-status', 'modified')
    })
  })

  describe('edge diff visualization', () => {
    const beforeWithEdge: ManifestGraph = {
      nodes: [
        { id: 'instrument:GBP', type: 'instrument', label: 'GBP', data: { code: 'GBP' } },
        { id: 'account_type:CURRENT', type: 'account_type', label: 'Current', data: { code: 'CURRENT' } },
      ],
      edges: [
        { id: 'edge-1', source: 'account_type:CURRENT', target: 'instrument:GBP', relationship: 'allowed_by' },
      ],
    }

    const afterNoEdge: ManifestGraph = {
      nodes: [
        { id: 'instrument:GBP', type: 'instrument', label: 'GBP', data: { code: 'GBP' } },
        { id: 'account_type:CURRENT', type: 'account_type', label: 'Current', data: { code: 'CURRENT' } },
      ],
      edges: [],
    }

    const afterNewEdge: ManifestGraph = {
      nodes: [
        { id: 'instrument:GBP', type: 'instrument', label: 'GBP', data: { code: 'GBP' } },
        { id: 'account_type:CURRENT', type: 'account_type', label: 'Current', data: { code: 'CURRENT' } },
      ],
      edges: [
        { id: 'edge-new', source: 'account_type:CURRENT', target: 'instrument:GBP', relationship: 'allowed_by' },
      ],
    }

    it('shows removed edge count', async () => {
      render(<ManifestDiffGraph before={beforeWithEdge} after={afterNoEdge} />)
      expect(await screen.findByText('-1 edge')).toBeInTheDocument()
    })

    it('renders removed edge with removed diff status', async () => {
      render(<ManifestDiffGraph before={beforeWithEdge} after={afterNoEdge} />)
      const edge = await screen.findByTestId('diff-flow-edge-edge-1')
      expect(edge).toHaveAttribute('data-diff-status', 'removed')
    })

    it('shows added edge count', async () => {
      render(<ManifestDiffGraph before={afterNoEdge} after={afterNewEdge} />)
      expect(await screen.findByText('+1 edge')).toBeInTheDocument()
    })

    it('renders added edge with added diff status', async () => {
      render(<ManifestDiffGraph before={afterNoEdge} after={afterNewEdge} />)
      const edge = await screen.findByTestId('diff-flow-edge-edge-new')
      expect(edge).toHaveAttribute('data-diff-status', 'added')
    })

    it('pluralizes edge count correctly', async () => {
      const afterTwoEdges: ManifestGraph = {
        nodes: [
          { id: 'instrument:GBP', type: 'instrument', label: 'GBP', data: { code: 'GBP' } },
          { id: 'instrument:USD', type: 'instrument', label: 'USD', data: { code: 'USD' } },
          { id: 'account_type:CURRENT', type: 'account_type', label: 'Current', data: { code: 'CURRENT' } },
        ],
        edges: [
          { id: 'edge-a', source: 'account_type:CURRENT', target: 'instrument:GBP', relationship: 'allowed_by' },
          { id: 'edge-b', source: 'account_type:CURRENT', target: 'instrument:USD', relationship: 'allowed_by' },
        ],
      }
      const noEdges: ManifestGraph = {
        ...afterTwoEdges,
        edges: [],
      }
      render(<ManifestDiffGraph before={afterTwoEdges} after={noEdges} />)
      expect(await screen.findByText('-2 edges')).toBeInTheDocument()
    })
  })

  describe('diff legend', () => {
    it('renders legend with all diff statuses', async () => {
      render(<ManifestDiffGraph before={emptyGraph} after={graphWithInstrument} />)
      await screen.findByTestId('react-flow')
      expect(screen.getByText('Added')).toBeInTheDocument()
      expect(screen.getByText('Removed')).toBeInTheDocument()
      expect(screen.getByText('Modified')).toBeInTheDocument()
      expect(screen.getByText('Unchanged')).toBeInTheDocument()
    })

    it('renders legend header', async () => {
      render(<ManifestDiffGraph before={emptyGraph} after={graphWithInstrument} />)
      await screen.findByTestId('react-flow')
      expect(screen.getByText('Legend')).toBeInTheDocument()
    })
  })

  describe('diff summary panel', () => {
    it('renders Changes header', async () => {
      render(<ManifestDiffGraph before={emptyGraph} after={graphWithInstrument} />)
      expect(await screen.findByText('Changes')).toBeInTheDocument()
    })

    it('does not show 0-count categories', async () => {
      render(<ManifestDiffGraph before={emptyGraph} after={graphWithInstrument} />)
      await screen.findByTestId('diff-summary')
      // Only added nodes, no removed/modified nodes or edge changes
      expect(screen.queryByText(/-\d+ removed/)).not.toBeInTheDocument()
      expect(screen.queryByText(/~\d+ modified/)).not.toBeInTheDocument()
      expect(screen.queryByText(/\+\d+ edge/)).not.toBeInTheDocument()
      expect(screen.queryByText(/-\d+ edge/)).not.toBeInTheDocument()
    })
  })

  describe('controls', () => {
    it('renders zoom controls', async () => {
      render(<ManifestDiffGraph before={emptyGraph} after={graphWithInstrument} />)
      expect(await screen.findByTestId('controls')).toBeInTheDocument()
    })
  })

  describe('multiple node types in diff', () => {
    const graphWithSaga: ManifestGraph = {
      nodes: [
        {
          id: 'saga:settle',
          type: 'saga',
          label: 'settle',
          data: { trigger: 'event:test.v1', script: 'def main(): pass' },
        },
      ],
      edges: [],
    }

    const graphWithAccountType: ManifestGraph = {
      nodes: [
        {
          id: 'account_type:CURRENT',
          type: 'account_type',
          label: 'Current Account',
          data: { code: 'CURRENT' },
        },
      ],
      edges: [],
    }

    const graphWithValuation: ManifestGraph = {
      nodes: [
        {
          id: 'valuation_rule:KWH->GBP',
          type: 'valuation_rule',
          label: 'KWH to GBP',
          data: { fromInstrument: 'KWH', toInstrument: 'GBP' },
        },
      ],
      edges: [],
    }

    const graphWithPaymentRail: ManifestGraph = {
      nodes: [
        {
          id: 'payment_rail:stripe',
          type: 'payment_rail',
          label: 'Stripe',
          data: { provider: 'stripe' },
        },
      ],
      edges: [],
    }

    const graphWithMapping: ManifestGraph = {
      nodes: [
        {
          id: 'mapping:inbound',
          type: 'mapping',
          label: 'stripe_inbound',
          data: {},
        },
      ],
      edges: [],
    }

    const graphWithMarketData: ManifestGraph = {
      nodes: [
        {
          id: 'market_data:SPOT',
          type: 'market_data',
          label: 'Spot Price',
          data: { code: 'SPOT' },
        },
      ],
      edges: [],
    }

    const graphWithOrganization: ManifestGraph = {
      nodes: [
        {
          id: 'organization:ACME',
          type: 'organization',
          label: 'Acme Corp',
          data: { code: 'ACME' },
        },
      ],
      edges: [],
    }

    const graphWithInternalAccount: ManifestGraph = {
      nodes: [
        {
          id: 'internal_account:OPS_GBP',
          type: 'internal_account',
          label: 'Operating GBP',
          data: { code: 'OPS_GBP', accountType: 'CURRENT' },
        },
      ],
      edges: [],
    }

    const graphWithOpGateway: ManifestGraph = {
      nodes: [
        {
          id: 'operational_gateway:gw',
          type: 'operational_gateway',
          label: 'Gateway',
          data: {},
        },
      ],
      edges: [],
    }

    const graphWithProviderConn: ManifestGraph = {
      nodes: [
        {
          id: 'provider_connection:conn-1',
          type: 'provider_connection',
          label: 'Stripe Connection',
          data: { connectionId: 'conn-1' },
        },
      ],
      edges: [],
    }

    const graphWithInstructionRoute: ManifestGraph = {
      nodes: [
        {
          id: 'instruction_route:payment',
          type: 'instruction_route',
          label: 'Payment Route',
          data: { connectionId: 'conn-1' },
        },
      ],
      edges: [],
    }

    const graphWithPartyType: ManifestGraph = {
      nodes: [
        {
          id: 'party_type:CUSTOMER',
          type: 'party_type',
          label: 'Customer',
          data: {},
        },
      ],
      edges: [],
    }

    it('assigns diff_saga node type for saga diffs', async () => {
      render(<ManifestDiffGraph before={emptyGraph} after={graphWithSaga} />)
      const node = await screen.findByTestId('diff-flow-node-saga:settle')
      expect(node).toHaveAttribute('data-node-type', 'diff_saga')
    })

    it('assigns diff_account_type node type', async () => {
      render(<ManifestDiffGraph before={emptyGraph} after={graphWithAccountType} />)
      const node = await screen.findByTestId('diff-flow-node-account_type:CURRENT')
      expect(node).toHaveAttribute('data-node-type', 'diff_account_type')
    })

    it('assigns diff_valuation_rule node type', async () => {
      render(<ManifestDiffGraph before={emptyGraph} after={graphWithValuation} />)
      const node = await screen.findByTestId('diff-flow-node-valuation_rule:KWH->GBP')
      expect(node).toHaveAttribute('data-node-type', 'diff_valuation_rule')
    })

    it('assigns diff_payment_rail node type', async () => {
      render(<ManifestDiffGraph before={emptyGraph} after={graphWithPaymentRail} />)
      const node = await screen.findByTestId('diff-flow-node-payment_rail:stripe')
      expect(node).toHaveAttribute('data-node-type', 'diff_payment_rail')
    })

    it('assigns diff_mapping node type', async () => {
      render(<ManifestDiffGraph before={emptyGraph} after={graphWithMapping} />)
      const node = await screen.findByTestId('diff-flow-node-mapping:inbound')
      expect(node).toHaveAttribute('data-node-type', 'diff_mapping')
    })

    it('assigns diff_market_data node type', async () => {
      render(<ManifestDiffGraph before={emptyGraph} after={graphWithMarketData} />)
      const node = await screen.findByTestId('diff-flow-node-market_data:SPOT')
      expect(node).toHaveAttribute('data-node-type', 'diff_market_data')
    })

    it('assigns diff_organization node type', async () => {
      render(<ManifestDiffGraph before={emptyGraph} after={graphWithOrganization} />)
      const node = await screen.findByTestId('diff-flow-node-organization:ACME')
      expect(node).toHaveAttribute('data-node-type', 'diff_organization')
    })

    it('assigns diff_internal_account node type', async () => {
      render(<ManifestDiffGraph before={emptyGraph} after={graphWithInternalAccount} />)
      const node = await screen.findByTestId('diff-flow-node-internal_account:OPS_GBP')
      expect(node).toHaveAttribute('data-node-type', 'diff_internal_account')
    })

    it('assigns diff_operational_gateway node type', async () => {
      render(<ManifestDiffGraph before={emptyGraph} after={graphWithOpGateway} />)
      const node = await screen.findByTestId('diff-flow-node-operational_gateway:gw')
      expect(node).toHaveAttribute('data-node-type', 'diff_operational_gateway')
    })

    it('assigns diff_provider_connection node type', async () => {
      render(<ManifestDiffGraph before={emptyGraph} after={graphWithProviderConn} />)
      const node = await screen.findByTestId('diff-flow-node-provider_connection:conn-1')
      expect(node).toHaveAttribute('data-node-type', 'diff_provider_connection')
    })

    it('assigns diff_instruction_route node type', async () => {
      render(<ManifestDiffGraph before={emptyGraph} after={graphWithInstructionRoute} />)
      const node = await screen.findByTestId('diff-flow-node-instruction_route:payment')
      expect(node).toHaveAttribute('data-node-type', 'diff_instruction_route')
    })

    it('assigns diff_party_type node type', async () => {
      render(<ManifestDiffGraph before={emptyGraph} after={graphWithPartyType} />)
      const node = await screen.findByTestId('diff-flow-node-party_type:CUSTOMER')
      expect(node).toHaveAttribute('data-node-type', 'diff_party_type')
    })
  })

  describe('combined diff states', () => {
    it('shows added, removed, and unchanged in one view', async () => {
      const before: ManifestGraph = {
        nodes: [
          { id: 'instrument:GBP', type: 'instrument', label: 'GBP', data: { code: 'GBP' } },
          { id: 'instrument:EUR', type: 'instrument', label: 'EUR', data: { code: 'EUR' } },
        ],
        edges: [],
      }
      const after: ManifestGraph = {
        nodes: [
          { id: 'instrument:GBP', type: 'instrument', label: 'GBP', data: { code: 'GBP' } },
          { id: 'instrument:USD', type: 'instrument', label: 'USD', data: { code: 'USD' } },
        ],
        edges: [],
      }
      render(<ManifestDiffGraph before={before} after={after} />)
      const flow = await screen.findByTestId('react-flow')
      // GBP unchanged + EUR removed + USD added = 3 nodes
      expect(flow).toHaveAttribute('data-node-count', '3')
      expect(await screen.findByText('+1 added')).toBeInTheDocument()
      expect(screen.getByText('-1 removed')).toBeInTheDocument()
    })

    it('renders removed node with removed status', async () => {
      const before: ManifestGraph = {
        nodes: [
          { id: 'instrument:GBP', type: 'instrument', label: 'GBP', data: { code: 'GBP' } },
          { id: 'instrument:EUR', type: 'instrument', label: 'EUR', data: { code: 'EUR' } },
        ],
        edges: [],
      }
      const after: ManifestGraph = {
        nodes: [
          { id: 'instrument:GBP', type: 'instrument', label: 'GBP', data: { code: 'GBP' } },
        ],
        edges: [],
      }
      render(<ManifestDiffGraph before={before} after={after} />)
      const removedNode = await screen.findByTestId('diff-flow-node-instrument:EUR')
      expect(removedNode).toHaveAttribute('data-diff-status', 'removed')
      const unchangedNode = screen.getByTestId('diff-flow-node-instrument:GBP')
      expect(unchangedNode).toHaveAttribute('data-diff-status', 'unchanged')
    })
  })
})
