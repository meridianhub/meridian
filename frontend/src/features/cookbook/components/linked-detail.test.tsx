import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, fireEvent } from '@testing-library/react'
import { LinkedPatternDetail } from './linked-detail'
import type { SagaFlow } from '../lib/star-parser'

// Mock CodeMirror (jsdom doesn't support it)
vi.mock('codemirror', () => ({ basicSetup: [] }))

const mockDispatch = vi.fn()
let lastEditorDoc = ''

vi.mock('@codemirror/view', () => ({
  EditorView: class MockEditorView {
    static editable = { of: vi.fn(() => ({})) }
    static updateListener = { of: vi.fn(() => ({})) }
    static lineWrapping = {}
    dom: HTMLElement
    state: { doc: { toString: () => string; line: (n: number) => { from: number; to: number } } }
    dispatch = mockDispatch

    constructor(config: { doc?: string; extensions?: unknown[]; parent?: HTMLElement }) {
      this.dom = document.createElement('div')
      this.dom.className = 'cm-editor'
      lastEditorDoc = config.doc ?? ''
      this.state = {
        doc: {
          toString: () => lastEditorDoc,
          line: (n: number) => ({ from: (n - 1) * 20, to: n * 20 }),
        },
      }
      if (config.parent) config.parent.appendChild(this.dom)
    }

    destroy() {
      this.dom.remove()
    }
  },
  Decoration: {
    mark: vi.fn(() => ({
      range: vi.fn((_from: number, _to: number) => ({})),
    })),
    set: vi.fn(() => ({})),
  },
  ViewPlugin: {
    fromClass: vi.fn(() => ({})),
  },
}))

vi.mock('@codemirror/state', () => ({
  Compartment: class {
    of = vi.fn(() => ({}))
    reconfigure = vi.fn(() => ({}))
  },
  EditorState: { create: vi.fn(() => ({})), readOnly: { of: vi.fn(() => ({})) } },
  Transaction: { userEvent: 'user-event' },
  RangeSetBuilder: class {
    add = vi.fn()
    finish = vi.fn(() => ({}))
  },
  StateField: { define: vi.fn(() => ({})) },
  StateEffect: { define: vi.fn(() => ({ of: vi.fn(() => ({})) })) },
}))

vi.mock('@codemirror/lang-python', () => ({ python: vi.fn(() => ({})) }))
vi.mock('@codemirror/lint', () => ({
  linter: vi.fn(() => ({})),
  lintGutter: vi.fn(() => ({})),
}))

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

const sampleStarlark = `# Saga: deposit-saga
# Trigger: payment.inbound
def execute(input_data):
    step(name="validate_payment")
    position_keeping.initiate_log(amount=input_data.amount)

    step(name="apply_credit")
    position_keeping.apply_credit(amount=input_data.amount, direction="CREDIT")
    fees.calculate(amount=input_data.amount)
`

describe('LinkedPatternDetail', () => {
  beforeEach(() => {
    vi.clearAllMocks()
  })

  it('renders editor, diagram, and handler reference panels', () => {
    render(
      <LinkedPatternDetail
        flow={sampleFlow}
        starlarkContent={sampleStarlark}
      />,
    )
    expect(screen.getByTestId('starlark-editor')).toBeInTheDocument()
    expect(screen.getByTestId('react-flow')).toBeInTheDocument()
    expect(screen.getByTestId('handler-reference')).toBeInTheDocument()
  })

  it('renders three-panel layout with resizable areas', () => {
    const { container } = render(
      <LinkedPatternDetail
        flow={sampleFlow}
        starlarkContent={sampleStarlark}
      />,
    )
    expect(container.querySelector('[data-testid="linked-detail"]')).toBeInTheDocument()
  })

  it('highlights handler reference when step is clicked in diagram', () => {
    render(
      <LinkedPatternDetail
        flow={sampleFlow}
        starlarkContent={sampleStarlark}
      />,
    )

    const stepNode = screen.getByTestId('flow-node-step-0')
    fireEvent.click(stepNode)

    const handlerRef = screen.getByTestId('handler-reference')
    expect(handlerRef.dataset.highlighted).toBe('position_keeping.initiate_log')
  })

  it('updates selected step state when diagram step is clicked', () => {
    render(
      <LinkedPatternDetail
        flow={sampleFlow}
        starlarkContent={sampleStarlark}
      />,
    )

    const step1 = screen.getByTestId('flow-node-step-1')
    fireEvent.click(step1)

    const handlerRef = screen.getByTestId('handler-reference')
    // The second step has position_keeping.apply_credit as first service call
    expect(handlerRef.dataset.highlighted).toBe('position_keeping.apply_credit')
  })

  it('passes service names to handler reference from flow', () => {
    render(
      <LinkedPatternDetail
        flow={sampleFlow}
        starlarkContent={sampleStarlark}
      />,
    )

    const handlerRef = screen.getByTestId('handler-reference')
    const services = handlerRef.dataset.services?.split(',') ?? []
    expect(services).toContain('position_keeping')
    expect(services).toContain('fees')
  })

  it('dispatches to editor when step is clicked to scroll to line', () => {
    render(
      <LinkedPatternDetail
        flow={sampleFlow}
        starlarkContent={sampleStarlark}
      />,
    )

    const stepNode = screen.getByTestId('flow-node-step-0')
    fireEvent.click(stepNode)

    // Editor dispatch should be called to scroll/highlight
    expect(mockDispatch).toHaveBeenCalled()
  })

  it('clears highlighted handler when clicked step has no service calls', () => {
    const flowWithNoop: SagaFlow = {
      ...sampleFlow,
      steps: [
        ...sampleFlow.steps,
        { name: 'noop', lineNumber: 20, serviceCalls: [], earlyExit: null },
      ],
    }

    render(<LinkedPatternDetail flow={flowWithNoop} starlarkContent={sampleStarlark} />)

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

    render(
      <LinkedPatternDetail
        flow={emptyFlow}
        starlarkContent=""
      />,
    )

    expect(screen.getByTestId('linked-detail')).toBeInTheDocument()
  })
})
