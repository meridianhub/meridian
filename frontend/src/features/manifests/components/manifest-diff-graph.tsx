import { useEffect, useMemo, useState } from 'react'
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
  TooltipProvider,
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
  ManifestNodeType,
} from '../lib/manifest-graph-model'
import { computeManifestDiff, type ManifestDiff } from '../lib/manifest-diff'

type DiffStatus = 'added' | 'removed' | 'modified' | 'unchanged'

const DIFF_COLORS: Record<DiffStatus, { border: string; bg: string }> = {
  added: { border: '#16a34a', bg: '#16a34a18' },
  removed: { border: '#dc2626', bg: '#dc262618' },
  modified: { border: '#d97706', bg: '#d9770618' },
  unchanged: { border: '#6b7280', bg: '#6b728010' },
}

const LAYER_PRIORITY: Record<ManifestNodeType, string> = {
  instrument: '40',
  account_type: '30',
  valuation_rule: '20',
  saga: '10',
}

interface DiffNodeData {
  manifestNode: ManifestNode
  diffStatus: DiffStatus
  [key: string]: unknown
}

function DiffNode({ data }: { data: DiffNodeData }) {
  const node = data.manifestNode
  const colors = DIFF_COLORS[data.diffStatus]
  return (
    <>
      <Handle type="target" position={Position.Top} className="!bg-transparent !border-0 !w-0 !h-0" />
      <div
        className="flex flex-col items-center justify-center rounded-lg border-2 px-3 py-2 text-center"
        style={{
          width: 180,
          borderColor: colors.border,
          backgroundColor: colors.bg,
          textDecoration: data.diffStatus === 'removed' ? 'line-through' : undefined,
        }}
        data-testid={`diff-node-${data.diffStatus}`}
      >
        <span className="text-[11px] font-bold font-mono text-foreground">{node.label}</span>
        <span className="text-[9px] text-muted-foreground">{node.type.replace('_', ' ')}</span>
      </div>
      <Handle type="source" position={Position.Bottom} className="!bg-transparent !border-0 !w-0 !h-0" />
    </>
  )
}

const nodeTypes = {
  diff_node: DiffNode,
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
  const removedIds = new Set(diff.removedNodes.map((n) => n.id))
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
  const removedEdgeIds = new Set(diff.removedEdges.map((e) => e.id))

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
        type: 'diff_node',
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
      <span className="w-3 h-3 rounded-sm border-2" style={{ borderColor: color, backgroundColor: `${color}18` }} />
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
  const [diffSummary, setDiffSummary] = useState<ManifestDiff | null>(null)

  const diff = useMemo(() => computeManifestDiff(before, after), [before, after])

  useEffect(() => {
    setDiffSummary(diff)
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
      <div className={className} data-testid="manifest-diff-no-changes" style={{ width: '100%', height: '100%' }}>
        <div className="flex items-center justify-center h-full text-muted-foreground text-sm">
          No differences between versions.
        </div>
      </div>
    )
  }

  return (
    <div className={className} style={{ width: '100%', height: '100%', position: 'relative' }} data-testid="manifest-diff-graph">
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
      {diffSummary && (
        <div className="absolute top-3 left-3 z-10 flex flex-col gap-1 rounded-lg border bg-background/95 p-3 backdrop-blur-sm shadow-sm" data-testid="diff-summary">
          <span className="text-xs font-semibold text-muted-foreground uppercase tracking-wider mb-1">Changes</span>
          {diffSummary.addedNodes.length > 0 && (
            <span className="text-xs text-green-600">+{diffSummary.addedNodes.length} added</span>
          )}
          {diffSummary.removedNodes.length > 0 && (
            <span className="text-xs text-red-600">-{diffSummary.removedNodes.length} removed</span>
          )}
          {diffSummary.modifiedNodes.length > 0 && (
            <span className="text-xs text-amber-600">~{diffSummary.modifiedNodes.length} modified</span>
          )}
          {diffSummary.addedEdges.length > 0 && (
            <span className="text-xs text-green-600">+{diffSummary.addedEdges.length} edge{diffSummary.addedEdges.length !== 1 ? 's' : ''}</span>
          )}
          {diffSummary.removedEdges.length > 0 && (
            <span className="text-xs text-red-600">-{diffSummary.removedEdges.length} edge{diffSummary.removedEdges.length !== 1 ? 's' : ''}</span>
          )}
        </div>
      )}

      {/* Legend */}
      <div className="absolute bottom-3 left-3 z-10 flex flex-col gap-1 rounded-lg border bg-background/95 p-3 backdrop-blur-sm shadow-sm">
        <span className="text-xs font-semibold text-muted-foreground uppercase tracking-wider mb-1">Legend</span>
        <DiffLegendItem label="Added" color="#16a34a" />
        <DiffLegendItem label="Removed" color="#dc2626" dashed />
        <DiffLegendItem label="Modified" color="#d97706" />
        <DiffLegendItem label="Unchanged" color="#6b7280" />
      </div>
    </div>
  )
}
