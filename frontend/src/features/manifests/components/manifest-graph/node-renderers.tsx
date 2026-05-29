/* eslint-disable react-refresh/only-export-components -- This module is the React
 * Flow node-type registry: it defines internal node renderers and exports the
 * `nodeTypes` map plus small helpers/types, not fast-refreshable page components. */
import { memo, useMemo, type ReactNode } from 'react'
import { Position, Handle } from '@xyflow/react'
import {
  Tooltip,
  TooltipContent,
  TooltipTrigger,
} from '@/components/ui/tooltip'
import type { ManifestNode } from '../../lib/manifest-graph-model'

/** Data attached to every React Flow node rendered by the manifest graph. */
export interface ManifestNodeData {
  manifestNode: ManifestNode
  color: string
  highlighted: boolean
  dimmed: boolean
  connectedInstrumentCount?: number
  [key: string]: unknown
}

interface NodeProps {
  data: ManifestNodeData
}

/** Maps a saga trigger string to a short badge label and Tailwind variant. */
export function getTriggerBadge(trigger: string): { label: string; variant: string } {
  if (trigger.startsWith('event:')) return { label: 'event', variant: 'bg-accent text-accent-foreground' }
  if (trigger.startsWith('scheduled:')) return { label: 'scheduled', variant: 'bg-info-muted text-info-foreground' }
  if (trigger.startsWith('api:')) return { label: 'api', variant: 'bg-success-muted text-success-foreground' }
  return { label: 'unknown', variant: 'bg-muted text-muted-foreground' }
}

const HANDLE_CLASS = '!bg-transparent !border-0 !w-0 !h-0'

const CARD_CLASS =
  'flex flex-col items-center justify-center rounded-lg border-2 px-3 py-2 text-center transition-opacity duration-150 cursor-pointer'

/** Invisible handles on all four sides so edges can attach to the nearest side. */
function FourSideHandles() {
  return (
    <>
      <Handle id="top" type="source" position={Position.Top} className={HANDLE_CLASS} />
      <Handle id="top-target" type="target" position={Position.Top} className={HANDLE_CLASS} />
      <Handle id="bottom" type="source" position={Position.Bottom} className={HANDLE_CLASS} />
      <Handle id="bottom-target" type="target" position={Position.Bottom} className={HANDLE_CLASS} />
      <Handle id="left" type="source" position={Position.Left} className={HANDLE_CLASS} />
      <Handle id="left-target" type="target" position={Position.Left} className={HANDLE_CLASS} />
      <Handle id="right" type="source" position={Position.Right} className={HANDLE_CLASS} />
      <Handle id="right-target" type="target" position={Position.Right} className={HANDLE_CLASS} />
    </>
  )
}

/** Themed container style shared by every node card (color, dim, highlight glow). */
function useNodeContainerStyle(data: ManifestNodeData) {
  return useMemo(
    () => ({
      width: 180,
      borderColor: data.color,
      backgroundColor: `color-mix(in oklch, ${data.color} 10%, transparent)`,
      opacity: data.dimmed ? 0.25 : 1,
      boxShadow: data.highlighted
        ? `0 0 12px color-mix(in oklch, ${data.color} 53%, transparent)`
        : undefined,
    }),
    [data.color, data.dimmed, data.highlighted],
  )
}

/**
 * Shared wrapper for all node renderers: four-side handles, the themed card,
 * and a tooltip. Renderers only supply their inner content and tooltip text.
 */
function NodeShell({
  data,
  tooltip,
  children,
}: {
  data: ManifestNodeData
  tooltip: ReactNode
  children: ReactNode
}) {
  const containerStyle = useNodeContainerStyle(data)
  return (
    <>
      <FourSideHandles />
      <Tooltip>
        <TooltipTrigger asChild>
          <div className={CARD_CLASS} style={containerStyle}>
            {children}
          </div>
        </TooltipTrigger>
        <TooltipContent side="top">{tooltip}</TooltipContent>
      </Tooltip>
    </>
  )
}

// Shared label primitives keep per-node markup tiny and consistent.
const CodeLabel = ({ children, truncate }: { children: ReactNode; truncate?: boolean }) => (
  <span className={`text-[11px] font-bold font-mono text-foreground${truncate ? ' truncate w-full' : ''}`}>
    {children}
  </span>
)
const TitleLabel = ({ children, mono }: { children: ReactNode; mono?: boolean }) => (
  <span className={`text-[11px] font-bold text-foreground truncate w-full${mono ? ' font-mono' : ''}`}>
    {children}
  </span>
)
const NameLabel = ({ children }: { children: ReactNode }) => (
  <span className="text-[10px] text-muted-foreground truncate w-full">{children}</span>
)
const SubLabel = ({ children, mono }: { children: ReactNode; mono?: boolean }) => (
  <span className={`text-[9px] text-muted-foreground${mono ? ' font-mono' : ''}`}>{children}</span>
)

const InstrumentNode = memo(function InstrumentNode({ data }: NodeProps) {
  const node = data.manifestNode
  const code = node.data.code as string
  const unit = (node.data.dimensions as Record<string, unknown> | undefined)?.unit as string | undefined
  return (
    <NodeShell data={data} tooltip={`${node.label} (${code})`}>
      <CodeLabel>{code}</CodeLabel>
      <NameLabel>{node.label}</NameLabel>
      {unit && <SubLabel>({unit})</SubLabel>}
    </NodeShell>
  )
})

const AccountTypeNode = memo(function AccountTypeNode({ data }: NodeProps) {
  const node = data.manifestNode
  const code = node.data.code as string
  const allowedCount = data.connectedInstrumentCount ?? 0
  return (
    <NodeShell data={data} tooltip={`${node.label} (${code})`}>
      <CodeLabel>{code}</CodeLabel>
      <NameLabel>{node.label}</NameLabel>
      <SubLabel>
        {allowedCount} instrument{allowedCount !== 1 ? 's' : ''}
      </SubLabel>
    </NodeShell>
  )
})

const ValuationRuleNode = memo(function ValuationRuleNode({ data }: NodeProps) {
  const node = data.manifestNode
  const from = node.data.fromInstrument as string
  const to = node.data.toInstrument as string
  return (
    <NodeShell data={data} tooltip={`Valuation: ${from} to ${to}`}>
      <span className="text-[10px] font-semibold text-foreground">
        {from} &rarr; {to}
      </span>
    </NodeShell>
  )
})

const SagaNode = memo(function SagaNode({ data }: NodeProps) {
  const node = data.manifestNode
  const trigger = node.data.trigger as string
  const badge = getTriggerBadge(trigger)
  return (
    <NodeShell data={data} tooltip={`${node.label} (${trigger})`}>
      <TitleLabel>{node.label}</TitleLabel>
      <span className={`mt-0.5 text-[9px] font-medium px-1.5 py-0.5 rounded-full ${badge.variant}`}>
        {badge.label}
      </span>
    </NodeShell>
  )
})

const MarketDataNode = memo(function MarketDataNode({ data }: NodeProps) {
  const node = data.manifestNode
  const code = node.data.code as string
  return (
    <NodeShell data={data} tooltip={`${node.label} (${code})`}>
      <CodeLabel>{code}</CodeLabel>
      <NameLabel>{node.label}</NameLabel>
    </NodeShell>
  )
})

const OrganizationNode = memo(function OrganizationNode({ data }: NodeProps) {
  const node = data.manifestNode
  const code = node.data.code as string
  return (
    <NodeShell data={data} tooltip={`${node.label} (${code})`}>
      <CodeLabel>{code}</CodeLabel>
      <NameLabel>{node.label}</NameLabel>
    </NodeShell>
  )
})

const InternalAccountNode = memo(function InternalAccountNode({ data }: NodeProps) {
  const node = data.manifestNode
  const code = node.data.code as string
  const accountType = node.data.accountType as string | undefined
  return (
    <NodeShell data={data} tooltip={`${node.label} (${code})`}>
      <CodeLabel>{code}</CodeLabel>
      <NameLabel>{node.label}</NameLabel>
      {accountType && <SubLabel>{accountType}</SubLabel>}
    </NodeShell>
  )
})

const MappingNode = memo(function MappingNode({ data }: NodeProps) {
  const node = data.manifestNode
  return (
    <NodeShell data={data} tooltip={`Mapping: ${node.label}`}>
      <TitleLabel mono>{node.label}</TitleLabel>
    </NodeShell>
  )
})

const PaymentRailNode = memo(function PaymentRailNode({ data }: NodeProps) {
  const node = data.manifestNode
  const provider = node.data.provider as string
  return (
    <NodeShell data={data} tooltip={`Payment Rail: ${provider}`}>
      <CodeLabel>{provider}</CodeLabel>
      <NameLabel>{node.label}</NameLabel>
    </NodeShell>
  )
})

const OperationalGatewayNode = memo(function OperationalGatewayNode({ data }: NodeProps) {
  const node = data.manifestNode
  return (
    <NodeShell data={data} tooltip={node.label}>
      <TitleLabel>{node.label}</TitleLabel>
    </NodeShell>
  )
})

const ProviderConnectionNode = memo(function ProviderConnectionNode({ data }: NodeProps) {
  const node = data.manifestNode
  const connectionId = node.data.connectionId as string
  return (
    <NodeShell data={data} tooltip={`${node.label} (${connectionId})`}>
      <TitleLabel>{node.label}</TitleLabel>
      <SubLabel mono>{connectionId}</SubLabel>
    </NodeShell>
  )
})

const InstructionRouteNode = memo(function InstructionRouteNode({ data }: NodeProps) {
  const node = data.manifestNode
  const connectionId = node.data.connectionId as string
  return (
    <NodeShell data={data} tooltip={`${node.label} via ${connectionId}`}>
      <TitleLabel>{node.label}</TitleLabel>
      <SubLabel mono>{connectionId}</SubLabel>
    </NodeShell>
  )
})

const EventChannelNode = memo(function EventChannelNode({ data }: NodeProps) {
  const node = data.manifestNode
  const channel = node.data.channel as string
  return (
    <NodeShell data={data} tooltip={`Event Channel: ${channel}`}>
      <CodeLabel truncate>{channel}</CodeLabel>
    </NodeShell>
  )
})

const PartyTypeNode = memo(function PartyTypeNode({ data }: NodeProps) {
  const node = data.manifestNode
  return (
    <NodeShell data={data} tooltip={`Party Type: ${node.label}`}>
      <TitleLabel>{node.label}</TitleLabel>
    </NodeShell>
  )
})

/** React Flow node type registry mapping manifest node types to renderers. */
export const nodeTypes = {
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
  event_channel: EventChannelNode,
}
