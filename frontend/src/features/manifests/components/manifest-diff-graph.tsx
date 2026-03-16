import { memo, useEffect, useMemo } from 'react'
import {
  ReactFlow,
  Controls,
  Background,
  useNodesState,
  useEdgesState,
  type Node,
  type Edge,
  BackgroundVariant,
  Position,
  Handle,
} from '@xyflow/react'
import '@xyflow/react/dist/style.css'
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
import type {
  ManifestNode,
  ManifestEdge,
  ManifestGraph,
} from '../lib/manifest-graph-model'
import { computeManifestDiff, type ManifestDiff } from '../lib/manifest-diff'
import { getLayerPriority } from '../lib/node-type-registry'

type DiffStatus = 'added' | 'removed' | 'modified' | 'unchanged'

const DIFF_COLORS: Record<DiffStatus, { border: string; bg: string }> = {
  added: { border: 'var(--graph-diff-added)', bg: 'color-mix(in oklch, var(--graph-diff-added) 10%, transparent)' },
  removed: { border: 'var(--graph-diff-removed)', bg: 'color-mix(in oklch, var(--graph-diff-removed) 10%, transparent)' },
  modified: { border: 'var(--graph-diff-modified)', bg: 'color-mix(in oklch, var(--graph-diff-modified) 10%, transparent)' },
  unchanged: { border: 'var(--graph-diff-unchanged)', bg: 'color-mix(in oklch, var(--graph-diff-unchanged) 6%, transparent)' },
}

const LAYER_PRIORITY = getLayerPriority()

interface DiffNodeData {
  manifestNode: ManifestNode
  diffStatus: DiffStatus
  [key: string]: unknown
}

// Trigger badge helper (mirrors manifest-graph.tsx)
function getTriggerBadge(trigger: string): { label: string; variant: string } {
  if (trigger.startsWith('event:')) return { label: 'event', variant: 'bg-accent text-accent-foreground' }
  if (trigger.startsWith('scheduled:')) return { label: 'scheduled', variant: 'bg-info-muted text-info-foreground' }
  if (trigger.startsWith('api:')) return { label: 'api', variant: 'bg-success-muted text-success-foreground' }
  return { label: 'unknown', variant: 'bg-muted text-muted-foreground' }
}

// Shared container style builder for all diff nodes
function useDiffContainerStyle(diffStatus: DiffStatus) {
  const colors = DIFF_COLORS[diffStatus]
  return useMemo(() => ({
    width: 180,
    borderColor: colors.border,
    backgroundColor: colors.bg,
    textDecoration: diffStatus === 'removed' ? 'line-through' as const : undefined,
  }), [colors.border, colors.bg, diffStatus])
}

const DiffInstrumentNode = memo(function DiffInstrumentNode({ data }: { data: DiffNodeData }) {
  const node = data.manifestNode
  const code = node.data.code as string
  const unit = (node.data.dimensions as Record<string, unknown> | undefined)?.unit as string | undefined
  const containerStyle = useDiffContainerStyle(data.diffStatus)

  return (
    <>
      <Handle type="target" position={Position.Top} className="!bg-transparent !border-0 !w-0 !h-0" />
      <Tooltip>
        <TooltipTrigger asChild>
          <div
            className="flex flex-col items-center justify-center rounded-lg border-2 px-3 py-2 text-center"
            style={containerStyle}
            data-testid={`diff-node-${data.diffStatus}`}
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
})

const DiffAccountTypeNode = memo(function DiffAccountTypeNode({ data }: { data: DiffNodeData }) {
  const node = data.manifestNode
  const code = node.data.code as string
  const containerStyle = useDiffContainerStyle(data.diffStatus)

  return (
    <>
      <Handle type="target" position={Position.Top} className="!bg-transparent !border-0 !w-0 !h-0" />
      <Tooltip>
        <TooltipTrigger asChild>
          <div
            className="flex flex-col items-center justify-center rounded-lg border-2 px-3 py-2 text-center"
            style={containerStyle}
            data-testid={`diff-node-${data.diffStatus}`}
          >
            <span className="text-[11px] font-bold font-mono text-foreground">{code}</span>
            <span className="text-[10px] text-muted-foreground truncate w-full">{node.label}</span>
          </div>
        </TooltipTrigger>
        <TooltipContent side="top">{node.label} ({code})</TooltipContent>
      </Tooltip>
      <Handle type="source" position={Position.Bottom} className="!bg-transparent !border-0 !w-0 !h-0" />
    </>
  )
})

const DiffValuationRuleNode = memo(function DiffValuationRuleNode({ data }: { data: DiffNodeData }) {
  const node = data.manifestNode
  const from = node.data.fromInstrument as string
  const to = node.data.toInstrument as string
  const containerStyle = useDiffContainerStyle(data.diffStatus)

  return (
    <>
      <Handle type="target" position={Position.Top} className="!bg-transparent !border-0 !w-0 !h-0" />
      <Tooltip>
        <TooltipTrigger asChild>
          <div
            className="flex flex-col items-center justify-center rounded-lg border-2 px-3 py-2 text-center"
            style={containerStyle}
            data-testid={`diff-node-${data.diffStatus}`}
          >
            <span className="text-[10px] font-semibold text-foreground">{from} &rarr; {to}</span>
          </div>
        </TooltipTrigger>
        <TooltipContent side="top">Valuation: {from} to {to}</TooltipContent>
      </Tooltip>
      <Handle type="source" position={Position.Bottom} className="!bg-transparent !border-0 !w-0 !h-0" />
    </>
  )
})

const DiffSagaNode = memo(function DiffSagaNode({ data }: { data: DiffNodeData }) {
  const node = data.manifestNode
  const trigger = node.data.trigger as string
  const badge = getTriggerBadge(trigger)
  const containerStyle = useDiffContainerStyle(data.diffStatus)

  return (
    <>
      <Handle type="target" position={Position.Top} className="!bg-transparent !border-0 !w-0 !h-0" />
      <Tooltip>
        <TooltipTrigger asChild>
          <div
            className="flex flex-col items-center justify-center rounded-lg border-2 px-3 py-2 text-center"
            style={containerStyle}
            data-testid={`diff-node-${data.diffStatus}`}
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
})

const DiffMarketDataNode = memo(function DiffMarketDataNode({ data }: { data: DiffNodeData }) {
  const node = data.manifestNode
  const code = node.data.code as string
  const containerStyle = useDiffContainerStyle(data.diffStatus)

  return (
    <>
      <Handle type="target" position={Position.Top} className="!bg-transparent !border-0 !w-0 !h-0" />
      <Tooltip>
        <TooltipTrigger asChild>
          <div
            className="flex flex-col items-center justify-center rounded-lg border-2 px-3 py-2 text-center"
            style={containerStyle}
            data-testid={`diff-node-${data.diffStatus}`}
          >
            <span className="text-[11px] font-bold font-mono text-foreground">{code}</span>
            <span className="text-[10px] text-muted-foreground truncate w-full">{node.label}</span>
          </div>
        </TooltipTrigger>
        <TooltipContent side="top">{node.label} ({code})</TooltipContent>
      </Tooltip>
      <Handle type="source" position={Position.Bottom} className="!bg-transparent !border-0 !w-0 !h-0" />
    </>
  )
})

const DiffOrganizationNode = memo(function DiffOrganizationNode({ data }: { data: DiffNodeData }) {
  const node = data.manifestNode
  const code = node.data.code as string
  const containerStyle = useDiffContainerStyle(data.diffStatus)

  return (
    <>
      <Handle type="target" position={Position.Top} className="!bg-transparent !border-0 !w-0 !h-0" />
      <Tooltip>
        <TooltipTrigger asChild>
          <div
            className="flex flex-col items-center justify-center rounded-lg border-2 px-3 py-2 text-center"
            style={containerStyle}
            data-testid={`diff-node-${data.diffStatus}`}
          >
            <span className="text-[11px] font-bold font-mono text-foreground">{code}</span>
            <span className="text-[10px] text-muted-foreground truncate w-full">{node.label}</span>
          </div>
        </TooltipTrigger>
        <TooltipContent side="top">{node.label} ({code})</TooltipContent>
      </Tooltip>
      <Handle type="source" position={Position.Bottom} className="!bg-transparent !border-0 !w-0 !h-0" />
    </>
  )
})

const DiffInternalAccountNode = memo(function DiffInternalAccountNode({ data }: { data: DiffNodeData }) {
  const node = data.manifestNode
  const code = node.data.code as string
  const accountType = node.data.accountType as string | undefined
  const containerStyle = useDiffContainerStyle(data.diffStatus)

  return (
    <>
      <Handle type="target" position={Position.Top} className="!bg-transparent !border-0 !w-0 !h-0" />
      <Tooltip>
        <TooltipTrigger asChild>
          <div
            className="flex flex-col items-center justify-center rounded-lg border-2 px-3 py-2 text-center"
            style={containerStyle}
            data-testid={`diff-node-${data.diffStatus}`}
          >
            <span className="text-[11px] font-bold font-mono text-foreground">{code}</span>
            <span className="text-[10px] text-muted-foreground truncate w-full">{node.label}</span>
            {accountType && <span className="text-[9px] text-muted-foreground">{accountType}</span>}
          </div>
        </TooltipTrigger>
        <TooltipContent side="top">{node.label} ({code})</TooltipContent>
      </Tooltip>
      <Handle type="source" position={Position.Bottom} className="!bg-transparent !border-0 !w-0 !h-0" />
    </>
  )
})

const DiffMappingNode = memo(function DiffMappingNode({ data }: { data: DiffNodeData }) {
  const node = data.manifestNode
  const containerStyle = useDiffContainerStyle(data.diffStatus)

  return (
    <>
      <Handle type="target" position={Position.Top} className="!bg-transparent !border-0 !w-0 !h-0" />
      <Tooltip>
        <TooltipTrigger asChild>
          <div
            className="flex flex-col items-center justify-center rounded-lg border-2 px-3 py-2 text-center"
            style={containerStyle}
            data-testid={`diff-node-${data.diffStatus}`}
          >
            <span className="text-[11px] font-bold font-mono text-foreground truncate w-full">{node.label}</span>
          </div>
        </TooltipTrigger>
        <TooltipContent side="top">Mapping: {node.label}</TooltipContent>
      </Tooltip>
      <Handle type="source" position={Position.Bottom} className="!bg-transparent !border-0 !w-0 !h-0" />
    </>
  )
})

const DiffPaymentRailNode = memo(function DiffPaymentRailNode({ data }: { data: DiffNodeData }) {
  const node = data.manifestNode
  const provider = node.data.provider as string
  const containerStyle = useDiffContainerStyle(data.diffStatus)

  return (
    <>
      <Handle type="target" position={Position.Top} className="!bg-transparent !border-0 !w-0 !h-0" />
      <Tooltip>
        <TooltipTrigger asChild>
          <div
            className="flex flex-col items-center justify-center rounded-lg border-2 px-3 py-2 text-center"
            style={containerStyle}
            data-testid={`diff-node-${data.diffStatus}`}
          >
            <span className="text-[11px] font-bold font-mono text-foreground">{provider}</span>
            <span className="text-[10px] text-muted-foreground truncate w-full">{node.label}</span>
          </div>
        </TooltipTrigger>
        <TooltipContent side="top">Payment Rail: {provider}</TooltipContent>
      </Tooltip>
      <Handle type="source" position={Position.Bottom} className="!bg-transparent !border-0 !w-0 !h-0" />
    </>
  )
})

const DiffOperationalGatewayNode = memo(function DiffOperationalGatewayNode({ data }: { data: DiffNodeData }) {
  const node = data.manifestNode
  const containerStyle = useDiffContainerStyle(data.diffStatus)

  return (
    <>
      <Handle type="target" position={Position.Top} className="!bg-transparent !border-0 !w-0 !h-0" />
      <Tooltip>
        <TooltipTrigger asChild>
          <div
            className="flex flex-col items-center justify-center rounded-lg border-2 px-3 py-2 text-center"
            style={containerStyle}
            data-testid={`diff-node-${data.diffStatus}`}
          >
            <span className="text-[11px] font-bold text-foreground truncate w-full">{node.label}</span>
          </div>
        </TooltipTrigger>
        <TooltipContent side="top">{node.label}</TooltipContent>
      </Tooltip>
      <Handle type="source" position={Position.Bottom} className="!bg-transparent !border-0 !w-0 !h-0" />
    </>
  )
})

const DiffProviderConnectionNode = memo(function DiffProviderConnectionNode({ data }: { data: DiffNodeData }) {
  const node = data.manifestNode
  const connectionId = node.data.connectionId as string
  const containerStyle = useDiffContainerStyle(data.diffStatus)

  return (
    <>
      <Handle type="target" position={Position.Top} className="!bg-transparent !border-0 !w-0 !h-0" />
      <Tooltip>
        <TooltipTrigger asChild>
          <div
            className="flex flex-col items-center justify-center rounded-lg border-2 px-3 py-2 text-center"
            style={containerStyle}
            data-testid={`diff-node-${data.diffStatus}`}
          >
            <span className="text-[11px] font-bold text-foreground truncate w-full">{node.label}</span>
            <span className="text-[9px] text-muted-foreground font-mono">{connectionId}</span>
          </div>
        </TooltipTrigger>
        <TooltipContent side="top">{node.label} ({connectionId})</TooltipContent>
      </Tooltip>
      <Handle type="source" position={Position.Bottom} className="!bg-transparent !border-0 !w-0 !h-0" />
    </>
  )
})

const DiffInstructionRouteNode = memo(function DiffInstructionRouteNode({ data }: { data: DiffNodeData }) {
  const node = data.manifestNode
  const connectionId = node.data.connectionId as string
  const containerStyle = useDiffContainerStyle(data.diffStatus)

  return (
    <>
      <Handle type="target" position={Position.Top} className="!bg-transparent !border-0 !w-0 !h-0" />
      <Tooltip>
        <TooltipTrigger asChild>
          <div
            className="flex flex-col items-center justify-center rounded-lg border-2 px-3 py-2 text-center"
            style={containerStyle}
            data-testid={`diff-node-${data.diffStatus}`}
          >
            <span className="text-[11px] font-bold text-foreground truncate w-full">{node.label}</span>
            <span className="text-[9px] text-muted-foreground font-mono">{connectionId}</span>
          </div>
        </TooltipTrigger>
        <TooltipContent side="top">{node.label} via {connectionId}</TooltipContent>
      </Tooltip>
      <Handle type="source" position={Position.Bottom} className="!bg-transparent !border-0 !w-0 !h-0" />
    </>
  )
})

const DiffPartyTypeNode = memo(function DiffPartyTypeNode({ data }: { data: DiffNodeData }) {
  const node = data.manifestNode
  const containerStyle = useDiffContainerStyle(data.diffStatus)

  return (
    <>
      <Handle type="target" position={Position.Top} className="!bg-transparent !border-0 !w-0 !h-0" />
      <Tooltip>
        <TooltipTrigger asChild>
          <div
            className="flex flex-col items-center justify-center rounded-lg border-2 px-3 py-2 text-center"
            style={containerStyle}
            data-testid={`diff-node-${data.diffStatus}`}
          >
            <span className="text-[11px] font-bold text-foreground truncate w-full">{node.label}</span>
          </div>
        </TooltipTrigger>
        <TooltipContent side="top">Party Type: {node.label}</TooltipContent>
      </Tooltip>
      <Handle type="source" position={Position.Bottom} className="!bg-transparent !border-0 !w-0 !h-0" />
    </>
  )
})

// Prefix diff_ to avoid conflicts with main graph node types when composed in the same React tree
const nodeTypes = {
  diff_instrument: DiffInstrumentNode,
  diff_account_type: DiffAccountTypeNode,
  diff_valuation_rule: DiffValuationRuleNode,
  diff_saga: DiffSagaNode,
  diff_market_data: DiffMarketDataNode,
  diff_organization: DiffOrganizationNode,
  diff_internal_account: DiffInternalAccountNode,
  diff_mapping: DiffMappingNode,
  diff_payment_rail: DiffPaymentRailNode,
  diff_operational_gateway: DiffOperationalGatewayNode,
  diff_provider_connection: DiffProviderConnectionNode,
  diff_instruction_route: DiffInstructionRouteNode,
  diff_party_type: DiffPartyTypeNode,
}

function buildDiffEdgeStyle(status: DiffStatus): React.CSSProperties {
  const colors = DIFF_COLORS[status]
  return {
    stroke: colors.border,
    strokeWidth: 2,
    strokeDasharray: status === 'removed' ? '4 4' : undefined,
  }
}

function buildReactFlowElements(
  diff: ManifestDiff,
  before: ManifestGraph,
  after: ManifestGraph,
): { allNodes: ManifestNode[]; allEdges: ManifestEdge[]; nodeStatusMap: Map<string, DiffStatus>; edgeStatusMap: Map<string, DiffStatus> } {
  const nodeStatusMap = new Map<string, DiffStatus>()
  const edgeStatusMap = new Map<string, DiffStatus>()

  const addedIds = new Set(diff.addedNodes.map((n) => n.id))
  const modifiedIds = new Set(diff.modifiedNodes.map((m) => m.after.id))

  // Build unified node list: after nodes + removed nodes from before
  const allNodes: ManifestNode[] = []
  for (const node of after.nodes) {
    allNodes.push(node)
    if (addedIds.has(node.id)) {
      nodeStatusMap.set(node.id, 'added')
    } else if (modifiedIds.has(node.id)) {
      nodeStatusMap.set(node.id, 'modified')
    } else {
      nodeStatusMap.set(node.id, 'unchanged')
    }
  }
  for (const node of diff.removedNodes) {
    allNodes.push(node)
    nodeStatusMap.set(node.id, 'removed')
  }

  const addedEdgeIds = new Set(diff.addedEdges.map((e) => e.id))

  const allEdges: ManifestEdge[] = []
  for (const edge of after.edges) {
    allEdges.push(edge)
    edgeStatusMap.set(edge.id, addedEdgeIds.has(edge.id) ? 'added' : 'unchanged')
  }
  for (const edge of diff.removedEdges) {
    allEdges.push(edge)
    edgeStatusMap.set(edge.id, 'removed')
  }

  return { allNodes, allEdges, nodeStatusMap, edgeStatusMap }
}

async function layoutDiffGraph(
  allNodes: ManifestNode[],
  allEdges: ManifestEdge[],
  nodeStatusMap: Map<string, DiffStatus>,
  edgeStatusMap: Map<string, DiffStatus>,
): Promise<{ nodes: Node[]; edges: Edge[] }> {
  if (allNodes.length === 0) {
    return { nodes: [], edges: [] }
  }

  const layoutNodes = allNodes.map((n) => ({
    id: n.id,
    width: NODE_WIDTH,
    height: NODE_BASE_HEIGHT + NODE_PADDING,
    layoutOptions: {
      'elk.layered.layering.layerChoiceConstraint': LAYER_PRIORITY[n.type],
    },
  }))

  const rfEdges: Edge[] = allEdges.map((e) => {
    const status = edgeStatusMap.get(e.id) ?? 'unchanged'
    return {
      id: e.id,
      source: e.source,
      target: e.target,
      style: buildDiffEdgeStyle(status),
      data: { diffStatus: status },
    }
  })

  const rfNodes = await layoutWithELK<DiffNodeData>(
    layoutNodes,
    rfEdges,
    (id, position) => {
      const mn = allNodes.find((n) => n.id === id)!
      const status = nodeStatusMap.get(id) ?? 'unchanged'
      return {
        id,
        type: `diff_${mn.type}`,
        position,
        data: {
          manifestNode: mn,
          diffStatus: status,
        } satisfies DiffNodeData,
      }
    },
    {
      direction: 'DOWN',
      nodeNodeSpacing: '50',
      layerSpacing: '80',
    },
  )

  return { nodes: rfNodes, edges: rfEdges }
}

function DiffLegendItem({ label, color, dashed }: { label: string; color: string; dashed?: boolean }) {
  return (
    <div className="flex items-center gap-2">
      <span className="w-3 h-3 rounded-sm border-2" style={{ borderColor: color, backgroundColor: `color-mix(in oklch, ${color} 10%, transparent)` }} />
      {dashed !== undefined && (
        <svg width="20" height="12">
          <line x1="0" y1="6" x2="20" y2="6" stroke={color} strokeWidth={2} strokeDasharray={dashed ? '4 4' : undefined} />
        </svg>
      )}
      <span className="text-xs text-muted-foreground">{label}</span>
    </div>
  )
}

interface ManifestDiffGraphProps {
  before: ManifestGraph
  after: ManifestGraph
  className?: string
}

export function ManifestDiffGraph({ before, after, className }: ManifestDiffGraphProps) {
  const [nodes, setNodes, onNodesChange] = useNodesState<Node>([])
  const [edges, setEdges, onEdgesChange] = useEdgesState<Edge>([])

  const diff = useMemo(() => computeManifestDiff(before, after), [before, after])

  useEffect(() => {
    const { allNodes, allEdges, nodeStatusMap, edgeStatusMap } = buildReactFlowElements(diff, before, after)

    let cancelled = false
    void layoutDiffGraph(allNodes, allEdges, nodeStatusMap, edgeStatusMap)
      .then((result) => {
        if (!cancelled) {
          setNodes(result.nodes)
          setEdges(result.edges)
        }
      })
      .catch((err) => {
        if (!cancelled) {
          console.error('[ManifestDiffGraph] layout failed:', err)
        }
      })
    return () => { cancelled = true }
  }, [diff, before, after, setNodes, setEdges])

  const noDiff = diff.addedNodes.length === 0
    && diff.removedNodes.length === 0
    && diff.modifiedNodes.length === 0
    && diff.addedEdges.length === 0
    && diff.removedEdges.length === 0

  if (noDiff) {
    return (
      <div className={`${className ?? ''} w-full h-full`} data-testid="manifest-diff-no-changes">
        <div className="flex items-center justify-center h-full text-muted-foreground text-sm">
          No differences between versions.
        </div>
      </div>
    )
  }

  return (
    <div className={`${className ?? ''} w-full h-full relative`} data-testid="manifest-diff-graph">
      <TooltipProvider>
        <ReactFlow
          nodes={nodes}
          edges={edges}
          onNodesChange={onNodesChange}
          onEdgesChange={onEdgesChange}
          nodeTypes={nodeTypes}
          fitView
          proOptions={{ hideAttribution: true }}
          nodesDraggable={false}
        >
          <Controls />
          <Background variant={BackgroundVariant.Dots} gap={16} size={1} />
        </ReactFlow>
      </TooltipProvider>

      {/* Diff summary */}
      <div className="absolute top-3 left-3 z-10 flex flex-col gap-1 rounded-lg border bg-background/95 p-3 backdrop-blur-sm shadow-sm" data-testid="diff-summary">
        <span className="text-xs font-semibold text-muted-foreground uppercase tracking-wider mb-1">Changes</span>
        {diff.addedNodes.length > 0 && (
          <span className="text-xs text-success-foreground">+{diff.addedNodes.length} added</span>
        )}
        {diff.removedNodes.length > 0 && (
          <span className="text-xs text-destructive">-{diff.removedNodes.length} removed</span>
        )}
        {diff.modifiedNodes.length > 0 && (
          <span className="text-xs text-warning-foreground">~{diff.modifiedNodes.length} modified</span>
        )}
        {diff.addedEdges.length > 0 && (
          <span className="text-xs text-success-foreground">+{diff.addedEdges.length} edge{diff.addedEdges.length !== 1 ? 's' : ''}</span>
        )}
        {diff.removedEdges.length > 0 && (
          <span className="text-xs text-destructive">-{diff.removedEdges.length} edge{diff.removedEdges.length !== 1 ? 's' : ''}</span>
        )}
      </div>

      {/* Legend */}
      <div className="absolute bottom-3 left-3 z-10 flex flex-col gap-1 rounded-lg border bg-background/95 p-3 backdrop-blur-sm shadow-sm">
        <span className="text-xs font-semibold text-muted-foreground uppercase tracking-wider mb-1">Legend</span>
        <DiffLegendItem label="Added" color="var(--graph-diff-added)" />
        <DiffLegendItem label="Removed" color="var(--graph-diff-removed)" dashed />
        <DiffLegendItem label="Modified" color="var(--graph-diff-modified)" />
        <DiffLegendItem label="Unchanged" color="var(--graph-diff-unchanged)" />
      </div>
    </div>
  )
}
