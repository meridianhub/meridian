import { describe, it, expect, vi } from 'vitest'
import { render, screen, fireEvent } from '@testing-library/react'
import { SagaFlowDiagram } from './saga-flow'
import { estimateDecisionSize, estimateStartNodeWidth, getNodeDimensions } from './saga-flow-sizing'
import { parseTriggerService } from '../lib/star-parser'
import type { SagaFlow } from '../lib/star-parser'

// Mock @xyflow/react to avoid canvas rendering issues in jsdom
vi.mock('@xyflow/react', () => {
  const Position = { Top: 'top', Bottom: 'bottom', Left: 'left', Right: 'right' }
  const BackgroundVariant = { Dots: 'dots' }

  function Handle() { return null }

  // Render actual custom node components so their rendering logic gets coverage
  function ReactFlow({ nodes, edges, nodeTypes, children }: {
    nodes: { id: string; type?: string; data: Record<string, unknown> }[];
    edges: unknown[];
    nodeTypes?: Record<string, React.ComponentType<{ data: Record<string, unknown> }>>;
    children?: React.ReactNode;
  }) {
    return (
      <div data-testid="react-flow" data-node-count={nodes.length} data-edge-count={edges.length}>
        {nodes.map((n) => {
          const NodeComponent = nodeTypes?.[n.type ?? '']
          return (
            <div key={n.id} data-testid={`node-${n.id}`}>
              {NodeComponent ? <NodeComponent data={n.data} /> : (n.data?.label as string) ?? n.id}
            </div>
          )
        })}
        {children}
      </div>
    )
  }

  function Controls() { return <div data-testid="controls" /> }
  function Background() { return null }

  return { ReactFlow, Controls, Background, Handle, Position, BackgroundVariant }
})

// Mock dagre for layout
vi.mock('@dagrejs/dagre', () => {
  const graphlib = {
    Graph: class {
      nodes: Record<string, { x: number; y: number; width: number; height: number }> = {}
      setDefaultEdgeLabel() { return this }
      setGraph() {}
      setNode(id: string, data: { width: number; height: number }) {
        this.nodes[id] = { x: 0, y: 0, ...data }
      }
      setEdge() {}
      node(id: string) { return this.nodes[id] ?? { x: 0, y: 0 } }
    },
  }

  return {
    default: {
      graphlib,
      layout(g: InstanceType<typeof graphlib.Graph>) {
        // Assign sequential y positions
        let y = 0
        for (const [, data] of Object.entries(g.nodes)) {
          data.x = 100
          data.y = y
          y += 100
        }
      },
    },
  }
})

const emptyFlow: SagaFlow = {
  name: 'empty_saga',
  trigger: null,
  filter: null,
  steps: [],
}

const simpleFlow: SagaFlow = {
  name: 'deposit',
  trigger: 'event:payments.received.v1',
  filter: null,
  steps: [
    {
      name: 'lookup_account',
      lineNumber: 5,
      serviceCalls: [{ service: 'reference_data', method: 'get_account', params: ['id'] }],
      earlyExit: null,
    },
    {
      name: 'book_position',
      lineNumber: 10,
      serviceCalls: [{ service: 'position_keeping', method: 'initiate_log', params: ['account_id', 'amount'] }],
      earlyExit: null,
    },
  ],
}

const flowWithEarlyExit: SagaFlow = {
  name: 'idempotent_saga',
  trigger: null,
  filter: null,
  steps: [
    {
      name: 'check_duplicate',
      lineNumber: 3,
      serviceCalls: [{ service: 'pk', method: 'query_logs', params: ['cid'] }],
      earlyExit: { condition: 'existing.count > 0', returnStatus: 'ALREADY_PROCESSED' },
    },
    {
      name: 'book',
      lineNumber: 8,
      serviceCalls: [{ service: 'pk', method: 'initiate_log', params: ['aid'] }],
      earlyExit: null,
    },
  ],
}

const secondFlow: SagaFlow = {
  name: 'settlement',
  trigger: 'event:position-keeping.transaction-captured.v1',
  filter: null,
  steps: [
    {
      name: 'compute_value',
      lineNumber: 3,
      serviceCalls: [{ service: 'valuation_engine', method: 'compute', params: ['amount'] }],
      earlyExit: null,
    },
  ],
}

describe('SagaFlowDiagram', () => {
  it('renders ReactFlow container', () => {
    render(<SagaFlowDiagram flows={[emptyFlow]} />)
    expect(screen.getByTestId('react-flow')).toBeInTheDocument()
  })

  it('renders start and end nodes for empty flow', () => {
    render(<SagaFlowDiagram flows={[emptyFlow]} />)
    expect(screen.getByTestId('node-start')).toBeInTheDocument()
    expect(screen.getByTestId('node-end')).toBeInTheDocument()
  })

  it('renders step nodes for each step', () => {
    render(<SagaFlowDiagram flows={[simpleFlow]} />)
    expect(screen.getByTestId('node-step-0')).toBeInTheDocument()
    expect(screen.getByTestId('node-step-1')).toBeInTheDocument()
  })

  it('displays step names', () => {
    render(<SagaFlowDiagram flows={[simpleFlow]} />)
    expect(screen.getByText('lookup_account')).toBeInTheDocument()
    expect(screen.getByText('book_position')).toBeInTheDocument()
  })

  it('displays saga name in start node', () => {
    render(<SagaFlowDiagram flows={[simpleFlow]} />)
    expect(screen.getByText('deposit')).toBeInTheDocument()
  })

  it('renders decision and exit nodes for early exit', () => {
    render(<SagaFlowDiagram flows={[flowWithEarlyExit]} />)
    expect(screen.getByTestId('node-decision-0')).toBeInTheDocument()
    expect(screen.getByTestId('node-exit-0')).toBeInTheDocument()
  })

  it('renders controls', () => {
    render(<SagaFlowDiagram flows={[simpleFlow]} />)
    expect(screen.getByTestId('controls')).toBeInTheDocument()
  })

  it('renders services legend', () => {
    render(<SagaFlowDiagram flows={[simpleFlow]} />)
    expect(screen.getByText('reference_data')).toBeInTheDocument()
    expect(screen.getByText('position_keeping')).toBeInTheDocument()
  })

  it('shows correct node count', () => {
    render(<SagaFlowDiagram flows={[simpleFlow]} />)
    // start + 2 steps + end = 4 nodes
    expect(screen.getByTestId('react-flow')).toHaveAttribute('data-node-count', '4')
  })

  it('shows correct edge count for simple flow', () => {
    render(<SagaFlowDiagram flows={[simpleFlow]} />)
    // start->step0, step0->step1, step1->end = 3 edges
    expect(screen.getByTestId('react-flow')).toHaveAttribute('data-edge-count', '3')
  })

  it('renders service legend items as clickable buttons', () => {
    render(<SagaFlowDiagram flows={[simpleFlow]} />)
    const refDataBtn = screen.getByRole('button', { name: /reference_data/ })
    expect(refDataBtn).toBeInTheDocument()
    const posKeepBtn = screen.getByRole('button', { name: /position_keeping/ })
    expect(posKeepBtn).toBeInTheDocument()
  })

  it('toggles service highlight on legend click', () => {
    render(<SagaFlowDiagram flows={[simpleFlow]} />)
    const refDataBtn = screen.getByRole('button', { name: /reference_data/ })

    // Click to highlight
    fireEvent.click(refDataBtn)
    expect(refDataBtn).toHaveAttribute('aria-pressed', 'true')

    // Click again to unhighlight
    fireEvent.click(refDataBtn)
    expect(refDataBtn).toHaveAttribute('aria-pressed', 'false')
  })

  it('assigns distinct colors to different services', () => {
    render(<SagaFlowDiagram flows={[simpleFlow]} />)
    const refDataBtn = screen.getByRole('button', { name: /reference_data/ })
    const posKeepBtn = screen.getByRole('button', { name: /position_keeping/ })
    const refDot = refDataBtn.querySelector('span[class*="rounded-full"]')
    const posDot = posKeepBtn.querySelector('span[class*="rounded-full"]')
    expect(refDot?.getAttribute('style')).not.toEqual(posDot?.getAttribute('style'))
  })

  it('includes trigger service in legend', () => {
    render(<SagaFlowDiagram flows={[simpleFlow]} />)
    // simpleFlow has trigger "event:payments.received.v1" → service "payments"
    expect(screen.getByRole('button', { name: /payments/ })).toBeInTheDocument()
  })

  it('shows trigger label next to trigger service in legend', () => {
    render(<SagaFlowDiagram flows={[simpleFlow]} />)
    expect(screen.getByText('trigger')).toBeInTheDocument()
  })

  // Multi-saga tests
  it('renders multiple sagas with prefixed node IDs', () => {
    render(<SagaFlowDiagram flows={[simpleFlow, secondFlow]} />)
    // First saga: s0-start, s0-step-0, s0-step-1, s0-end
    expect(screen.getByTestId('node-s0-start')).toBeInTheDocument()
    expect(screen.getByTestId('node-s0-step-0')).toBeInTheDocument()
    // Second saga: s1-start, s1-step-0, s1-end
    expect(screen.getByTestId('node-s1-start')).toBeInTheDocument()
    expect(screen.getByTestId('node-s1-step-0')).toBeInTheDocument()
  })

  it('shows saga legend for multi-saga patterns', () => {
    render(<SagaFlowDiagram flows={[simpleFlow, secondFlow]} />)
    expect(screen.getByText('Sagas')).toBeInTheDocument()
    expect(screen.getByRole('button', { name: /deposit/ })).toBeInTheDocument()
    expect(screen.getByRole('button', { name: /settlement/ })).toBeInTheDocument()
  })

  it('does not show saga legend for single saga', () => {
    render(<SagaFlowDiagram flows={[simpleFlow]} />)
    expect(screen.queryByText('Sagas')).not.toBeInTheDocument()
  })

  it('merges services from all sagas into one legend', () => {
    render(<SagaFlowDiagram flows={[simpleFlow, secondFlow]} />)
    // Services from both: payments (trigger), reference_data, position_keeping, valuation_engine
    expect(screen.getByRole('button', { name: /valuation_engine/ })).toBeInTheDocument()
    expect(screen.getByRole('button', { name: /reference_data/ })).toBeInTheDocument()
  })

  it('renders decision node with condition label visible', () => {
    render(<SagaFlowDiagram flows={[flowWithEarlyExit]} />)
    expect(screen.getByText('existing.count > 0')).toBeInTheDocument()
  })

  it('renders exit node with return status visible', () => {
    render(<SagaFlowDiagram flows={[flowWithEarlyExit]} />)
    expect(screen.getByText('ALREADY_PROCESSED')).toBeInTheDocument()
  })

  it('renders trigger badge in start node', () => {
    render(<SagaFlowDiagram flows={[simpleFlow]} />)
    expect(screen.getByText('event:payments.received.v1')).toBeInTheDocument()
  })

  it('renders COMPLETED label in end node', () => {
    render(<SagaFlowDiagram flows={[simpleFlow]} />)
    expect(screen.getByText('COMPLETED')).toBeInTheDocument()
  })

  it('renders service call badges in step nodes', () => {
    render(<SagaFlowDiagram flows={[simpleFlow]} />)
    expect(screen.getByText('reference_data.get_account')).toBeInTheDocument()
    expect(screen.getByText('position_keeping.initiate_log')).toBeInTheDocument()
  })
})

describe('estimateDecisionSize', () => {
  it('returns minimum size for short labels', () => {
    const size = estimateDecisionSize('x > 0')
    expect(size.width).toBeGreaterThanOrEqual(120)
    expect(size.height).toBeGreaterThanOrEqual(80)
  })

  it('scales up for longer labels', () => {
    const short = estimateDecisionSize('x > 0')
    const long = estimateDecisionSize('not billing_account_id')
    expect(long.width).toBeGreaterThan(short.width)
  })

  it('caps at maximum width', () => {
    const size = estimateDecisionSize('a'.repeat(100))
    expect(size.width).toBeLessThanOrEqual(220)
  })
})

describe('estimateStartNodeWidth', () => {
  it('returns minimum width for short names', () => {
    expect(estimateStartNodeWidth('test', null)).toBeGreaterThanOrEqual(160)
  })

  it('scales up for long trigger text', () => {
    const short = estimateStartNodeWidth('saga', 'scheduled:topup')
    const long = estimateStartNodeWidth('saga', 'event:position-keeping.transaction-captured.v1')
    expect(long).toBeGreaterThan(short)
  })

  it('caps at maximum width', () => {
    expect(estimateStartNodeWidth('a'.repeat(100), null)).toBeLessThanOrEqual(300)
  })
})

describe('getNodeDimensions', () => {
  it('uses dynamic sizing for decision nodes', () => {
    const short = getNodeDimensions('sagaDecision', 'x > 0', null)
    const long = getNodeDimensions('sagaDecision', 'not billing_account_id', null)
    expect(long.width).toBeGreaterThan(short.width)
  })

  it('uses dynamic sizing for start nodes', () => {
    const short = getNodeDimensions('sagaStart', 'saga', null)
    const long = getNodeDimensions('sagaStart', 'saga', 'event:position-keeping.transaction-captured.v1')
    expect(long.width).toBeGreaterThan(short.width)
  })

  it('adjusts start node height based on trigger presence', () => {
    const withTrigger = getNodeDimensions('sagaStart', 'saga', 'scheduled:topup')
    const noTrigger = getNodeDimensions('sagaStart', 'saga', null)
    expect(withTrigger.height).toBeGreaterThan(noTrigger.height)
  })

  it('returns default dimensions for step nodes', () => {
    const dims = getNodeDimensions('sagaStep', 'step', null)
    expect(dims).toEqual({ width: 200, height: 60 })
  })

  it('returns default dimensions for exit nodes', () => {
    const dims = getNodeDimensions('sagaExit', 'exit', null)
    expect(dims).toEqual({ width: 120, height: 36 })
  })

  it('returns default dimensions for end nodes', () => {
    const dims = getNodeDimensions('sagaEnd', '', null)
    expect(dims).toEqual({ width: 140, height: 44 })
  })

  it('falls back to step dimensions for unknown types', () => {
    const dims = getNodeDimensions('unknown', '', null)
    expect(dims).toEqual({ width: 200, height: 60 })
  })
})

describe('parseTriggerService', () => {
  it('extracts service from event trigger', () => {
    expect(parseTriggerService('event:position-keeping.transaction-captured.v1')).toBe('position_keeping')
  })

  it('extracts service from webhook trigger', () => {
    expect(parseTriggerService('webhook:stripe.payment_intent.succeeded')).toBe('stripe')
  })

  it('returns null for api trigger', () => {
    expect(parseTriggerService('api:/v1/payments/stripe')).toBeNull()
  })

  it('returns null for null trigger', () => {
    expect(parseTriggerService(null)).toBeNull()
  })

  it('handles event with no dot separator', () => {
    expect(parseTriggerService('event:simple')).toBe('simple')
  })
})
