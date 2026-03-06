import { useMemo, useState } from 'react'
import {
  ReactFlow,
  Controls,
  Background,
  type Node,
  type Edge,
  BackgroundVariant,
  Position,
  Handle,
} from '@xyflow/react'
import '@xyflow/react/dist/style.css'
import Dagre from '@dagrejs/dagre'
import type { SagaFlow } from '../lib/star-parser'

// Curated palette of visually distinct service colors
const SERVICE_PALETTE = [
  { bg: 'hsl(220, 70%, 92%)', fg: 'hsl(220, 70%, 45%)' },  // Blue
  { bg: 'hsl(150, 60%, 90%)', fg: 'hsl(150, 60%, 35%)' },  // Green
  { bg: 'hsl(30, 80%, 92%)', fg: 'hsl(30, 80%, 45%)' },    // Orange
  { bg: 'hsl(280, 60%, 92%)', fg: 'hsl(280, 60%, 45%)' },  // Purple
  { bg: 'hsl(0, 70%, 92%)', fg: 'hsl(0, 70%, 45%)' },      // Red
  { bg: 'hsl(180, 60%, 90%)', fg: 'hsl(180, 60%, 35%)' },  // Teal
  { bg: 'hsl(50, 80%, 90%)', fg: 'hsl(50, 80%, 35%)' },    // Yellow
  { bg: 'hsl(330, 60%, 92%)', fg: 'hsl(330, 60%, 45%)' },  // Pink
]

function buildServiceColorMap(flow: SagaFlow): Map<string, { bg: string; fg: string }> {
  const services = new Set<string>()
  for (const step of flow.steps) {
    for (const call of step.serviceCalls) {
      services.add(call.service)
    }
  }
  const sorted = [...services].sort()
  const map = new Map<string, { bg: string; fg: string }>()
  sorted.forEach((svc, i) => {
    map.set(svc, SERVICE_PALETTE[i % SERVICE_PALETTE.length])
  })
  return map
}

// --- Custom Node Components ---

interface StartNodeData {
  label: string
  trigger: string | null
  [key: string]: unknown
}

function StartNode({ data }: { data: StartNodeData }) {
  return (
    <>
      <div className="flex flex-col items-center justify-center rounded-full border-2 border-emerald-500 bg-emerald-50 px-4 py-2 dark:bg-emerald-950/40">
        <span className="text-xs font-semibold text-emerald-700 dark:text-emerald-300">{data.label}</span>
        {data.trigger && (
          <span className="text-[10px] text-emerald-600 dark:text-emerald-400">{data.trigger}</span>
        )}
      </div>
      <Handle type="source" position={Position.Right} className="!bg-emerald-500 !border-0 !w-2 !h-2" />
    </>
  )
}

interface StepNodeData {
  label: string
  serviceCalls: { service: string; method: string }[]
  serviceColors: Map<string, { bg: string; fg: string }>
  highlightedService: string | null
  [key: string]: unknown
}

function StepNode({ data }: { data: StepNodeData }) {
  const primaryService = data.serviceCalls[0]?.service
  const primaryColors = primaryService ? data.serviceColors.get(primaryService) : undefined
  const borderColor = primaryColors?.fg ?? '#71717a'

  const usesHighlighted = data.highlightedService
    ? data.serviceCalls.some((c) => c.service === data.highlightedService)
    : true
  const dimmed = data.highlightedService && !usesHighlighted

  return (
    <>
      <Handle type="target" position={Position.Left} className="!bg-transparent !border-0 !w-0 !h-0" />
      <div
        className={`flex flex-col gap-1 rounded-lg border-2 bg-background px-3 py-2 shadow-sm min-w-[180px] transition-opacity ${dimmed ? 'opacity-30' : 'opacity-100'}`}
        style={{
          borderColor,
          ...(data.highlightedService && usesHighlighted
            ? { boxShadow: `0 0 0 2px ${borderColor}`, outline: `2px solid ${borderColor}`, outlineOffset: '2px' }
            : {}),
        }}
      >
        <span className="text-xs font-semibold text-foreground">{data.label}</span>
        {data.serviceCalls.length > 0 && (
          <div className="flex flex-wrap gap-1">
            {data.serviceCalls.map((call, idx) => {
              const colors = data.serviceColors.get(call.service)
              return (
                <span
                  key={`${call.service}.${call.method}.${idx}`}
                  className="inline-flex items-center rounded-full px-1.5 py-0.5 text-[10px] font-medium"
                  style={{
                    backgroundColor: colors?.bg ?? 'hsl(0, 0%, 92%)',
                    color: colors?.fg ?? 'hsl(0, 0%, 45%)',
                  }}
                >
                  {call.service}.{call.method}
                </span>
              )
            })}
          </div>
        )}
      </div>
      <Handle type="source" position={Position.Right} className="!bg-transparent !border-0 !w-0 !h-0" />
    </>
  )
}

interface DecisionNodeData {
  label: string
  [key: string]: unknown
}

function DecisionNode({ data }: { data: DecisionNodeData }) {
  return (
    <>
      <Handle type="target" position={Position.Left} className="!bg-transparent !border-0 !w-0 !h-0" />
      <div
        className="flex items-center justify-center border-2 border-amber-500 bg-amber-50 dark:bg-amber-950/40"
        style={{
          width: 120,
          height: 80,
          clipPath: 'polygon(50% 0%, 100% 50%, 50% 100%, 0% 50%)',
        }}
      >
        <span className="text-[10px] font-medium text-amber-700 dark:text-amber-300 text-center leading-tight px-4 max-w-[90px]">
          {data.label}
        </span>
      </div>
      <Handle type="source" position={Position.Bottom} id="exit" className="!bg-transparent !border-0 !w-0 !h-0" />
      <Handle type="source" position={Position.Right} id="no" className="!bg-transparent !border-0 !w-0 !h-0" />
    </>
  )
}

interface ExitNodeData {
  label: string
  [key: string]: unknown
}

function ExitNode({ data }: { data: ExitNodeData }) {
  return (
    <>
      <Handle type="target" position={Position.Left} className="!bg-transparent !border-0 !w-0 !h-0" />
      <div className="flex items-center justify-center rounded-full border-2 border-red-500 bg-red-50 px-3 py-1.5 dark:bg-red-950/40">
        <span className="text-[10px] font-semibold text-red-700 dark:text-red-300">{data.label}</span>
      </div>
    </>
  )
}

function EndNode() {
  return (
    <>
      <Handle type="target" position={Position.Left} className="!bg-transparent !border-0 !w-0 !h-0" />
      <div className="flex items-center justify-center rounded-full border-2 border-slate-500 bg-slate-100 px-4 py-2 dark:bg-slate-800">
        <span className="text-xs font-semibold text-slate-600 dark:text-slate-300">COMPLETED</span>
      </div>
    </>
  )
}

const nodeTypes = {
  sagaStart: StartNode,
  sagaStep: StepNode,
  sagaDecision: DecisionNode,
  sagaExit: ExitNode,
  sagaEnd: EndNode,
}

// --- Layout ---

const NODE_DIMENSIONS: Record<string, { width: number; height: number }> = {
  sagaStart: { width: 160, height: 50 },
  sagaStep: { width: 200, height: 60 },
  sagaDecision: { width: 120, height: 80 },
  sagaExit: { width: 120, height: 36 },
  sagaEnd: { width: 140, height: 44 },
}

function layoutNodes(nodes: Node[], edges: Edge[]): Node[] {
  const g = new Dagre.graphlib.Graph().setDefaultEdgeLabel(() => ({}))
  g.setGraph({ rankdir: 'LR', nodesep: 60, ranksep: 100 })

  for (const n of nodes) {
    const dims = NODE_DIMENSIONS[n.type ?? 'sagaStep'] ?? { width: 200, height: 60 }
    g.setNode(n.id, { width: dims.width, height: dims.height })
  }

  for (const e of edges) {
    g.setEdge(e.source, e.target)
  }

  Dagre.layout(g)

  return nodes.map((n) => {
    const nodeWithPos = g.node(n.id)
    const dims = NODE_DIMENSIONS[n.type ?? 'sagaStep'] ?? { width: 200, height: 60 }
    return {
      ...n,
      position: {
        x: (nodeWithPos?.x ?? 0) - dims.width / 2,
        y: (nodeWithPos?.y ?? 0) - dims.height / 2,
      },
    }
  })
}

// --- Build Graph from SagaFlow ---

function buildFlowGraph(
  flow: SagaFlow,
  serviceColors: Map<string, { bg: string; fg: string }>,
  highlightedService: string | null,
): { nodes: Node[]; edges: Edge[] } {
  const nodes: Node[] = []
  const edges: Edge[] = []

  // Start node
  nodes.push({
    id: 'start',
    type: 'sagaStart',
    position: { x: 0, y: 0 },
    data: { label: flow.name, trigger: flow.trigger } satisfies StartNodeData,
  })

  if (flow.steps.length === 0) {
    nodes.push({
      id: 'end',
      type: 'sagaEnd',
      position: { x: 0, y: 0 },
      data: {},
    })
    edges.push({ id: 'start-end', source: 'start', target: 'end' })
    return { nodes: layoutNodes(nodes, edges), edges }
  }

  // Step + decision + exit nodes
  let prevId: string | null = 'start'

  for (let i = 0; i < flow.steps.length; i++) {
    const step = flow.steps[i]
    const stepId = `step-${i}`
    const nextId = i + 1 < flow.steps.length ? `step-${i + 1}` : 'end'

    nodes.push({
      id: stepId,
      type: 'sagaStep',
      position: { x: 0, y: 0 },
      data: {
        label: step.name,
        serviceCalls: step.serviceCalls,
        serviceColors,
        highlightedService,
      } satisfies StepNodeData,
    })

    // Connect from previous node (null when previous decision's "No" edge already connects)
    if (prevId) {
      edges.push({
        id: `${prevId}-${stepId}`,
        source: prevId,
        target: stepId,
      })
    }

    if (step.earlyExit) {
      const decisionId = `decision-${i}`
      const exitId = `exit-${i}`

      nodes.push({
        id: decisionId,
        type: 'sagaDecision',
        position: { x: 0, y: 0 },
        data: { label: step.earlyExit.condition } satisfies DecisionNodeData,
      })

      nodes.push({
        id: exitId,
        type: 'sagaExit',
        position: { x: 0, y: 0 },
        data: { label: step.earlyExit.returnStatus } satisfies ExitNodeData,
      })

      edges.push({
        id: `${stepId}-${decisionId}`,
        source: stepId,
        target: decisionId,
      })

      // "Yes" -> exit
      edges.push({
        id: `${decisionId}-${exitId}`,
        source: decisionId,
        sourceHandle: 'exit',
        target: exitId,
        label: 'Yes',
        style: { stroke: '#ef4444', strokeDasharray: '6 3' },
      })

      // "No" -> next step (already connects to nextId, so skip prevId for next iteration)
      edges.push({
        id: `${decisionId}-${nextId}`,
        source: decisionId,
        sourceHandle: 'no',
        target: nextId,
        label: 'No',
      })

      prevId = null
    } else {
      prevId = stepId
    }
  }

  // End node
  nodes.push({
    id: 'end',
    type: 'sagaEnd',
    position: { x: 0, y: 0 },
    data: {},
  })

  // Connect last step to end (only if no early exit on last step already connected it)
  if (prevId) {
    edges.push({
      id: `${prevId}-end`,
      source: prevId,
      target: 'end',
    })
  }

  return { nodes: layoutNodes(nodes, edges), edges }
}

// --- Component ---

interface SagaFlowDiagramProps {
  flow: SagaFlow
  onStepClick?: (stepName: string, lineNumber: number) => void
  className?: string
}

export function SagaFlowDiagram({ flow, onStepClick, className }: SagaFlowDiagramProps) {
  const [highlightedService, setHighlightedService] = useState<string | null>(null)

  const serviceColors = useMemo(() => buildServiceColorMap(flow), [flow])

  const { nodes, edges } = useMemo(
    () => buildFlowGraph(flow, serviceColors, highlightedService),
    [flow, serviceColors, highlightedService],
  )

  // Collect unique services for the legend
  const services = useMemo(() => [...serviceColors.keys()].sort(), [serviceColors])

  return (
    <div className={`relative ${className ?? ''}`} style={{ width: '100%', height: '100%', minHeight: 300 }}>
      <ReactFlow
        nodes={nodes}
        edges={edges}
        nodeTypes={nodeTypes}
        onNodeClick={(_event, node) => {
          if (node.type === 'sagaStep' && onStepClick) {
            const stepIndex = parseInt(node.id.replace('step-', ''), 10)
            const step = flow.steps[stepIndex]
            if (step) onStepClick(step.name, step.lineNumber)
          }
        }}
        fitView
        proOptions={{ hideAttribution: true }}
        nodesDraggable={false}
        nodesConnectable={false}
      >
        <Controls showInteractive={false} position="top-right" />
        <Background variant={BackgroundVariant.Dots} gap={16} size={1} />
      </ReactFlow>

      {services.length > 0 && (
        <div className="absolute bottom-3 left-3 z-10 flex flex-col gap-1 rounded-lg border bg-background/95 p-3 backdrop-blur-sm shadow-sm">
          <span className="text-xs font-semibold text-muted-foreground uppercase tracking-wider mb-1">Services</span>
          {services.map((svc) => {
            const colors = serviceColors.get(svc)
            const isActive = highlightedService === svc
            return (
              <button
                key={svc}
                type="button"
                className={`flex items-center gap-2 cursor-pointer rounded px-1 -mx-1 transition-colors hover:bg-muted/50 ${isActive ? 'font-semibold' : ''}`}
                onClick={() => setHighlightedService((prev) => (prev === svc ? null : svc))}
              >
                <span
                  className={`inline-block h-2.5 w-2.5 rounded-full ${isActive ? 'ring-2 ring-offset-1' : ''}`}
                  style={{ backgroundColor: colors?.fg }}
                />
                <span className="text-xs text-muted-foreground">{svc}</span>
              </button>
            )
          })}
        </div>
      )}
    </div>
  )
}
