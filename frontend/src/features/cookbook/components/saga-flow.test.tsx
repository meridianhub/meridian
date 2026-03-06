import { describe, it, expect, vi } from 'vitest'
import { render, screen } from '@testing-library/react'
import { SagaFlowDiagram } from './saga-flow'
import type { SagaFlow } from '../lib/star-parser'

// Mock @xyflow/react to avoid canvas rendering issues in jsdom
vi.mock('@xyflow/react', () => {
  const Position = { Top: 'top', Bottom: 'bottom', Left: 'left', Right: 'right' }
  const BackgroundVariant = { Dots: 'dots' }

  function Handle() { return null }

  function ReactFlow({ nodes, edges, children }: { nodes: unknown[]; edges: unknown[]; children?: React.ReactNode }) {
    return (
      <div data-testid="react-flow" data-node-count={nodes.length} data-edge-count={edges.length}>
        {(nodes as { id: string; data: { label?: string } }[]).map((n) => (
          <div key={n.id} data-testid={`node-${n.id}`}>
            {n.data?.label ?? n.id}
          </div>
        ))}
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

describe('SagaFlowDiagram', () => {
  it('renders ReactFlow container', () => {
    render(<SagaFlowDiagram flow={emptyFlow} />)
    expect(screen.getByTestId('react-flow')).toBeInTheDocument()
  })

  it('renders start and end nodes for empty flow', () => {
    render(<SagaFlowDiagram flow={emptyFlow} />)
    expect(screen.getByTestId('node-start')).toBeInTheDocument()
    expect(screen.getByTestId('node-end')).toBeInTheDocument()
  })

  it('renders step nodes for each step', () => {
    render(<SagaFlowDiagram flow={simpleFlow} />)
    expect(screen.getByTestId('node-step-0')).toBeInTheDocument()
    expect(screen.getByTestId('node-step-1')).toBeInTheDocument()
  })

  it('displays step names', () => {
    render(<SagaFlowDiagram flow={simpleFlow} />)
    expect(screen.getByText('lookup_account')).toBeInTheDocument()
    expect(screen.getByText('book_position')).toBeInTheDocument()
  })

  it('displays saga name in start node', () => {
    render(<SagaFlowDiagram flow={simpleFlow} />)
    expect(screen.getByText('deposit')).toBeInTheDocument()
  })

  it('renders decision and exit nodes for early exit', () => {
    render(<SagaFlowDiagram flow={flowWithEarlyExit} />)
    expect(screen.getByTestId('node-decision-0')).toBeInTheDocument()
    expect(screen.getByTestId('node-exit-0')).toBeInTheDocument()
  })

  it('renders controls', () => {
    render(<SagaFlowDiagram flow={simpleFlow} />)
    expect(screen.getByTestId('controls')).toBeInTheDocument()
  })

  it('renders services legend', () => {
    render(<SagaFlowDiagram flow={simpleFlow} />)
    expect(screen.getByText('reference_data')).toBeInTheDocument()
    expect(screen.getByText('position_keeping')).toBeInTheDocument()
  })

  it('shows correct node count', () => {
    render(<SagaFlowDiagram flow={simpleFlow} />)
    // start + 2 steps + end = 4 nodes
    expect(screen.getByTestId('react-flow')).toHaveAttribute('data-node-count', '4')
  })

  it('shows correct edge count for simple flow', () => {
    render(<SagaFlowDiagram flow={simpleFlow} />)
    // start->step0, step0->step1, step1->end = 3 edges
    expect(screen.getByTestId('react-flow')).toHaveAttribute('data-edge-count', '3')
  })
})
