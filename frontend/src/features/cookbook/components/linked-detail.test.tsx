import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, fireEvent } from '@testing-library/react'
import { LinkedPatternDetail } from './linked-detail'
import type { SagaFlow } from '../lib/star-parser'

// Mock @xyflow/react
vi.mock('@xyflow/react', () => ({
  ReactFlow: ({ onNodeClick, nodes }: { onNodeClick?: (e: unknown, node: unknown) => void; nodes: unknown[] }) => (
    <div data-testid="react-flow">
      {(nodes as { id: string; type: string; data: { label: string; serviceCalls?: { service: string; method: string }[] } }[]).map((node) => (
        <button
          key={node.id}
          data-testid={`flow-node-${node.id}`}
          data-node-type={node.type}
          onClick={(e) => onNodeClick?.(e, node)}
        >
          {node.data.label}
          {node.data.serviceCalls?.map((sc: { service: string; method: string }, i: number) => (
            <span key={i} data-testid={`service-call-${node.id}-${i}`}>
              {sc.service}.{sc.method}
            </span>
          ))}
        </button>
      ))}
    </div>
  ),
  Controls: () => <div data-testid="flow-controls" />,
  Background: () => <div data-testid="flow-background" />,
  BackgroundVariant: { Dots: 'dots' },
  Position: { Top: 'top', Bottom: 'bottom', Left: 'left', Right: 'right' },
  Handle: () => null,
}))

// Mock @dagrejs/dagre
vi.mock('@dagrejs/dagre', () => ({
  default: {
    graphlib: {
      Graph: class {
        setDefaultEdgeLabel = vi.fn().mockReturnThis()
        setGraph = vi.fn()
        setNode = vi.fn()
        setEdge = vi.fn()
        node = vi.fn(() => ({ x: 100, y: 100 }))
      },
    },
    layout: vi.fn(),
  },
}))

// Mock handler reference
vi.mock('@/shared/handler-reference', () => ({
  HandlerReference: ({ serviceNames, highlightedHandler }: { serviceNames?: string[]; highlightedHandler?: string }) => (
    <div data-testid="handler-reference" data-services={serviceNames?.join(',')} data-highlighted={highlightedHandler ?? ''}>
      Handler Reference
    </div>
  ),
}))

// Mock API context
vi.mock('@/api/context', () => ({
  useApiClients: vi.fn(() => ({
    sagaRegistry: {
      describeHandlers: vi.fn(async () => ({ services: [] })),
    },
  })),
}))

const sampleFlow: SagaFlow = {
  name: 'deposit-saga',
  trigger: 'payment.inbound',
  filter: null,
  steps: [
    {
      name: 'validate_payment',
      lineNumber: 5,
      serviceCalls: [
        { service: 'position_keeping', method: 'initiate_log', params: ['amount'] },
      ],
      earlyExit: null,
    },
    {
      name: 'apply_credit',
      lineNumber: 15,
      serviceCalls: [
        { service: 'position_keeping', method: 'apply_credit', params: ['amount', 'direction'] },
        { service: 'fees', method: 'calculate', params: ['amount'] },
      ],
      earlyExit: null,
    },
  ],
}

describe('LinkedPatternDetail', () => {
  beforeEach(() => {
    vi.clearAllMocks()
  })

  it('renders diagram and handler reference panels', () => {
    render(<LinkedPatternDetail flows={[sampleFlow]} />)
    expect(screen.getByTestId('react-flow')).toBeInTheDocument()
    expect(screen.getByTestId('handler-reference')).toBeInTheDocument()
  })

  it('renders full-width layout without editor panel', () => {
    const { container } = render(<LinkedPatternDetail flows={[sampleFlow]} />)
    const detail = container.querySelector('[data-testid="linked-detail"]')
    expect(detail).toBeInTheDocument()
    // Should not contain starlark editor
    expect(screen.queryByTestId('starlark-editor')).not.toBeInTheDocument()
  })

  it('highlights handler reference when step is clicked in diagram', () => {
    render(<LinkedPatternDetail flows={[sampleFlow]} />)

    const stepNode = screen.getByTestId('flow-node-step-0')
    fireEvent.click(stepNode)

    const handlerRef = screen.getByTestId('handler-reference')
    expect(handlerRef.dataset.highlighted).toBe('position_keeping.initiate_log')
  })

  it('updates selected step state when diagram step is clicked', () => {
    render(<LinkedPatternDetail flows={[sampleFlow]} />)

    const step1 = screen.getByTestId('flow-node-step-1')
    fireEvent.click(step1)

    const handlerRef = screen.getByTestId('handler-reference')
    expect(handlerRef.dataset.highlighted).toBe('position_keeping.apply_credit')
  })

  it('passes service names to handler reference from flow', () => {
    render(<LinkedPatternDetail flows={[sampleFlow]} />)

    const handlerRef = screen.getByTestId('handler-reference')
    const services = handlerRef.dataset.services?.split(',') ?? []
    expect(services).toContain('position_keeping')
    expect(services).toContain('fees')
  })

  it('clears highlighted handler when clicked step has no service calls', () => {
    const flowWithNoop: SagaFlow = {
      ...sampleFlow,
      steps: [
        ...sampleFlow.steps,
        { name: 'noop', lineNumber: 20, serviceCalls: [], earlyExit: null },
      ],
    }

    render(<LinkedPatternDetail flows={[flowWithNoop]} />)

    fireEvent.click(screen.getByTestId('flow-node-step-0'))
    expect(screen.getByTestId('handler-reference').dataset.highlighted).toBe('position_keeping.initiate_log')

    fireEvent.click(screen.getByTestId('flow-node-step-2'))
    expect(screen.getByTestId('handler-reference').dataset.highlighted).toBe('')
  })

  it('renders without crashing when flow has no steps', () => {
    const emptyFlow: SagaFlow = {
      name: 'empty-saga',
      trigger: null,
      filter: null,
      steps: [],
    }

    render(<LinkedPatternDetail flows={[emptyFlow]} />)

    expect(screen.getByTestId('linked-detail')).toBeInTheDocument()
  })
})
