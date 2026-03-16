import { memo, useCallback, useEffect, useMemo, useState } from 'react'
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
import { Maximize2, Pencil, X } from 'lucide-react'
import {
  Dialog,
  DialogContent,
  DialogHeader,
  DialogTitle,
} from '@/components/ui/dialog'
import {
  Tooltip,
  TooltipContent,
  TooltipProvider,
  TooltipTrigger,
} from '@/components/ui/tooltip'
import { Button } from '@/components/ui/button'
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
import { NODE_TYPE_REGISTRY, getNodeThemes, getLayerPriority } from '../lib/node-type-registry'
import type { Manifest } from '@/api/gen/meridian/control_plane/v1/manifest_pb'
import { useEventChain } from '../hooks/use-event-chain'
import { EventChainPanel } from './event-chain-panel'
import { ApplyResourceModal } from '@/features/economy/components/apply-resource-modal'
import { getResourceSchema } from '@/features/economy/lib/resource-schema-registry'

const NODE_THEMES = getNodeThemes()

// Edge styles per relationship type
const MANIFEST_EDGE_STYLES: Record<string, React.CSSProperties> = {
  allowed_by: { stroke: 'var(--graph-instrument)', strokeWidth: 2 },
  converts_from: { stroke: 'var(--graph-valuation-rule)', strokeWidth: 1.5, strokeDasharray: '6 3' },
  converts_to: { stroke: 'var(--graph-valuation-rule)', strokeWidth: 1.5 },
}

const EDGE_LEGEND: { label: string; color: string; dashed?: boolean }[] = [
  { label: 'Allowed by', color: 'var(--graph-instrument)' },
  { label: 'Converts from', color: 'var(--graph-valuation-rule)', dashed: true },
  { label: 'Converts to', color: 'var(--graph-valuation-rule)' },
]

const LAYER_PRIORITY = getLayerPriority()

// Trigger type display
function getTriggerBadge(trigger: string): { label: string; variant: string } {
  if (trigger.startsWith('event:')) return { label: 'event', variant: 'bg-accent text-accent-foreground' }
  if (trigger.startsWith('scheduled:')) return { label: 'scheduled', variant: 'bg-info-muted text-info-foreground' }
  if (trigger.startsWith('api:')) return { label: 'api', variant: 'bg-success-muted text-success-foreground' }
  return { label: 'unknown', variant: 'bg-muted text-muted-foreground' }
}

// Custom node data interface
interface ManifestNodeData {
  manifestNode: ManifestNode
  color: string
  highlighted: boolean
  dimmed: boolean
  connectedInstrumentCount?: number
  [key: string]: unknown
}

const InstrumentNode = memo(function InstrumentNode({ data }: { data: ManifestNodeData }) {
  const node = data.manifestNode
  const code = node.data.code as string
  const unit = (node.data.dimensions as Record<string, unknown> | undefined)?.unit as string | undefined

  const containerStyle = useMemo(() => ({
    width: 180,
    borderColor: data.color,
    backgroundColor: `color-mix(in oklch, ${data.color} 10%, transparent)`,
    opacity: data.dimmed ? 0.25 : 1,
    boxShadow: data.highlighted ? `0 0 12px color-mix(in oklch, ${data.color} 53%, transparent)` : undefined,
  }), [data.color, data.dimmed, data.highlighted])

  return (
    <>
      <Handle type="target" position={Position.Top} className="!bg-transparent !border-0 !w-0 !h-0" />
      <Tooltip>
        <TooltipTrigger asChild>
          <div
            className="flex flex-col items-center justify-center rounded-lg border-2 px-3 py-2 text-center transition-opacity duration-150 cursor-pointer"
            style={containerStyle}
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

const AccountTypeNode = memo(function AccountTypeNode({ data }: { data: ManifestNodeData }) {
  const node = data.manifestNode
  const code = node.data.code as string
  const allowedCount = data.connectedInstrumentCount ?? 0

  const containerStyle = useMemo(() => ({
    width: 180,
    borderColor: data.color,
    backgroundColor: `color-mix(in oklch, ${data.color} 10%, transparent)`,
    opacity: data.dimmed ? 0.25 : 1,
    boxShadow: data.highlighted ? `0 0 12px color-mix(in oklch, ${data.color} 53%, transparent)` : undefined,
  }), [data.color, data.dimmed, data.highlighted])

  return (
    <>
      <Handle type="target" position={Position.Top} className="!bg-transparent !border-0 !w-0 !h-0" />
      <Tooltip>
        <TooltipTrigger asChild>
          <div
            className="flex flex-col items-center justify-center rounded-lg border-2 px-3 py-2 text-center transition-opacity duration-150 cursor-pointer"
            style={containerStyle}
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
})

const ValuationRuleNode = memo(function ValuationRuleNode({ data }: { data: ManifestNodeData }) {
  const node = data.manifestNode
  const from = node.data.fromInstrument as string
  const to = node.data.toInstrument as string

  const containerStyle = useMemo(() => ({
    width: 180,
    borderColor: data.color,
    backgroundColor: `color-mix(in oklch, ${data.color} 10%, transparent)`,
    opacity: data.dimmed ? 0.25 : 1,
    boxShadow: data.highlighted ? `0 0 12px color-mix(in oklch, ${data.color} 53%, transparent)` : undefined,
  }), [data.color, data.dimmed, data.highlighted])

  return (
    <>
      <Handle type="target" position={Position.Top} className="!bg-transparent !border-0 !w-0 !h-0" />
      <Tooltip>
        <TooltipTrigger asChild>
          <div
            className="flex flex-col items-center justify-center rounded-lg border-2 px-3 py-2 text-center transition-opacity duration-150 cursor-pointer"
            style={containerStyle}
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

const SagaNode = memo(function SagaNode({ data }: { data: ManifestNodeData }) {
  const node = data.manifestNode
  const trigger = node.data.trigger as string
  const badge = getTriggerBadge(trigger)

  const containerStyle = useMemo(() => ({
    width: 180,
    borderColor: data.color,
    backgroundColor: `color-mix(in oklch, ${data.color} 10%, transparent)`,
    opacity: data.dimmed ? 0.25 : 1,
    boxShadow: data.highlighted ? `0 0 12px color-mix(in oklch, ${data.color} 53%, transparent)` : undefined,
  }), [data.color, data.dimmed, data.highlighted])

  return (
    <>
      <Handle type="target" position={Position.Top} className="!bg-transparent !border-0 !w-0 !h-0" />
      <Tooltip>
        <TooltipTrigger asChild>
          <div
            className="flex flex-col items-center justify-center rounded-lg border-2 px-3 py-2 text-center transition-opacity duration-150 cursor-pointer"
            style={containerStyle}
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

const MarketDataNode = memo(function MarketDataNode({ data }: { data: ManifestNodeData }) {
  const node = data.manifestNode
  const code = node.data.code as string

  const containerStyle = useMemo(() => ({
    width: 180,
    borderColor: data.color,
    backgroundColor: `color-mix(in oklch, ${data.color} 10%, transparent)`,
    opacity: data.dimmed ? 0.25 : 1,
    boxShadow: data.highlighted ? `0 0 12px color-mix(in oklch, ${data.color} 53%, transparent)` : undefined,
  }), [data.color, data.dimmed, data.highlighted])

  return (
    <>
      <Handle type="target" position={Position.Top} className="!bg-transparent !border-0 !w-0 !h-0" />
      <Tooltip>
        <TooltipTrigger asChild>
          <div
            className="flex flex-col items-center justify-center rounded-lg border-2 px-3 py-2 text-center transition-opacity duration-150 cursor-pointer"
            style={containerStyle}
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

const OrganizationNode = memo(function OrganizationNode({ data }: { data: ManifestNodeData }) {
  const node = data.manifestNode
  const code = node.data.code as string

  const containerStyle = useMemo(() => ({
    width: 180,
    borderColor: data.color,
    backgroundColor: `color-mix(in oklch, ${data.color} 10%, transparent)`,
    opacity: data.dimmed ? 0.25 : 1,
    boxShadow: data.highlighted ? `0 0 12px color-mix(in oklch, ${data.color} 53%, transparent)` : undefined,
  }), [data.color, data.dimmed, data.highlighted])

  return (
    <>
      <Handle type="target" position={Position.Top} className="!bg-transparent !border-0 !w-0 !h-0" />
      <Tooltip>
        <TooltipTrigger asChild>
          <div
            className="flex flex-col items-center justify-center rounded-lg border-2 px-3 py-2 text-center transition-opacity duration-150 cursor-pointer"
            style={containerStyle}
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

const InternalAccountNode = memo(function InternalAccountNode({ data }: { data: ManifestNodeData }) {
  const node = data.manifestNode
  const code = node.data.code as string
  const accountType = node.data.accountType as string | undefined

  const containerStyle = useMemo(() => ({
    width: 180,
    borderColor: data.color,
    backgroundColor: `color-mix(in oklch, ${data.color} 10%, transparent)`,
    opacity: data.dimmed ? 0.25 : 1,
    boxShadow: data.highlighted ? `0 0 12px color-mix(in oklch, ${data.color} 53%, transparent)` : undefined,
  }), [data.color, data.dimmed, data.highlighted])

  return (
    <>
      <Handle type="target" position={Position.Top} className="!bg-transparent !border-0 !w-0 !h-0" />
      <Tooltip>
        <TooltipTrigger asChild>
          <div
            className="flex flex-col items-center justify-center rounded-lg border-2 px-3 py-2 text-center transition-opacity duration-150 cursor-pointer"
            style={containerStyle}
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

const MappingNode = memo(function MappingNode({ data }: { data: ManifestNodeData }) {
  const node = data.manifestNode

  const containerStyle = useMemo(() => ({
    width: 180,
    borderColor: data.color,
    backgroundColor: `color-mix(in oklch, ${data.color} 10%, transparent)`,
    opacity: data.dimmed ? 0.25 : 1,
    boxShadow: data.highlighted ? `0 0 12px color-mix(in oklch, ${data.color} 53%, transparent)` : undefined,
  }), [data.color, data.dimmed, data.highlighted])

  return (
    <>
      <Handle type="target" position={Position.Top} className="!bg-transparent !border-0 !w-0 !h-0" />
      <Tooltip>
        <TooltipTrigger asChild>
          <div
            className="flex flex-col items-center justify-center rounded-lg border-2 px-3 py-2 text-center transition-opacity duration-150 cursor-pointer"
            style={containerStyle}
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

const PaymentRailNode = memo(function PaymentRailNode({ data }: { data: ManifestNodeData }) {
  const node = data.manifestNode
  const provider = node.data.provider as string

  const containerStyle = useMemo(() => ({
    width: 180,
    borderColor: data.color,
    backgroundColor: `color-mix(in oklch, ${data.color} 10%, transparent)`,
    opacity: data.dimmed ? 0.25 : 1,
    boxShadow: data.highlighted ? `0 0 12px color-mix(in oklch, ${data.color} 53%, transparent)` : undefined,
  }), [data.color, data.dimmed, data.highlighted])

  return (
    <>
      <Handle type="target" position={Position.Top} className="!bg-transparent !border-0 !w-0 !h-0" />
      <Tooltip>
        <TooltipTrigger asChild>
          <div
            className="flex flex-col items-center justify-center rounded-lg border-2 px-3 py-2 text-center transition-opacity duration-150 cursor-pointer"
            style={containerStyle}
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

const OperationalGatewayNode = memo(function OperationalGatewayNode({ data }: { data: ManifestNodeData }) {
  const node = data.manifestNode

  const containerStyle = useMemo(() => ({
    width: 180,
    borderColor: data.color,
    backgroundColor: `color-mix(in oklch, ${data.color} 10%, transparent)`,
    opacity: data.dimmed ? 0.25 : 1,
    boxShadow: data.highlighted ? `0 0 12px color-mix(in oklch, ${data.color} 53%, transparent)` : undefined,
  }), [data.color, data.dimmed, data.highlighted])

  return (
    <>
      <Handle type="target" position={Position.Top} className="!bg-transparent !border-0 !w-0 !h-0" />
      <Tooltip>
        <TooltipTrigger asChild>
          <div
            className="flex flex-col items-center justify-center rounded-lg border-2 px-3 py-2 text-center transition-opacity duration-150 cursor-pointer"
            style={containerStyle}
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

const ProviderConnectionNode = memo(function ProviderConnectionNode({ data }: { data: ManifestNodeData }) {
  const node = data.manifestNode
  const connectionId = node.data.connectionId as string

  const containerStyle = useMemo(() => ({
    width: 180,
    borderColor: data.color,
    backgroundColor: `color-mix(in oklch, ${data.color} 10%, transparent)`,
    opacity: data.dimmed ? 0.25 : 1,
    boxShadow: data.highlighted ? `0 0 12px color-mix(in oklch, ${data.color} 53%, transparent)` : undefined,
  }), [data.color, data.dimmed, data.highlighted])

  return (
    <>
      <Handle type="target" position={Position.Top} className="!bg-transparent !border-0 !w-0 !h-0" />
      <Tooltip>
        <TooltipTrigger asChild>
          <div
            className="flex flex-col items-center justify-center rounded-lg border-2 px-3 py-2 text-center transition-opacity duration-150 cursor-pointer"
            style={containerStyle}
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

const InstructionRouteNode = memo(function InstructionRouteNode({ data }: { data: ManifestNodeData }) {
  const node = data.manifestNode
  const connectionId = node.data.connectionId as string

  const containerStyle = useMemo(() => ({
    width: 180,
    borderColor: data.color,
    backgroundColor: `color-mix(in oklch, ${data.color} 10%, transparent)`,
    opacity: data.dimmed ? 0.25 : 1,
    boxShadow: data.highlighted ? `0 0 12px color-mix(in oklch, ${data.color} 53%, transparent)` : undefined,
  }), [data.color, data.dimmed, data.highlighted])

  return (
    <>
      <Handle type="target" position={Position.Top} className="!bg-transparent !border-0 !w-0 !h-0" />
      <Tooltip>
        <TooltipTrigger asChild>
          <div
            className="flex flex-col items-center justify-center rounded-lg border-2 px-3 py-2 text-center transition-opacity duration-150 cursor-pointer"
            style={containerStyle}
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

const PartyTypeNode = memo(function PartyTypeNode({ data }: { data: ManifestNodeData }) {
  const node = data.manifestNode

  const containerStyle = useMemo(() => ({
    width: 180,
    borderColor: data.color,
    backgroundColor: `color-mix(in oklch, ${data.color} 10%, transparent)`,
    opacity: data.dimmed ? 0.25 : 1,
    boxShadow: data.highlighted ? `0 0 12px color-mix(in oklch, ${data.color} 53%, transparent)` : undefined,
  }), [data.color, data.dimmed, data.highlighted])

  return (
    <>
      <Handle type="target" position={Position.Top} className="!bg-transparent !border-0 !w-0 !h-0" />
      <Tooltip>
        <TooltipTrigger asChild>
          <div
            className="flex flex-col items-center justify-center rounded-lg border-2 px-3 py-2 text-center transition-opacity duration-150 cursor-pointer"
            style={containerStyle}
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

const nodeTypes = {
  instrument: InstrumentNode,
  account_type: AccountTypeNode,
  valuation_rule: ValuationRuleNode,
  saga: SagaNode,
  market_data: MarketDataNode,
  organization: OrganizationNode,
  internal_account: InternalAccountNode,
  mapping: MappingNode,
  payment_rail: PaymentRailNode,
  operational_gateway: OperationalGatewayNode,
  provider_connection: ProviderConnectionNode,
  instruction_route: InstructionRouteNode,
  party_type: PartyTypeNode,
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

  // Compute connected instrument count per account_type node from actual edges
  const connectedInstruments = new Map<string, number>()
  for (const e of filteredEdges) {
    if (e.relationship === 'allowed_by') {
      connectedInstruments.set(e.source, (connectedInstruments.get(e.source) ?? 0) + 1)
    }
  }

  const layoutNodes = filteredNodes.map((n) => ({
    id: n.id,
    width: NODE_WIDTH,
    height: NODE_BASE_HEIGHT + NODE_PADDING,
    layoutOptions: {
      'elk.layered.layering.layerChoiceConstraint': LAYER_PRIORITY[n.type],
    },
  }))

  const rfEdges = buildReactFlowEdges(filteredEdges)

  const rfNodes = await layoutWithELK<ManifestNodeData>(
    layoutNodes,
    rfEdges,
    (id, position) => {
      const mn = nodeMap.get(id)!
      const color = NODE_TYPE_REGISTRY[mn.type].color
      return {
        id,
        type: mn.type,
        position,
        data: {
          manifestNode: mn,
          color,
          highlighted: false,
          dimmed: false,
          connectedInstrumentCount: connectedInstruments.get(id),
        } satisfies ManifestNodeData,
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
  /** @internal Suppresses the fullscreen button to prevent recursive nesting. */
  _fullscreen?: boolean
}

export function ManifestGraph({ manifest, className, _fullscreen }: ManifestGraphProps) {
  const navigate = useNavigate()
  const [nodes, setNodes, onNodesChange] = useNodesState<Node>([])
  const [edges, setEdges, onEdgesChange] = useEdgesState<Edge>([])
  const [hoveredNode, setHoveredNode] = useState<string | null>(null)
  const [selectedNode, setSelectedNode] = useState<string | null>(null)
  const [showEventChain, setShowEventChain] = useState(false)
  const [fullscreen, setFullscreen] = useState(false)
  const [editModalOpen, setEditModalOpen] = useState(false)
  const [visibleTypes, setVisibleTypes] = useState<Set<ManifestNodeType>>(
    () => new Set<ManifestNodeType>(Object.keys(NODE_TYPE_REGISTRY) as ManifestNodeType[]),
  )

  const graph = useMemo(() => buildManifestGraph(manifest), [manifest])

  // Derive effective selection: clear if the selected node no longer exists in the graph
  const effectiveSelectedNode = useMemo(
    () => (selectedNode && graph.nodes.some((n) => n.id === selectedNode) ? selectedNode : null),
    [selectedNode, graph],
  )

  const selectedManifestNode = useMemo(
    () => (effectiveSelectedNode ? graph.nodes.find((n) => n.id === effectiveSelectedNode) ?? null : null),
    [graph, effectiveSelectedNode],
  )

  const canShowEventChain = selectedManifestNode?.type === 'instrument' || selectedManifestNode?.type === 'account_type'
  const canEditResource = selectedManifestNode ? getResourceSchema(selectedManifestNode.type) !== undefined : false

  const eventChainNodeId = showEventChain ? effectiveSelectedNode : null
  const eventChain = useEventChain(graph, eventChainNodeId)

  const nodeCountByType = useMemo(() => {
    const counts = Object.fromEntries(
      (Object.keys(NODE_TYPE_REGISTRY) as ManifestNodeType[]).map((t) => [t, 0]),
    ) as Record<ManifestNodeType, number>
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

  // Hover + selection highlighting
  useEffect(() => {
    const activeNode = hoveredNode ?? effectiveSelectedNode
    const connectedNodes = new Set<string>()
    if (activeNode) {
      connectedNodes.add(activeNode)
      for (const e of currentEdges) {
        if (e.source === activeNode || e.target === activeNode) {
          connectedNodes.add(e.source)
          connectedNodes.add(e.target)
        }
      }
    }

    setNodes((nds) => {
      let changed = false
      const next = nds.map((n) => {
        const highlighted = activeNode ? n.id === activeNode : false
        const dimmed = activeNode ? !connectedNodes.has(n.id) : false
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
  }, [hoveredNode, effectiveSelectedNode, currentEdges, setNodes, setEdges])

  const onNodeClick: NodeMouseHandler = useCallback(
    (_event, node) => {
      setSelectedNode((prev) => (prev === node.id ? null : node.id))
      setShowEventChain(false)
    },
    [],
  )

  const onNodeDoubleClick: NodeMouseHandler = useCallback(
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
        case 'valuation_rule':
          navigate('/reference-data/valuation-rules')
          break
        case 'saga':
          navigate(`/starlark-config/${mn.label}`)
          break
        case 'market_data': {
          const code = mn.data.code as string | undefined
          navigate(code ? `/market-data/${code}` : '/market-data')
          break
        }
        case 'organization':
          navigate('/reference-data/nodes')
          break
        case 'internal_account':
          navigate('/internal-accounts')
          break
        case 'mapping':
          navigate('/gateway-mappings')
          break
        case 'payment_rail':
        case 'operational_gateway':
        case 'provider_connection':
        case 'instruction_route':
          navigate('/reference-data')
          break
        case 'party_type':
          navigate('/parties')
          break
      }
    },
    [navigate],
  )

  const onPaneClick = useCallback(() => {
    setSelectedNode(null)
    setShowEventChain(false)
  }, [])

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
    // Clear selection if the selected node's type was just hidden
    if (selectedManifestNode?.type === type) {
      setSelectedNode(null)
      setShowEventChain(false)
    }
  }, [selectedManifestNode])

  const totalVisible = nodes.length

  if (graph.nodes.length === 0) {
    return (
      <div className={`${className ?? ''} w-full h-full`} data-testid="manifest-graph-empty">
        <div className="flex items-center justify-center h-full text-muted-foreground text-sm">
          No elements in manifest to visualize.
        </div>
      </div>
    )
  }

  return (
    <div className={`${className ?? ''} w-full h-full relative`}>
      <TooltipProvider>
        <ReactFlow
          nodes={nodes}
          edges={edges}
          onNodesChange={onNodesChange}
          onEdgesChange={onEdgesChange}
          onNodeClick={onNodeClick}
          onNodeDoubleClick={onNodeDoubleClick}
          onPaneClick={onPaneClick}
          onNodeMouseEnter={onNodeMouseEnter}
          onNodeMouseLeave={onNodeMouseLeave}
          nodeTypes={nodeTypes}
          fitView
          fitViewOptions={{ padding: 0.3 }}
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

      {/* Selection toolbar */}
      {selectedManifestNode && (
        <div
          className="absolute top-3 right-3 z-10 flex items-center gap-2 rounded-lg border bg-background/95 p-2 backdrop-blur-sm shadow-sm"
          data-testid="node-toolbar"
        >
          <span className="text-xs font-medium text-foreground px-1">
            {selectedManifestNode.label}
          </span>
          {canEditResource && (
            <Button
              size="sm"
              variant="outline"
              className="text-xs h-7"
              onClick={() => setEditModalOpen(true)}
              data-testid="edit-resource-button"
            >
              <Pencil className="h-3 w-3 mr-1" />
              Edit
            </Button>
          )}
          {canShowEventChain && (
            <Button
              size="sm"
              variant="outline"
              className="text-xs h-7"
              onClick={() => setShowEventChain(true)}
              data-testid="show-event-chain-button"
            >
              Show Event Chain
            </Button>
          )}
          <Button
            size="sm"
            variant="ghost"
            className="h-7 w-7 p-0"
            onClick={() => { setSelectedNode(null); setShowEventChain(false) }}
            aria-label="Deselect node"
          >
            <X className="h-3.5 w-3.5" />
          </Button>
        </div>
      )}

      {/* Event chain side panel */}
      {showEventChain && eventChain && selectedManifestNode && (
        <div
          className="absolute top-0 right-0 z-20 h-full w-96 border-l bg-background shadow-lg overflow-y-auto"
          data-testid="event-chain-side-panel"
        >
          <div className="flex items-center justify-between p-3 border-b">
            <h3 className="text-sm font-semibold">Event Chain</h3>
            <Button
              size="sm"
              variant="ghost"
              className="h-7 w-7 p-0"
              onClick={() => { setSelectedNode(null); setShowEventChain(false) }}
              aria-label="Close event chain panel"
              data-testid="close-event-chain-panel"
            >
              <X className="h-3.5 w-3.5" />
            </Button>
          </div>
          <div className="p-3">
            <EventChainPanel
              chain={eventChain}
              startNodeLabel={selectedManifestNode.label}
              onSagaClick={(sagaId) => navigate(`/starlark-config/${sagaId.replace(/^saga:/, '')}`)}
            />
          </div>
        </div>
      )}

      {/* Fullscreen button + dialog — suppressed in nested (already-fullscreen) instances */}
      {!_fullscreen && !selectedManifestNode && (
        <Button
          variant="outline"
          size="icon"
          className="absolute top-3 right-3 z-10 size-8 bg-background/80 backdrop-blur-sm"
          onClick={() => setFullscreen(true)}
          aria-label="View fullscreen"
        >
          <Maximize2 className="size-4" />
        </Button>
      )}

      {!_fullscreen && (
        <Dialog open={fullscreen} onOpenChange={setFullscreen}>
          <DialogContent className="max-w-[calc(100vw-2rem)] h-[calc(100vh-2rem)] sm:max-w-[calc(100vw-4rem)] sm:h-[calc(100vh-4rem)] flex flex-col p-4">
            <DialogHeader className="shrink-0">
              <DialogTitle>Economy Graph</DialogTitle>
            </DialogHeader>
            <div className="flex-1 min-h-0 rounded-lg border">
              <ManifestGraph manifest={manifest} className="h-full w-full" _fullscreen />
            </div>
          </DialogContent>
        </Dialog>
      )}

      {selectedManifestNode && canEditResource && (
        <ApplyResourceModal
          key={selectedManifestNode.id}
          open={editModalOpen}
          onOpenChange={setEditModalOpen}
          nodeType={selectedManifestNode.type}
          initialData={selectedManifestNode.data}
        />
      )}
    </div>
  )
}
