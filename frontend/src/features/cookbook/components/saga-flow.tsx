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
import { parseTriggerService } from '../lib/star-parser'

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

// Separate palette for saga highlighting (border/outline colors)
const SAGA_PALETTE = [
  'hsl(220, 70%, 50%)',   // Blue
  'hsl(150, 60%, 40%)',   // Green
  'hsl(30, 80%, 50%)',    // Orange
  'hsl(280, 60%, 50%)',   // Purple
  'hsl(0, 70%, 50%)',     // Red
  'hsl(180, 60%, 40%)',   // Teal
]

function buildServiceColorMap(flows: SagaFlow[]): Map<string, { bg: string; fg: string }> {
  const services = new Set<string>()
  for (const flow of flows) {
    const triggerSvc = parseTriggerService(flow.trigger)
    if (triggerSvc) services.add(triggerSvc)
    for (const step of flow.steps) {
      for (const call of step.serviceCalls) {
        services.add(call.service)
      }
    }
  }
  const sorted = [...services].sort()
  const map = new Map<string, { bg: string; fg: string }>()
  sorted.forEach((svc, i) => {
    map.set(svc, SERVICE_PALETTE[i % SERVICE_PALETTE.length])
  })
  return map
}

function buildSagaColorMap(flows: SagaFlow[]): Map<string, string> {
  const map = new Map<string, string>()
  flows.forEach((flow, i) => {
    map.set(flow.name, SAGA_PALETTE[i % SAGA_PALETTE.length])
  })
  return map
}

// --- Custom Node Components ---

interface StartNodeData {
  label: string
  trigger: string | null
  triggerService: string | null
  serviceColors: Map<string, { bg: string; fg: string }>
  highlightedService: string | null
  sagaName: string
  highlightedSaga: string | null
  sagaColor: string | undefined
  direction: FlowDirection
  [key: string]: unknown
}

function StartNode({ data }: { data: StartNodeData }) {
  const triggerColors = data.triggerService ? data.serviceColors.get(data.triggerService) : undefined
  const isTriggerHighlighted = data.highlightedService === data.triggerService && data.triggerService != null
  const dimmedByService = data.highlightedService && !isTriggerHighlighted
  const dimmedBySaga = data.highlightedSaga && data.highlightedSaga !== data.sagaName
  const dimmed = dimmedByService || dimmedBySaga

  return (
    <>
      <div
        className={`flex flex-col items-center justify-center rounded-full border-2 border-success bg-success-muted px-4 py-2 transition-opacity ${dimmed ? 'opacity-30' : 'opacity-100'}`}
        style={{
          ...(isTriggerHighlighted && triggerColors
            ? { borderColor: triggerColors.fg, boxShadow: `0 0 0 2px ${triggerColors.fg}`, outline: `2px solid ${triggerColors.fg}`, outlineOffset: '2px' }
            : {}),
          ...(data.highlightedSaga === data.sagaName && data.sagaColor
            ? { borderColor: data.sagaColor, boxShadow: `0 0 0 2px ${data.sagaColor}` }
            : {}),
        }}
      >
        <span className="text-xs font-semibold text-success-foreground">{data.label}</span>
        {data.trigger && (
          <span
            className="inline-flex items-center rounded-full px-1.5 py-0.5 text-[10px] font-medium mt-0.5"
            style={triggerColors
              ? { backgroundColor: triggerColors.bg, color: triggerColors.fg }
              : {}}
          >
            {data.trigger}
          </span>
        )}
      </div>
      <Handle type="source" position={data.direction === 'TB' ? Position.Bottom : Position.Right} className="bg-success! border-0! w-2! h-2!" />
    </>
  )
}

interface StepNodeData {
  label: string
  serviceCalls: { service: string; method: string }[]
  serviceColors: Map<string, { bg: string; fg: string }>
  highlightedService: string | null
  sagaName: string
  highlightedSaga: string | null
  sagaColor: string | undefined
  direction: FlowDirection
  [key: string]: unknown
}

function StepNode({ data }: { data: StepNodeData }) {
  const primaryService = data.serviceCalls[0]?.service
  const primaryColors = primaryService ? data.serviceColors.get(primaryService) : undefined
  const borderColor = primaryColors?.fg ?? '#71717a'

  const usesHighlighted = data.highlightedService
    ? data.serviceCalls.some((c) => c.service === data.highlightedService)
    : true
  const dimmedByService = data.highlightedService && !usesHighlighted
  const dimmedBySaga = data.highlightedSaga && data.highlightedSaga !== data.sagaName
  const dimmed = dimmedByService || dimmedBySaga

  const activeBorder = data.highlightedSaga === data.sagaName && data.sagaColor
    ? data.sagaColor
    : (data.highlightedService && usesHighlighted ? borderColor : undefined)

  return (
    <>
      <Handle type="target" position={data.direction === 'TB' ? Position.Top : Position.Left} className="bg-transparent! border-0! w-0! h-0!" />
      <div
        className={`flex flex-col gap-1 rounded-lg border-2 bg-background px-3 py-2 shadow-sm min-w-[140px] sm:min-w-[180px] transition-opacity ${dimmed ? 'opacity-30' : 'opacity-100'}`}
        style={{
          borderColor,
          ...(activeBorder
            ? { boxShadow: `0 0 0 2px ${activeBorder}`, outline: `2px solid ${activeBorder}`, outlineOffset: '2px' }
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
      <Handle type="source" position={data.direction === 'TB' ? Position.Bottom : Position.Right} className="bg-transparent! border-0! w-0! h-0!" />
    </>
  )
}

interface DecisionNodeData {
  label: string
  sagaName: string
  highlightedSaga: string | null
  direction: FlowDirection
  [key: string]: unknown
}

function DecisionNode({ data }: { data: DecisionNodeData }) {
  const dimmed = data.highlightedSaga && data.highlightedSaga !== data.sagaName
  return (
    <>
      <Handle type="target" position={data.direction === 'TB' ? Position.Top : Position.Left} className="bg-transparent! border-0! w-0! h-0!" />
      <div
        className={`flex items-center justify-center border-2 border-warning bg-warning-muted transition-opacity ${dimmed ? 'opacity-30' : 'opacity-100'}`}
        style={{
          width: 120,
          height: 80,
          clipPath: 'polygon(50% 0%, 100% 50%, 50% 100%, 0% 50%)',
        }}
      >
        <span className="text-[10px] font-medium text-warning-foreground text-center leading-tight px-4 max-w-[90px]">
          {data.label}
        </span>
      </div>
      <Handle type="source" position={data.direction === 'TB' ? Position.Right : Position.Bottom} id="exit" className="bg-transparent! border-0! w-0! h-0!" />
      <Handle type="source" position={data.direction === 'TB' ? Position.Bottom : Position.Right} id="no" className="bg-transparent! border-0! w-0! h-0!" />
    </>
  )
}

interface ExitNodeData {
  label: string
  sagaName: string
  highlightedSaga: string | null
  direction: FlowDirection
  [key: string]: unknown
}

function ExitNode({ data }: { data: ExitNodeData }) {
  const dimmed = data.highlightedSaga && data.highlightedSaga !== data.sagaName
  return (
    <>
      <Handle type="target" position={data.direction === 'TB' ? Position.Top : Position.Left} className="bg-transparent! border-0! w-0! h-0!" />
      <div className={`flex items-center justify-center rounded-full border-2 border-destructive bg-destructive/10 px-3 py-1.5 transition-opacity ${dimmed ? 'opacity-30' : 'opacity-100'}`}>
        <span className="text-[10px] font-semibold text-destructive">{data.label}</span>
      </div>
    </>
  )
}

interface EndNodeData {
  sagaName: string
  highlightedSaga: string | null
  direction: FlowDirection
  [key: string]: unknown
}

function EndNode({ data }: { data: EndNodeData }) {
  const dimmed = data.highlightedSaga && data.highlightedSaga !== data.sagaName
  return (
    <>
      <Handle type="target" position={data.direction === 'TB' ? Position.Top : Position.Left} className="bg-transparent! border-0! w-0! h-0!" />
      <div className={`flex items-center justify-center rounded-full border-2 border-border bg-muted px-4 py-2 transition-opacity ${dimmed ? 'opacity-30' : 'opacity-100'}`}>
        <span className="text-xs font-semibold text-muted-foreground">COMPLETED</span>
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

export type FlowDirection = 'LR' | 'TB'

function layoutNodes(nodes: Node[], edges: Edge[], direction: FlowDirection = 'LR'): Node[] {
  const g = new Dagre.graphlib.Graph().setDefaultEdgeLabel(() => ({}))
  g.setGraph({ rankdir: direction, nodesep: 60, ranksep: 100 })

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

// --- Build Combined Graph from multiple SagaFlows ---

function buildCombinedFlowGraph(
  flows: SagaFlow[],
  serviceColors: Map<string, { bg: string; fg: string }>,
  sagaColors: Map<string, string>,
  highlightedService: string | null,
  highlightedSaga: string | null,
  direction: FlowDirection = 'LR',
): { nodes: Node[]; edges: Edge[] } {
  const nodes: Node[] = []
  const edges: Edge[] = []

  for (let fi = 0; fi < flows.length; fi++) {
    const flow = flows[fi]
    const prefix = flows.length > 1 ? `s${fi}-` : ''
    const triggerService = parseTriggerService(flow.trigger)
    const sagaColor = sagaColors.get(flow.name)

    // Start node
    nodes.push({
      id: `${prefix}start`,
      type: 'sagaStart',
      position: { x: 0, y: 0 },
      data: {
        label: flow.name,
        trigger: flow.trigger,
        triggerService,
        serviceColors,
        highlightedService,
        sagaName: flow.name,
        highlightedSaga,
        sagaColor,
        direction,
      } satisfies StartNodeData,
    })

    if (flow.steps.length === 0) {
      nodes.push({
        id: `${prefix}end`,
        type: 'sagaEnd',
        position: { x: 0, y: 0 },
        data: { sagaName: flow.name, highlightedSaga, direction } satisfies EndNodeData,
      })
      edges.push({ id: `${prefix}start-end`, source: `${prefix}start`, target: `${prefix}end` })
      continue
    }

    let prevId: string | null = `${prefix}start`

    for (let i = 0; i < flow.steps.length; i++) {
      const step = flow.steps[i]
      const stepId = `${prefix}step-${i}`
      const nextId = i + 1 < flow.steps.length ? `${prefix}step-${i + 1}` : `${prefix}end`

      nodes.push({
        id: stepId,
        type: 'sagaStep',
        position: { x: 0, y: 0 },
        data: {
          label: step.name,
          serviceCalls: step.serviceCalls,
          serviceColors,
          highlightedService,
          sagaName: flow.name,
          highlightedSaga,
          sagaColor,
          direction,
        } satisfies StepNodeData,
      })

      if (prevId) {
        edges.push({
          id: `${prevId}-${stepId}`,
          source: prevId,
          target: stepId,
        })
      }

      if (step.earlyExit) {
        const decisionId = `${prefix}decision-${i}`
        const exitId = `${prefix}exit-${i}`

        nodes.push({
          id: decisionId,
          type: 'sagaDecision',
          position: { x: 0, y: 0 },
          data: { label: step.earlyExit.condition, sagaName: flow.name, highlightedSaga, direction } satisfies DecisionNodeData,
        })

        nodes.push({
          id: exitId,
          type: 'sagaExit',
          position: { x: 0, y: 0 },
          data: { label: step.earlyExit.returnStatus, sagaName: flow.name, highlightedSaga, direction } satisfies ExitNodeData,
        })

        edges.push({
          id: `${stepId}-${decisionId}`,
          source: stepId,
          target: decisionId,
        })

        edges.push({
          id: `${decisionId}-${exitId}`,
          source: decisionId,
          sourceHandle: 'exit',
          target: exitId,
          label: 'Yes',
          style: { stroke: '#ef4444', strokeDasharray: '6 3' },
        })

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
      id: `${prefix}end`,
      type: 'sagaEnd',
      position: { x: 0, y: 0 },
      data: { sagaName: flow.name, highlightedSaga, direction } satisfies EndNodeData,
    })

    if (prevId) {
      edges.push({
        id: `${prevId}-${prefix}end`,
        source: prevId,
        target: `${prefix}end`,
      })
    }
  }

  return { nodes: layoutNodes(nodes, edges, direction), edges }
}

// --- Component ---

interface SagaFlowDiagramProps {
  flows: SagaFlow[]
  onStepClick?: (stepName: string, lineNumber: number) => void
  className?: string
  direction?: FlowDirection
}

export function SagaFlowDiagram({ flows, onStepClick, className, direction = 'LR' }: SagaFlowDiagramProps) {
  const [highlightedService, setHighlightedService] = useState<string | null>(null)
  const [highlightedSaga, setHighlightedSaga] = useState<string | null>(null)
  const [legendOpen, setLegendOpen] = useState(true)

  const serviceColors = useMemo(() => buildServiceColorMap(flows), [flows])
  const sagaColors = useMemo(() => buildSagaColorMap(flows), [flows])

  const effectiveServiceHighlight = highlightedService && serviceColors.has(highlightedService)
    ? highlightedService
    : null

  const effectiveSagaHighlight = highlightedSaga && sagaColors.has(highlightedSaga)
    ? highlightedSaga
    : null

  const { nodes, edges } = useMemo(
    () => buildCombinedFlowGraph(flows, serviceColors, sagaColors, effectiveServiceHighlight, effectiveSagaHighlight, direction),
    [flows, serviceColors, sagaColors, effectiveServiceHighlight, effectiveSagaHighlight, direction],
  )

  const services = useMemo(() => [...serviceColors.keys()].sort(), [serviceColors])

  // Collect all trigger services for labeling
  const triggerServices = useMemo(() => {
    const set = new Set<string>()
    for (const flow of flows) {
      const svc = parseTriggerService(flow.trigger)
      if (svc) set.add(svc)
    }
    return set
  }, [flows])

  return (
    <div className={`relative ${className ?? ''}`} style={{ width: '100%', height: '100%', minHeight: 300 }}>
      <ReactFlow
        nodes={nodes}
        edges={edges}
        nodeTypes={nodeTypes}
        onNodeClick={(_event, node) => {
          if (node.type === 'sagaStep' && onStepClick) {
            // Extract step index — node id is either "step-N" or "sN-step-N"
            const parts = node.id.split('step-')
            const stepIndex = parseInt(parts[parts.length - 1], 10)
            // Find which flow this node belongs to
            const sagaName = (node.data as StepNodeData).sagaName
            const flow = flows.find((f) => f.name === sagaName)
            const step = flow?.steps[stepIndex]
            if (step) onStepClick(step.name, step.lineNumber)
          }
        }}
        fitView
        proOptions={{ hideAttribution: true }}
        nodesDraggable={false}
        nodesConnectable={false}
      >
        <Controls showInteractive={false} position="bottom-right" />
        <Background variant={BackgroundVariant.Dots} gap={16} size={1} />
      </ReactFlow>

      {/* Legend panel — collapsible to avoid overlapping diagram on small screens */}
      <div className="absolute bottom-3 left-3 z-10">
        {legendOpen ? (
          <div className="flex flex-col gap-2 rounded-lg border bg-background/95 p-3 backdrop-blur-sm shadow-sm max-w-[200px]">
            <button
              type="button"
              className="text-[10px] text-muted-foreground/60 self-end hover:text-muted-foreground"
              onClick={() => setLegendOpen(false)}
              aria-label="Collapse legend"
            >
              hide
            </button>
            {/* Saga filter (only shown for multi-saga patterns) */}
            {flows.length > 1 && (
              <div className="flex flex-col gap-1">
                <span className="text-xs font-semibold text-muted-foreground uppercase tracking-wider">Sagas</span>
                {flows.map((flow) => {
                  const color = sagaColors.get(flow.name)
                  const isActive = effectiveSagaHighlight === flow.name
                  return (
                    <button
                      key={flow.name}
                      type="button"
                      aria-pressed={isActive}
                      className={`flex items-center gap-2 cursor-pointer rounded px-1 -mx-1 transition-colors hover:bg-muted/50 ${isActive ? 'font-semibold' : ''}`}
                      onClick={() => {
                        setHighlightedSaga((prev) => (prev === flow.name ? null : flow.name))
                        setHighlightedService(null)
                      }}
                    >
                      <span
                        className={`inline-block h-2.5 w-2.5 rounded-sm ${isActive ? 'ring-2 ring-offset-1' : ''}`}
                        style={{ backgroundColor: color }}
                      />
                      <span className="text-xs text-muted-foreground truncate">{flow.name}</span>
                    </button>
                  )
                })}
              </div>
            )}

            {/* Service filter */}
            {services.length > 0 && (
              <div className="flex flex-col gap-1">
                {flows.length > 1 && <div className="border-t my-1" />}
                <span className="text-xs font-semibold text-muted-foreground uppercase tracking-wider">Services</span>
                {services.map((svc) => {
                  const colors = serviceColors.get(svc)
                  const isActive = effectiveServiceHighlight === svc
                  return (
                    <button
                      key={svc}
                      type="button"
                      aria-pressed={isActive}
                      className={`flex items-center gap-2 cursor-pointer rounded px-1 -mx-1 transition-colors hover:bg-muted/50 ${isActive ? 'font-semibold' : ''}`}
                      onClick={() => {
                        setHighlightedService((prev) => (prev === svc ? null : svc))
                        setHighlightedSaga(null)
                      }}
                    >
                      <span
                        className={`inline-block h-2.5 w-2.5 rounded-full ${isActive ? 'ring-2 ring-offset-1' : ''}`}
                        style={{ backgroundColor: colors?.fg }}
                      />
                      <span className="text-xs text-muted-foreground">{svc}</span>
                      {triggerServices.has(svc) && (
                        <span className="text-[9px] text-muted-foreground/60 italic">trigger</span>
                      )}
                    </button>
                  )
                })}
              </div>
            )}
          </div>
        ) : (
          <button
            type="button"
            className="rounded-lg border bg-background/95 px-2.5 py-1.5 text-xs text-muted-foreground backdrop-blur-sm shadow-sm hover:text-foreground transition-colors"
            onClick={() => setLegendOpen(true)}
            aria-label="Show legend"
          >
            Legend
          </button>
        )}
      </div>
    </div>
  )
}
