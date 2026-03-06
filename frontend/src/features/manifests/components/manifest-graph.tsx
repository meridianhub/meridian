import { useCallback, useEffect, useMemo, useState } from 'react'
import {
  ReactFlow,
  Controls,
  Background,
  MiniMap,
  useNodesState,
  useEdgesState,
  type Node,
  type Edge,
  type NodeMouseHandler,
  BackgroundVariant,
  Position,
  Handle,
} from '@xyflow/react'
import '@xyflow/react/dist/style.css'
import { useNavigate } from 'react-router-dom'
import {
  Tooltip,
  TooltipContent,
  TooltipProvider,
  TooltipTrigger,
} from '@/components/ui/tooltip'
import {
  layoutWithELK,
  NODE_WIDTH,
  NODE_BASE_HEIGHT,
  NODE_PADDING,
} from '@/lib/visualization/graph-layout'
import {
  buildManifestGraph,
  type ManifestNode,
  type ManifestEdge,
  type ManifestNodeType,
  type ManifestGraph as ManifestGraphModel,
} from '../lib/manifest-graph-model'
import type { Manifest } from '@/api/gen/meridian/control_plane/v1/manifest_pb'

// Theme colors per node type
const NODE_THEMES: Record<ManifestNodeType, { color: string; label: string }> = {
  instrument: { color: '#3b82f6', label: 'Instruments' },
  account_type: { color: '#22c55e', label: 'Account Types' },
  valuation_rule: { color: '#f59e0b', label: 'Valuation Rules' },
  saga: { color: '#8b5cf6', label: 'Sagas' },
}

// Edge styles per relationship type
const MANIFEST_EDGE_STYLES: Record<string, React.CSSProperties> = {
  allowed_by: { stroke: '#3b82f6', strokeWidth: 2 },
  converts_from: { stroke: '#f59e0b', strokeWidth: 1.5, strokeDasharray: '6 3' },
  converts_to: { stroke: '#f59e0b', strokeWidth: 1.5 },
}

const EDGE_LEGEND: { label: string; color: string; dashed?: boolean }[] = [
  { label: 'Allowed by', color: '#3b82f6' },
  { label: 'Converts from', color: '#f59e0b', dashed: true },
  { label: 'Converts to', color: '#f59e0b' },
]

// ELK layer ordering: instruments at top, account types below, valuation rules middle, sagas bottom
const LAYER_ORDER: Record<ManifestNodeType, number> = {
  instrument: 0,
  account_type: 1,
  valuation_rule: 2,
  saga: 3,
}

// Trigger type display
function getTriggerBadge(trigger: string): { label: string; variant: string } {
  if (trigger.startsWith('event:')) return { label: 'event', variant: 'bg-purple-100 text-purple-800' }
  if (trigger.startsWith('scheduled:')) return { label: 'scheduled', variant: 'bg-blue-100 text-blue-800' }
  if (trigger.startsWith('api:')) return { label: 'api', variant: 'bg-green-100 text-green-800' }
  return { label: 'unknown', variant: 'bg-gray-100 text-gray-800' }
}

// Custom node data interface
interface ManifestNodeData {
  manifestNode: ManifestNode
  color: string
  highlighted: boolean
  dimmed: boolean
  [key: string]: unknown
}

function InstrumentNode({ data }: { data: ManifestNodeData }) {
  const node = data.manifestNode
  const code = node.data.code as string
  const unit = (node.data.dimensions as Record<string, unknown> | undefined)?.unit as string | undefined
  return (
    <>
      <Handle type="target" position={Position.Top} className="!bg-transparent !border-0 !w-0 !h-0" />
      <Tooltip>
        <TooltipTrigger asChild>
          <div
            className="flex flex-col items-center justify-center rounded-lg border-2 px-3 py-2 text-center transition-opacity duration-150 cursor-pointer"
            style={{
              width: 180,
              borderColor: data.color,
              backgroundColor: `${data.color}18`,
              opacity: data.dimmed ? 0.25 : 1,
              boxShadow: data.highlighted ? `0 0 12px ${data.color}88` : undefined,
            }}
          >
            <span className="text-[11px] font-bold font-mono text-foreground">{code}</span>
            <span className="text-[10px] text-muted-foreground truncate w-full">{node.label}</span>
            {unit && <span className="text-[9px] text-muted-foreground">({unit})</span>}
          </div>
        </TooltipTrigger>
        <TooltipContent side="top">{node.label} ({code})</TooltipContent>
      </Tooltip>
      <Handle type="source" position={Position.Bottom} className="!bg-transparent !border-0 !w-0 !h-0" />
    </>
  )
}

function AccountTypeNode({ data }: { data: ManifestNodeData }) {
  const node = data.manifestNode
  const code = node.data.code as string
  const allowedCount = (node.data.allowedInstruments as string[] | undefined)?.length ?? 0
  return (
    <>
      <Handle type="target" position={Position.Top} className="!bg-transparent !border-0 !w-0 !h-0" />
      <Tooltip>
        <TooltipTrigger asChild>
          <div
            className="flex flex-col items-center justify-center rounded-lg border-2 px-3 py-2 text-center transition-opacity duration-150 cursor-pointer"
            style={{
              width: 180,
              borderColor: data.color,
              backgroundColor: `${data.color}18`,
              opacity: data.dimmed ? 0.25 : 1,
              boxShadow: data.highlighted ? `0 0 12px ${data.color}88` : undefined,
            }}
          >
            <span className="text-[11px] font-bold font-mono text-foreground">{code}</span>
            <span className="text-[10px] text-muted-foreground truncate w-full">{node.label}</span>
            <span className="text-[9px] text-muted-foreground">{allowedCount} instrument{allowedCount !== 1 ? 's' : ''}</span>
          </div>
        </TooltipTrigger>
        <TooltipContent side="top">{node.label} ({code})</TooltipContent>
      </Tooltip>
      <Handle type="source" position={Position.Bottom} className="!bg-transparent !border-0 !w-0 !h-0" />
    </>
  )
}

function ValuationRuleNode({ data }: { data: ManifestNodeData }) {
  const node = data.manifestNode
  const from = node.data.fromInstrument as string
  const to = node.data.toInstrument as string
  return (
    <>
      <Handle type="target" position={Position.Top} className="!bg-transparent !border-0 !w-0 !h-0" />
      <Tooltip>
        <TooltipTrigger asChild>
          <div
            className="flex flex-col items-center justify-center rounded-lg border-2 px-3 py-2 text-center transition-opacity duration-150"
            style={{
              width: 180,
              borderColor: data.color,
              backgroundColor: `${data.color}18`,
              opacity: data.dimmed ? 0.25 : 1,
              boxShadow: data.highlighted ? `0 0 12px ${data.color}88` : undefined,
            }}
          >
            <span className="text-[10px] font-semibold text-foreground">{from} &rarr; {to}</span>
          </div>
        </TooltipTrigger>
        <TooltipContent side="top">Valuation: {from} to {to}</TooltipContent>
      </Tooltip>
      <Handle type="source" position={Position.Bottom} className="!bg-transparent !border-0 !w-0 !h-0" />
    </>
  )
}

function SagaNode({ data }: { data: ManifestNodeData }) {
  const node = data.manifestNode
  const trigger = node.data.trigger as string
  const badge = getTriggerBadge(trigger)
  return (
    <>
      <Handle type="target" position={Position.Top} className="!bg-transparent !border-0 !w-0 !h-0" />
      <Tooltip>
        <TooltipTrigger asChild>
          <div
            className="flex flex-col items-center justify-center rounded-lg border-2 px-3 py-2 text-center transition-opacity duration-150 cursor-pointer"
            style={{
              width: 180,
              borderColor: data.color,
              backgroundColor: `${data.color}18`,
              opacity: data.dimmed ? 0.25 : 1,
              boxShadow: data.highlighted ? `0 0 12px ${data.color}88` : undefined,
            }}
          >
            <span className="text-[11px] font-bold text-foreground truncate w-full">{node.label}</span>
            <span className={`mt-0.5 text-[9px] font-medium px-1.5 py-0.5 rounded-full ${badge.variant}`}>
              {badge.label}
            </span>
          </div>
        </TooltipTrigger>
        <TooltipContent side="top">{node.label} ({trigger})</TooltipContent>
      </Tooltip>
      <Handle type="source" position={Position.Bottom} className="!bg-transparent !border-0 !w-0 !h-0" />
    </>
  )
}

const nodeTypes = {
  instrument: InstrumentNode,
  account_type: AccountTypeNode,
  valuation_rule: ValuationRuleNode,
  saga: SagaNode,
}

function buildReactFlowEdges(manifestEdges: ManifestEdge[]): Edge[] {
  return manifestEdges.map((e) => ({
    id: e.id,
    source: e.source,
    target: e.target,
    style: MANIFEST_EDGE_STYLES[e.relationship] ?? {},
    markerEnd: e.relationship === 'converts_to' || e.relationship === 'allowed_by'
      ? { type: 'arrowclosed' as const, color: (MANIFEST_EDGE_STYLES[e.relationship]?.stroke as string) ?? '#999' }
      : undefined,
    data: { relationship: e.relationship },
  }))
}

async function layoutManifestGraph(
  graph: ManifestGraphModel,
  visibleTypes: Set<ManifestNodeType>,
): Promise<{ nodes: Node[]; edges: Edge[] }> {
  const filteredNodes = graph.nodes.filter((n) => visibleTypes.has(n.type))
  const filteredNodeIds = new Set(filteredNodes.map((n) => n.id))
  const filteredEdges = graph.edges.filter(
    (e) => filteredNodeIds.has(e.source) && filteredNodeIds.has(e.target),
  )

  if (filteredNodes.length === 0) {
    return { nodes: [], edges: [] }
  }

  const nodeMap = new Map(filteredNodes.map((n) => [n.id, n]))

  const layoutNodes = filteredNodes.map((n) => ({
    id: n.id,
    width: NODE_WIDTH,
    height: NODE_BASE_HEIGHT + NODE_PADDING,
  }))

  const rfEdges = buildReactFlowEdges(filteredEdges)

  const rfNodes = await layoutWithELK<ManifestNodeData>(
    layoutNodes,
    rfEdges,
    (id, position) => {
      const mn = nodeMap.get(id)!
      const color = NODE_THEMES[mn.type].color
      return {
        id,
        type: mn.type,
        position,
        data: {
          manifestNode: mn,
          color,
          highlighted: false,
          dimmed: false,
        } satisfies ManifestNodeData,
      }
    },
    {
      direction: 'DOWN',
      nodeNodeSpacing: '50',
      layerSpacing: '80',
    },
  )

  // Sort by layer order for ELK placement hint
  rfNodes.sort((a, b) => {
    const aType = nodeMap.get(a.id)!.type
    const bType = nodeMap.get(b.id)!.type
    return LAYER_ORDER[aType] - LAYER_ORDER[bType]
  })

  return { nodes: rfNodes, edges: rfEdges }
}

function LegendItem({ label, color, dashed }: { label: string; color: string; dashed?: boolean }) {
  return (
    <div className="flex items-center gap-2">
      <svg width="32" height="12">
        <line
          x1="0" y1="6" x2="32" y2="6"
          stroke={color}
          strokeWidth={2}
          strokeDasharray={dashed ? '6 3' : undefined}
        />
      </svg>
      <span className="text-xs text-muted-foreground">{label}</span>
    </div>
  )
}

interface ManifestGraphProps {
  manifest: Manifest
  className?: string
}

export function ManifestGraph({ manifest, className }: ManifestGraphProps) {
  const navigate = useNavigate()
  const [nodes, setNodes, onNodesChange] = useNodesState<Node>([])
  const [edges, setEdges, onEdgesChange] = useEdgesState<Edge>([])
  const [hoveredNode, setHoveredNode] = useState<string | null>(null)
  const [visibleTypes, setVisibleTypes] = useState<Set<ManifestNodeType>>(
    () => new Set<ManifestNodeType>(['instrument', 'account_type', 'valuation_rule', 'saga']),
  )

  const graph = useMemo(() => buildManifestGraph(manifest), [manifest])

  const nodeCountByType = useMemo(() => {
    const counts: Record<ManifestNodeType, number> = {
      instrument: 0,
      account_type: 0,
      valuation_rule: 0,
      saga: 0,
    }
    for (const n of graph.nodes) {
      counts[n.type]++
    }
    return counts
  }, [graph])

  // Memoize filtered edges for hover highlighting
  const currentEdges = useMemo(() => {
    const filteredNodeIds = new Set(
      graph.nodes.filter((n) => visibleTypes.has(n.type)).map((n) => n.id),
    )
    return graph.edges.filter(
      (e) => filteredNodeIds.has(e.source) && filteredNodeIds.has(e.target),
    )
  }, [graph, visibleTypes])

  // Layout
  useEffect(() => {
    let cancelled = false
    void layoutManifestGraph(graph, visibleTypes)
      .then((result) => {
        if (!cancelled) {
          setNodes(result.nodes)
          setEdges(result.edges)
        }
      })
      .catch((err) => {
        if (!cancelled) {
          console.error('[ManifestGraph] layout failed:', err)
        }
      })
    return () => { cancelled = true }
  }, [graph, visibleTypes, setNodes, setEdges])

  // Hover highlighting
  useEffect(() => {
    const connectedNodes = new Set<string>()
    if (hoveredNode) {
      connectedNodes.add(hoveredNode)
      for (const e of currentEdges) {
        if (e.source === hoveredNode || e.target === hoveredNode) {
          connectedNodes.add(e.source)
          connectedNodes.add(e.target)
        }
      }
    }

    setNodes((nds) => {
      let changed = false
      const next = nds.map((n) => {
        const highlighted = hoveredNode ? n.id === hoveredNode : false
        const dimmed = hoveredNode ? !connectedNodes.has(n.id) : false
        const current = n.data as ManifestNodeData
        if (current.highlighted === highlighted && current.dimmed === dimmed) return n
        changed = true
        return { ...n, data: { ...n.data, highlighted, dimmed } }
      })
      return changed ? next : nds
    })

    setEdges((eds) => {
      let changed = false
      const next = eds.map((e) => {
        const animated = hoveredNode ? e.source === hoveredNode || e.target === hoveredNode : false
        if (e.animated === animated) return e
        changed = true
        return { ...e, animated }
      })
      return changed ? next : eds
    })
  }, [hoveredNode, currentEdges, setNodes, setEdges])

  const onNodeClick: NodeMouseHandler = useCallback(
    (_event, node) => {
      const data = node.data as ManifestNodeData
      const mn = data.manifestNode
      switch (mn.type) {
        case 'instrument':
          navigate('/reference-data/instruments')
          break
        case 'account_type':
          navigate('/reference-data/account-types')
          break
        case 'saga':
          navigate(`/sagas/${mn.label}`)
          break
      }
    },
    [navigate],
  )

  const onNodeMouseEnter: NodeMouseHandler = useCallback((_event, node) => {
    setHoveredNode(node.id)
  }, [])

  const onNodeMouseLeave: NodeMouseHandler = useCallback(() => {
    setHoveredNode(null)
  }, [])

  const toggleType = useCallback((type: ManifestNodeType) => {
    setVisibleTypes((prev) => {
      const next = new Set(prev)
      if (next.has(type)) {
        next.delete(type)
      } else {
        next.add(type)
      }
      return next
    })
  }, [])

  const totalVisible = nodes.length

  if (graph.nodes.length === 0) {
    return (
      <div className={className} data-testid="manifest-graph-empty" style={{ width: '100%', height: '100%' }}>
        <div className="flex items-center justify-center h-full text-muted-foreground text-sm">
          No elements in manifest to visualize.
        </div>
      </div>
    )
  }

  return (
    <div className={className} style={{ width: '100%', height: '100%', position: 'relative' }}>
      <TooltipProvider>
        <ReactFlow
          nodes={nodes}
          edges={edges}
          onNodesChange={onNodesChange}
          onEdgesChange={onEdgesChange}
          onNodeClick={onNodeClick}
          onNodeMouseEnter={onNodeMouseEnter}
          onNodeMouseLeave={onNodeMouseLeave}
          nodeTypes={nodeTypes}
          fitView
          proOptions={{ hideAttribution: true }}
        >
          <Controls />
          <Background variant={BackgroundVariant.Dots} gap={16} size={1} />
          <MiniMap
            nodeColor={(n) => (n.data as ManifestNodeData).color}
            maskColor="rgba(0, 0, 0, 0.15)"
          />
        </ReactFlow>
      </TooltipProvider>

      {/* Filter sidebar */}
      <div className="absolute top-3 left-3 z-10 flex flex-col gap-2 rounded-lg border bg-background/95 p-3 backdrop-blur-sm shadow-sm">
        <span className="text-xs font-semibold text-muted-foreground uppercase tracking-wider">Element Types</span>
        {(Object.keys(NODE_THEMES) as ManifestNodeType[]).map((type) => {
          const theme = NODE_THEMES[type]
          const count = nodeCountByType[type]
          return (
            <label key={type} className="flex items-center gap-2 cursor-pointer">
              <input
                type="checkbox"
                checked={visibleTypes.has(type)}
                onChange={() => toggleType(type)}
                className="rounded"
                aria-label={`Show ${theme.label}`}
              />
              <span
                className="w-2.5 h-2.5 rounded-full"
                style={{ backgroundColor: theme.color }}
              />
              <span className="text-xs text-foreground">{theme.label}</span>
              <span className="text-[10px] text-muted-foreground">({count})</span>
            </label>
          )
        })}
        <span className="text-[10px] text-muted-foreground mt-1">{totalVisible} nodes visible</span>
      </div>

      {/* Legend */}
      <div className="absolute bottom-3 left-3 z-10 flex flex-col gap-1 rounded-lg border bg-background/95 p-3 backdrop-blur-sm shadow-sm">
        <span className="text-xs font-semibold text-muted-foreground uppercase tracking-wider mb-1">Edges</span>
        {EDGE_LEGEND.map((item) => (
          <LegendItem key={item.label} {...item} />
        ))}
      </div>
    </div>
  )
}
