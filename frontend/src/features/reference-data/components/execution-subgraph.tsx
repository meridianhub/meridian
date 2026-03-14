import { useEffect, useMemo } from 'react'
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
  ManifestGraph,
  ManifestNode,
  ManifestNodeType,
} from '@/features/manifests/lib/manifest-graph-model'
import { filterSubgraph } from '@/features/reference-data/lib/filter-subgraph'

const NODE_THEMES: Record<ManifestNodeType, { color: string }> = {
  instrument: { color: 'var(--graph-instrument)' },
  account_type: { color: 'var(--graph-account-type)' },
  valuation_rule: { color: 'var(--graph-valuation-rule)' },
  saga: { color: 'var(--graph-saga)' },
}

const EDGE_STYLES: Record<string, React.CSSProperties> = {
  allowed_by: { stroke: 'var(--graph-instrument)', strokeWidth: 2 },
  converts_from: { stroke: 'var(--graph-valuation-rule)', strokeWidth: 1.5, strokeDasharray: '6 3' },
  converts_to: { stroke: 'var(--graph-valuation-rule)', strokeWidth: 1.5 },
  writes_to: { stroke: 'var(--graph-saga)', strokeWidth: 1.5 },
  uses_valuation: { stroke: 'var(--graph-valuation-rule)', strokeWidth: 1.5, strokeDasharray: '4 2' },
}

interface SubgraphNodeData {
  manifestNode: ManifestNode
  color: string
  isFocusNode: boolean
  [key: string]: unknown
}

function SubgraphNode({ data }: { data: SubgraphNodeData }) {
  const node = data.manifestNode
  const code = (node.data.code as string) ?? node.label
  return (
    <>
      <Handle type="target" position={Position.Top} className="!bg-transparent !border-0 !w-0 !h-0" />
      <Tooltip>
        <TooltipTrigger asChild>
          <div
            className="flex flex-col items-center justify-center rounded-lg border-2 px-3 py-2 text-center"
            style={{
              width: 180,
              borderColor: data.color,
              borderWidth: data.isFocusNode ? 3 : 2,
              backgroundColor: `${data.color}18`,
              boxShadow: data.isFocusNode ? `0 0 16px ${data.color}66` : undefined,
            }}
          >
            <span className="text-[11px] font-bold font-mono text-foreground">{code}</span>
            <span className="text-[10px] text-muted-foreground truncate w-full">{node.label}</span>
            <span className="text-[9px] text-muted-foreground italic">{node.type.replace('_', ' ')}</span>
          </div>
        </TooltipTrigger>
        <TooltipContent side="top">{node.label} ({node.type})</TooltipContent>
      </Tooltip>
      <Handle type="source" position={Position.Bottom} className="!bg-transparent !border-0 !w-0 !h-0" />
    </>
  )
}

const nodeTypes = {
  subgraph_node: SubgraphNode,
}

interface ExecutionSubgraphProps {
  graph: ManifestGraph | null
  focusNodeId: string
}

export function ExecutionSubgraph({ graph, focusNodeId }: ExecutionSubgraphProps) {
  const [nodes, setNodes, onNodesChange] = useNodesState<Node>([])
  const [edges, setEdges, onEdgesChange] = useEdgesState<Edge>([])

  const subgraph = useMemo(
    () => (graph ? filterSubgraph(graph, focusNodeId) : null),
    [graph, focusNodeId],
  )

  const hasConnections = subgraph && subgraph.nodes.length > 1

  useEffect(() => {
    if (!subgraph || subgraph.nodes.length <= 1) {
      setNodes([])
      setEdges([])
      return
    }

    let cancelled = false

    const layoutNodes = subgraph.nodes.map((n) => ({
      id: n.id,
      width: NODE_WIDTH,
      height: NODE_BASE_HEIGHT + NODE_PADDING,
    }))

    const rfEdges: Edge[] = subgraph.edges.map((e) => ({
      id: e.id,
      source: e.source,
      target: e.target,
      style: EDGE_STYLES[e.relationship] ?? {},
      markerEnd: { type: 'arrowclosed' as const, color: (EDGE_STYLES[e.relationship]?.stroke as string) ?? 'var(--graph-diff-unchanged)' },
    }))

    const nodeMap = new Map(subgraph.nodes.map((n) => [n.id, n]))

    void layoutWithELK<SubgraphNodeData>(
      layoutNodes,
      rfEdges,
      (id, position) => {
        const mn = nodeMap.get(id)!
        return {
          id,
          type: 'subgraph_node',
          position,
          data: {
            manifestNode: mn,
            color: NODE_THEMES[mn.type].color,
            isFocusNode: id === focusNodeId,
          } satisfies SubgraphNodeData,
        }
      },
      { direction: 'DOWN', nodeNodeSpacing: '40', layerSpacing: '60' },
    ).then((rfNodes) => {
      if (!cancelled) {
        setNodes(rfNodes)
        setEdges(rfEdges)
      }
    }).catch((err) => {
      if (!cancelled) {
        console.error('[ExecutionSubgraph] layout failed:', err)
        setNodes([])
        setEdges([])
      }
    })

    return () => { cancelled = true }
  }, [subgraph, focusNodeId, setNodes, setEdges])

  if (!graph) {
    return (
      <div className="flex items-center justify-center py-8 text-sm text-muted-foreground">
        No manifest data available.
      </div>
    )
  }

  if (!hasConnections) {
    return (
      <div className="flex items-center justify-center py-8 text-sm text-muted-foreground">
        No connected elements found.
      </div>
    )
  }

  return (
    <div style={{ width: '100%', height: 400 }}>
      <TooltipProvider>
        <ReactFlow
          nodes={nodes}
          edges={edges}
          onNodesChange={onNodesChange}
          onEdgesChange={onEdgesChange}
          nodeTypes={nodeTypes}
          fitView
          proOptions={{ hideAttribution: true }}
        >
          <Controls />
          <Background variant={BackgroundVariant.Dots} gap={16} size={1} />
        </ReactFlow>
      </TooltipProvider>
    </div>
  )
}
