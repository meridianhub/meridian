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
import ELK from 'elkjs/lib/elk.bundled.js'
import { useNavigate } from 'react-router-dom'
import type { CookbookItem, PatternMeta } from '../hooks/use-cookbook'

const elk = new ELK()

// Category color mapping
const CATEGORY_COLORS: Record<string, string> = {
  foundation: '#6366f1', // indigo
  energy: '#f59e0b',     // amber
  economy: '#10b981',    // emerald
  carbon: '#22c55e',     // green
  compute: '#8b5cf6',    // violet
  compliance: '#ef4444', // red
  payments: '#3b82f6',   // blue
  trading: '#ec4899',    // pink
  billing: '#06b6d4',    // cyan
  commodities: '#d97706', // amber-dark
}

function getCategoryColor(categories?: string[]): string {
  if (!categories?.length) return '#71717a' // zinc-500
  for (const cat of categories) {
    if (CATEGORY_COLORS[cat]) return CATEGORY_COLORS[cat]
  }
  return '#71717a'
}

// Edge styles by relationship type
const EDGE_STYLES = {
  registryDependencies: { stroke: '#3b82f6', strokeWidth: 2, strokeDasharray: undefined },
  composes_with: { stroke: '#22c55e', strokeWidth: 1.5, strokeDasharray: '6 3' },
  extends: { stroke: '#8b5cf6', strokeWidth: 3, strokeDasharray: undefined },
  conflicts_with: { stroke: '#ef4444', strokeWidth: 1.5, strokeDasharray: '4 4' },
} as const

type RelationshipType = keyof typeof EDGE_STYLES

// Custom node component
interface PatternNodeData {
  label: string
  complexity: number
  categories: string[]
  color: string
  highlighted: boolean
  dimmed: boolean
  [key: string]: unknown
}

function PatternNode({ data }: { data: PatternNodeData }) {
  const size = 40 + (data.complexity ?? 1) * 12
  return (
    <>
      <Handle type="target" position={Position.Top} className="!bg-transparent !border-0 !w-0 !h-0" />
      <div
        className="flex flex-col items-center justify-center rounded-lg border-2 px-3 py-2 text-center transition-opacity duration-150"
        style={{
          width: size,
          height: size,
          borderColor: data.color,
          backgroundColor: `${data.color}18`,
          opacity: data.dimmed ? 0.25 : 1,
          boxShadow: data.highlighted ? `0 0 12px ${data.color}88` : undefined,
        }}
      >
        <span className="text-[10px] font-semibold leading-tight text-foreground truncate w-full">
          {data.label}
        </span>
        {data.complexity > 0 && (
          <span
            className="mt-0.5 inline-flex items-center justify-center rounded-full text-[8px] font-bold text-white"
            style={{ backgroundColor: data.color, width: 16, height: 16 }}
          >
            {data.complexity}
          </span>
        )}
      </div>
      <Handle type="source" position={Position.Bottom} className="!bg-transparent !border-0 !w-0 !h-0" />
    </>
  )
}

const nodeTypes = { pattern: PatternNode }

// Build graph data from patterns
function buildEdges(patterns: CookbookItem[]): Edge[] {
  const nameSet = new Set(patterns.map((p) => p.name))
  const edges: Edge[] = []
  const edgeIds = new Set<string>()

  for (const pattern of patterns) {
    const meta = pattern.meta as PatternMeta | undefined

    // registryDependencies (top-level field)
    for (const dep of pattern.registryDependencies ?? []) {
      if (nameSet.has(dep)) {
        const id = `dep-${pattern.name}-${dep}`
        if (!edgeIds.has(id)) {
          edgeIds.add(id)
          edges.push({
            id,
            source: dep,
            target: pattern.name,
            style: EDGE_STYLES.registryDependencies,
            markerEnd: { type: 'arrowclosed' as const, color: '#3b82f6' },
            data: { type: 'registryDependencies' as RelationshipType },
          })
        }
      }
    }

    // composes_with (bidirectional, deduplicate)
    for (const other of meta?.composes_with ?? []) {
      if (nameSet.has(other)) {
        const sorted = [pattern.name, other].sort()
        const id = `comp-${sorted[0]}-${sorted[1]}`
        if (!edgeIds.has(id)) {
          edgeIds.add(id)
          edges.push({
            id,
            source: sorted[0],
            target: sorted[1],
            style: EDGE_STYLES.composes_with,
            data: { type: 'composes_with' as RelationshipType },
          })
        }
      }
    }

    // extends
    for (const base of meta?.extends ?? []) {
      if (nameSet.has(base)) {
        const id = `ext-${pattern.name}-${base}`
        if (!edgeIds.has(id)) {
          edgeIds.add(id)
          edges.push({
            id,
            source: base,
            target: pattern.name,
            style: EDGE_STYLES.extends,
            markerEnd: { type: 'arrowclosed' as const, color: '#8b5cf6' },
            data: { type: 'extends' as RelationshipType },
          })
        }
      }
    }

    // conflicts_with (bidirectional, deduplicate)
    for (const other of meta?.conflicts_with ?? []) {
      if (nameSet.has(other)) {
        const sorted = [pattern.name, other].sort()
        const id = `conf-${sorted[0]}-${sorted[1]}`
        if (!edgeIds.has(id)) {
          edgeIds.add(id)
          edges.push({
            id,
            source: sorted[0],
            target: sorted[1],
            style: EDGE_STYLES.conflicts_with,
            data: { type: 'conflicts_with' as RelationshipType },
          })
        }
      }
    }
  }

  return edges
}

async function layoutGraph(
  patterns: CookbookItem[],
  edges: Edge[],
): Promise<Node[]> {
  const elkNodes = patterns.map((p) => {
    const complexity = (p.meta as PatternMeta | undefined)?.complexity ?? 1
    const size = 40 + complexity * 12
    return { id: p.name, width: size + 20, height: size + 20 }
  })

  const elkEdges = edges.map((e) => ({
    id: e.id,
    sources: [e.source],
    targets: [e.target],
  }))

  const layout = await elk.layout({
    id: 'root',
    layoutOptions: {
      'elk.algorithm': 'layered',
      'elk.direction': 'DOWN',
      'elk.spacing.nodeNode': '80',
      'elk.layered.spacing.nodeNodeBetweenLayers': '100',
    },
    children: elkNodes,
    edges: elkEdges,
  })

  return patterns.map((p) => {
    const elkNode = layout.children?.find((n) => n.id === p.name)
    const meta = p.meta as PatternMeta | undefined
    const color = getCategoryColor(p.categories)
    return {
      id: p.name,
      type: 'pattern',
      position: { x: elkNode?.x ?? 0, y: elkNode?.y ?? 0 },
      data: {
        label: p.title,
        complexity: meta?.complexity ?? 1,
        categories: p.categories ?? [],
        color,
        highlighted: false,
        dimmed: false,
      } satisfies PatternNodeData,
    }
  })
}

// Collect all unique categories and industries from patterns
function collectFilterOptions(patterns: CookbookItem[]) {
  const categories = new Set<string>()
  const industries = new Set<string>()
  for (const p of patterns) {
    for (const c of p.categories ?? []) categories.add(c)
    const meta = p.meta as PatternMeta | undefined
    for (const i of meta?.industries ?? []) industries.add(i)
  }
  return {
    categories: [...categories].sort(),
    industries: [...industries].sort(),
  }
}

// Legend item
function LegendItem({ label, color, dashed, thick }: { label: string; color: string; dashed?: boolean; thick?: boolean }) {
  return (
    <div className="flex items-center gap-2">
      <svg width="32" height="12">
        <line
          x1="0" y1="6" x2="32" y2="6"
          stroke={color}
          strokeWidth={thick ? 3 : 2}
          strokeDasharray={dashed ? '4 3' : undefined}
        />
      </svg>
      <span className="text-xs text-muted-foreground">{label}</span>
    </div>
  )
}

interface CompositionGraphProps {
  patterns: CookbookItem[]
  className?: string
}

export function CompositionGraph({ patterns, className }: CompositionGraphProps) {
  const navigate = useNavigate()
  const [nodes, setNodes, onNodesChange] = useNodesState<Node>([])
  const [edges, setEdges, onEdgesChange] = useEdgesState<Edge>([])
  const [hoveredNode, setHoveredNode] = useState<string | null>(null)
  const [categoryFilter, setCategoryFilter] = useState<string>('')
  const [industryFilter, setIndustryFilter] = useState<string>('')

  // Only patterns (not UI components)
  const graphPatterns = useMemo(
    () => patterns.filter((p) => p.type === 'registry:pattern'),
    [patterns],
  )

  // Filter options
  const filterOptions = useMemo(() => collectFilterOptions(graphPatterns), [graphPatterns])

  // Apply filters
  const filteredPatterns = useMemo(() => {
    return graphPatterns.filter((p) => {
      if (categoryFilter && !(p.categories ?? []).includes(categoryFilter)) return false
      const meta = p.meta as PatternMeta | undefined
      if (industryFilter && !(meta?.industries ?? []).includes(industryFilter)) return false
      return true
    })
  }, [graphPatterns, categoryFilter, industryFilter])

  // Build edges from filtered patterns
  const graphEdges = useMemo(() => buildEdges(filteredPatterns), [filteredPatterns])

  // Layout
  useEffect(() => {
    if (filteredPatterns.length === 0) {
      setNodes([])
      setEdges([])
      return
    }
    let cancelled = false
    void layoutGraph(filteredPatterns, graphEdges).then((layoutNodes) => {
      if (!cancelled) {
        setNodes(layoutNodes)
        setEdges(graphEdges)
      }
    })
    return () => { cancelled = true }
  }, [filteredPatterns, graphEdges, setNodes, setEdges])

  // Hover highlighting
  useEffect(() => {
    if (!hoveredNode) {
      setNodes((nds) =>
        nds.map((n) => ({
          ...n,
          data: { ...n.data, highlighted: false, dimmed: false },
        })),
      )
      setEdges((eds) => eds.map((e) => ({ ...e, animated: false })))
      return
    }

    const connectedNodes = new Set<string>([hoveredNode])
    for (const e of edges) {
      if (e.source === hoveredNode || e.target === hoveredNode) {
        connectedNodes.add(e.source)
        connectedNodes.add(e.target)
      }
    }

    setNodes((nds) =>
      nds.map((n) => ({
        ...n,
        data: {
          ...n.data,
          highlighted: n.id === hoveredNode,
          dimmed: !connectedNodes.has(n.id),
        },
      })),
    )
    setEdges((eds) =>
      eds.map((e) => ({
        ...e,
        animated: e.source === hoveredNode || e.target === hoveredNode,
      })),
    )
  }, [hoveredNode, edges, setNodes, setEdges])

  const onNodeClick: NodeMouseHandler = useCallback(
    (_event, node) => {
      navigate(`/cookbook/${node.id}`)
    },
    [navigate],
  )

  const onNodeMouseEnter: NodeMouseHandler = useCallback((_event, node) => {
    setHoveredNode(node.id)
  }, [])

  const onNodeMouseLeave: NodeMouseHandler = useCallback(() => {
    setHoveredNode(null)
  }, [])

  return (
    <div className={className} style={{ width: '100%', height: '100%' }}>
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
          nodeColor={(n) => (n.data as PatternNodeData).color}
          maskColor="rgba(0, 0, 0, 0.15)"
        />
      </ReactFlow>

      {/* Filter sidebar */}
      <div className="absolute top-3 left-3 z-10 flex flex-col gap-2 rounded-lg border bg-background/95 p-3 backdrop-blur-sm shadow-sm">
        <span className="text-xs font-semibold text-muted-foreground uppercase tracking-wider">Filters</span>
        <select
          value={categoryFilter}
          onChange={(e) => setCategoryFilter(e.target.value)}
          className="rounded border bg-background px-2 py-1 text-xs"
        >
          <option value="">All categories</option>
          {filterOptions.categories.map((c) => (
            <option key={c} value={c}>{c}</option>
          ))}
        </select>
        <select
          value={industryFilter}
          onChange={(e) => setIndustryFilter(e.target.value)}
          className="rounded border bg-background px-2 py-1 text-xs"
        >
          <option value="">All industries</option>
          {filterOptions.industries.map((i) => (
            <option key={i} value={i}>{i}</option>
          ))}
        </select>
        <span className="text-[10px] text-muted-foreground">{filteredPatterns.length} patterns</span>
      </div>

      {/* Legend */}
      <div className="absolute bottom-3 left-3 z-10 flex flex-col gap-1 rounded-lg border bg-background/95 p-3 backdrop-blur-sm shadow-sm">
        <span className="text-xs font-semibold text-muted-foreground uppercase tracking-wider mb-1">Edges</span>
        <LegendItem label="Dependency" color="#3b82f6" />
        <LegendItem label="Composes with" color="#22c55e" dashed />
        <LegendItem label="Extends" color="#8b5cf6" thick />
        <LegendItem label="Conflicts" color="#ef4444" dashed />
      </div>
    </div>
  )
}
